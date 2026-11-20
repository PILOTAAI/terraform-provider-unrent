// Package provider is the Unrent provider for Terraform and OpenTofu (docs/terraform.md), built on the Terraform
// Plugin Framework. It manages an organisation's projects, environments, services, variables, databases and links,
// domains, crons, storage targets, notification channels, status page and project roles through the public API
// only (https://api.unrent.dev/v1). Jobs that need a person's passkey are never bypassed: the provider prints the
// approval page and waits for it (approval_timeout), or fails with it. Secret values are write-only attributes,
// sealed to the server's public key before they leave the machine, so they are never in the plan or the state.
package provider

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// DefaultAPI is the public API's origin.
const DefaultAPI = "https://api.unrent.dev"

var (
	_ provider.Provider = (*unrentProvider)(nil)
)

type unrentProvider struct {
	version string
	// getenv and httpClient are swappable in tests.
	getenv     func(string) string
	httpClient *http.Client
}

// New returns the provider factory providerserver.NewProtocol6 serves.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &unrentProvider{version: version, getenv: os.Getenv} }
}

type githubActionsModel struct {
	Environment types.String `tfsdk:"environment"`
}

type providerModel struct {
	APIURL          types.String        `tfsdk:"api_url"`
	Org             types.String        `tfsdk:"org"`
	Token           types.String        `tfsdk:"token"`
	GitHubActions   *githubActionsModel `tfsdk:"github_actions"`
	ApprovalTimeout types.String        `tfsdk:"approval_timeout"`
	JobTimeout      types.String        `tfsdk:"job_timeout"`
}

func (p *unrentProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "unrent"
	resp.Version = p.version
}

var durationRe = regexp.MustCompile(`^[0-9]+(s|m|h)$`)

func (p *unrentProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage an Unrent organisation (https://unrent.dev) through its public API. Jobs that need a person's passkey print the approval page and wait for it; secret values are write-only and sealed to the server before they are sent.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The API origin (default `https://api.unrent.dev`, or `UNRENT_API`).",
				Validators:          []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^https?://[^/?#]+$`), "an origin such as https://api.unrent.dev")},
			},
			"org": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The organisation id (`org_…`, or `UNRENT_ORG`). A token belongs to one organisation, so this is only needed for a GitHub Actions exchange; otherwise it is checked against the token's.",
				Validators:          []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^org_[0-9a-z]{16,32}$`), "an organisation id (org_…)")},
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "A personal token (`unr_pat_…`) with the `read` and `manage` scopes, or `UNRENT_TOKEN`. Without one the provider uses `github_actions`, then the token of `unrent login`.",
			},
			"approval_timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "How long to wait for a person to approve a held job with a passkey, e.g. `15m` (default). `0s` fails at once with the approval page.",
				Validators:          []validator.String{stringvalidator.RegexMatches(durationRe, "a duration such as 30s, 15m or 1h")},
			},
			"job_timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "How long to wait for a server to finish a job (a deployment, a database, a cron), e.g. `10m` (default).",
				Validators:          []validator.String{stringvalidator.RegexMatches(durationRe, "a duration such as 30s, 15m or 1h")},
			},
		},
		Blocks: map[string]schema.Block{
			"github_actions": schema.SingleNestedBlock{
				MarkdownDescription: "Exchange the running GitHub Actions job's OIDC token for a ten-minute token bound to one environment (the repository must be linked in Settings → CI; the job needs `permissions: id-token: write`). Renewed when it expires during a long apply.",
				Attributes: map[string]schema.Attribute{
					"environment": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The Unrent environment the repository is linked to.",
						Validators:          []validator.String{envValidator()},
					},
				},
			},
		},
	}
}

func durationOr(v types.String, def time.Duration) time.Duration {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return def
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil {
		return def
	}
	return d
}

func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (p *unrentProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	getenv := p.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	h := p.httpClient
	if h == nil {
		h = &http.Client{Timeout: 60 * time.Second}
	}
	api := strings.TrimRight(firstSet(m.APIURL.ValueString(), getenv("UNRENT_API"), DefaultAPI), "/")
	org := firstSet(m.Org.ValueString(), getenv("UNRENT_ORG"))
	c := &client.Client{
		API:             api,
		Org:             org,
		HTTP:            h,
		UserAgent:       "terraform-provider-unrent/" + p.version,
		ApprovalTimeout: durationOr(m.ApprovalTimeout, 15*time.Minute),
		JobTimeout:      durationOr(m.JobTimeout, 10*time.Minute),
		Log:             func(ctx context.Context, msg string) { tflog.Warn(ctx, msg) },
	}
	token := firstSet(m.Token.ValueString(), getenv("UNRENT_TOKEN"))
	switch {
	case token != "":
		c.Token = token
	case m.GitHubActions != nil:
		env := m.GitHubActions.Environment.ValueString()
		if org == "" || env == "" {
			resp.Diagnostics.AddAttributeError(path.Root("github_actions"), "GitHub Actions needs org and environment", "Set org (or UNRENT_ORG) and github_actions.environment: the exchange asks for a token of that organisation and environment.")
			return
		}
		exchange := func(ctx context.Context) (string, error) {
			t, _, err := client.GitHubActions(ctx, h, api, org, env, getenv)
			return t, err
		}
		t, err := exchange(ctx)
		if err != nil {
			resp.Diagnostics.AddError("GitHub Actions token exchange failed", err.Error())
			return
		}
		c.Token, c.Refresh = t, exchange
	default:
		t, o, err := client.CLICredentials(api, getenv, time.Now())
		if err != nil {
			resp.Diagnostics.AddError("No Unrent token", "Set token (or UNRENT_TOKEN) to a personal token with the read and manage scopes (Settings → API tokens), use github_actions in a workflow, or sign in with `unrent login`. ("+err.Error()+")")
			return
		}
		c.Token = t
		if org == "" {
			c.Org = o
		}
	}
	me, err := c.Me(ctx)
	if err != nil {
		resp.Diagnostics.AddError("The Unrent API refused the token", err.Error())
		return
	}
	if c.Org != "" && c.Org != me.Org.ID {
		resp.Diagnostics.AddError("Wrong organisation", "The token belongs to "+me.Org.ID+", not "+c.Org+".")
		return
	}
	c.Org = me.Org.ID
	if !contains(me.Scopes, "manage") {
		tflog.Warn(ctx, "the token has no manage scope: plans and reads work, changes will be refused (insufficient_scope)")
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (p *unrentProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newProjectResource,
		newProjectRoleResource,
		newEnvironmentResource,
		newServiceResource,
		newServiceEnvResource,
		newCronResource,
		newDatabaseResource,
		newDatabaseLinkResource,
		newDomainResource,
		newStorageTargetResource,
		newChannelResource,
		newStatusPageResource,
		newStatusComponentResource,
		// WP2.14: environments v2 (docs/environments-v2.md §7)
		newProtectionResource,
		newVariablesResource,
		newBranchEnvsResource,
		newEnvTemplateResource,
		// P21: sandboxes (docs/sandboxes.md §4)
		newSandboxSettingsResource,
	}
}

func (p *unrentProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newServersDataSource,
		newReleasesDataSource,
		newProjectDataSource,
		newMembersDataSource,
		newEnvironmentsDataSource, // environments-v3 §2.5
		newSandboxesDataSource,    // P21
	}
}
