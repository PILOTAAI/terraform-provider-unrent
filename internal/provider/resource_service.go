package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type serviceResource struct{ base }

func newServiceResource() resource.Resource { return &serviceResource{} }

type repositoryModel struct {
	Type           types.String `tfsdk:"type"`
	Connection     types.String `tfsdk:"connection"`
	InstallationID types.Int64  `tfsdk:"installation_id"`
	RepoID         types.String `tfsdk:"repo_id"`
	URL            types.String `tfsdk:"url"`
	Branch         types.String `tfsdk:"branch"`
	RootDir        types.String `tfsdk:"root_dir"`
	Dockerfile     types.String `tfsdk:"dockerfile"`
	AutoDeploy     types.Bool   `tfsdk:"auto_deploy"`
	// WP2.14: the deploy rule (docs/environments-v2.md §4.1); branch + auto_deploy stay its shorthand
	DeployRule *deployRuleModel `tfsdk:"deploy_rule"`
}

type serviceModel struct {
	ID                    types.String     `tfsdk:"id"`
	Environment           types.String     `tfsdk:"environment"`
	Name                  types.String     `tfsdk:"name"`
	ServerID              types.String     `tfsdk:"server_id"`
	Port                  types.Int64      `tfsdk:"port"`
	MemoryMB              types.Int64      `tfsdk:"memory_mb"`
	HealthCheckPath       types.String     `tfsdk:"health_check_path"`
	Image                 types.String     `tfsdk:"image"`
	RegistryCredentialID  types.String     `tfsdk:"registry_credential_id"`
	Repository            *repositoryModel `tfsdk:"repository"`
	RepositoryAccessToken types.String     `tfsdk:"repository_access_token_wo"`
	WaitForDeployment     types.Bool       `tfsdk:"wait_for_deployment"`
	State                 types.String     `tfsdk:"state"`
	LiveDigest            types.String     `tfsdk:"live_digest"`
	Recipient             types.String     `tfsdk:"recipient"`
}

type serviceAPI struct {
	Env      string  `json:"env"`
	Name     string  `json:"name"`
	AgentID  string  `json:"agent_id"`
	Image    string  `json:"image"`
	Port     int64   `json:"port"`
	MemoryMB *int64  `json:"memory_mb"`
	State    string  `json:"state"`
	Recip    *string `json:"recipient"`
	Health   struct {
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"health"`
	Live *struct {
		Digest string `json:"digest"`
	} `json:"live"`
	Source struct {
		Type               string               `json:"type"`
		RegistryCredential *struct{ ID string } `json:"registry_credential"`
		InstallationID     *int64               `json:"installation_id"`
		RepoID             json.RawMessage      `json:"repo_id"`
		Connection         *string              `json:"connection"`
		CloneURL           *string              `json:"clone_url"`
		Branch             *string              `json:"branch"`
		RootDir            *string              `json:"root_dir"`
		Dockerfile         *string              `json:"dockerfile"`
		AutoDeploy         *bool                `json:"auto_deploy"`
	} `json:"source"`
}

func (r *serviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (r *serviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A service: a container from a public or private image, or built from a repository (GitHub, GitLab, Bitbucket, Gitea or any SSH Git URL), on one server. Created and deployed in one step. Changing the image redeploys it. Destroying it is a destructive job: the provider prints the approval page and waits for an approver's passkey.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "`<environment>/<name>`", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"name":        schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{nameValidator()}},
			"server_id":   schema.StringAttribute{Required: true, PlanModifiers: replace, MarkdownDescription: "The server it runs on (`agt_…`, from `data.unrent_servers`).", Validators: []validator.String{agentValidator()}},
			"port":        schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}, Validators: []validator.Int64{int64validator.Between(1, 65535)}},
			"memory_mb":   schema.Int64Attribute{Optional: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}, Validators: []validator.Int64{int64validator.Between(64, 262144)}},
			"health_check_path": schema.StringAttribute{
				Optional: true, PlanModifiers: replace,
				MarkdownDescription: "An HTTP path that answers 2xx when the service is healthy; without one the health check is a TCP connect.",
				Validators:          []validator.String{stringvalidator.RegexMatches(regexpMust(`^/[A-Za-z0-9._~/?=&%-]{0,200}$`), "a path starting with /")},
			},
			"image": schema.StringAttribute{
				Optional: true, MarkdownDescription: "An image (`ghcr.io/acme/web:1.2`, or pinned `…@sha256:…`). Changing it deploys the new image. Public images from docker.io, ghcr.io, public.ecr.aws, quay.io or registry.gitlab.com, or any image with `registry_credential_id`.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 512)},
			},
			"registry_credential_id": schema.StringAttribute{Optional: true, MarkdownDescription: "A registry credential (Settings → Registries) for a private image."},
			"repository": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Build from a repository instead of an image. The repository and its provider are fixed; branch, directories and auto-deploy can change.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplaceIf(repoReplaced, "the repository changed", "the repository changed")},
				Attributes: map[string]schema.Attribute{
					"type":            schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("github", "gitlab", "bitbucket", "gitea", "git")}},
					"connection":      schema.StringAttribute{Optional: true, MarkdownDescription: "gitlab, bitbucket, gitea: the Git connection (Settings → Git providers)."},
					"installation_id": schema.Int64Attribute{Optional: true, MarkdownDescription: "github: the GitHub App installation id."},
					"repo_id":         schema.StringAttribute{Optional: true, MarkdownDescription: "github, gitlab, bitbucket, gitea: the provider's repository id."},
					"url":             schema.StringAttribute{Optional: true, MarkdownDescription: "git: an SSH URL such as `git@git.example.com:team/app.git`."},
					"branch":          schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"root_dir":        schema.StringAttribute{Optional: true},
					"dockerfile":      schema.StringAttribute{Optional: true},
					"auto_deploy":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"deploy_rule":     deployRuleSchema(), // WP2.14 (deploy_rule.go)
				},
			},
			"repository_access_token_wo": schema.StringAttribute{
				Optional: true, WriteOnly: true, Sensitive: true,
				MarkdownDescription: "bitbucket: a repository access token, sealed to the server before it is sent; never stored in the plan or state (write-only, Terraform/OpenTofu ≥ 1.11).",
			},
			"wait_for_deployment": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Wait until a deployment succeeded (default true); a failed one is an error."},
			"state":               schema.StringAttribute{Computed: true, MarkdownDescription: "running, stopped, deploying, failed, never_deployed, …"},
			"live_digest":         schema.StringAttribute{Computed: true, MarkdownDescription: "The image digest that runs now."},
			"recipient":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "The server's public age key (secrets are sealed to it)."},
		},
	}
}

func repoReplaced(ctx context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
	var before, after repositoryModel
	if req.StateValue.IsNull() || req.PlanValue.IsNull() {
		resp.RequiresReplace = !(req.StateValue.IsNull() && req.PlanValue.IsNull())
		return
	}
	req.StateValue.As(ctx, &before, objectAs)
	req.PlanValue.As(ctx, &after, objectAs)
	resp.RequiresReplace = !before.Type.Equal(after.Type) || !before.Connection.Equal(after.Connection) || !before.InstallationID.Equal(after.InstallationID) || !before.RepoID.Equal(after.RepoID) || !before.URL.Equal(after.URL)
}

func (r *serviceResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("image"), path.MatchRoot("repository")),
		resourcevalidator.Conflicting(path.MatchRoot("repository"), path.MatchRoot("registry_credential_id")),
	}
}

func (r *serviceResource) get(ctx context.Context, env, name string) (*serviceAPI, error) {
	var s serviceAPI
	if err := r.c.Get(ctx, client.OpGetService, client.Params{"env": env, "name": name}, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// set copies what the API says into the model (drift shows as a plan against the configuration).
func (r *serviceResource) set(m *serviceModel, s *serviceAPI) {
	m.ID = types.StringValue(s.Env + "/" + s.Name)
	m.Environment = types.StringValue(s.Env)
	m.Name = types.StringValue(s.Name)
	m.ServerID = types.StringValue(s.AgentID)
	m.Port = types.Int64Value(s.Port)
	if s.MemoryMB != nil {
		m.MemoryMB = types.Int64Value(*s.MemoryMB)
	} else {
		m.MemoryMB = types.Int64Null()
	}
	if s.Health.Type == "http" {
		m.HealthCheckPath = types.StringValue(s.Health.Path)
	} else {
		m.HealthCheckPath = types.StringNull()
	}
	m.State = types.StringValue(s.State)
	if s.Live != nil {
		m.LiveDigest = types.StringValue(s.Live.Digest)
	} else {
		m.LiveDigest = types.StringNull()
	}
	m.Recipient = nullStr(s.Recip)
	if m.WaitForDeployment.IsNull() || m.WaitForDeployment.IsUnknown() {
		m.WaitForDeployment = types.BoolValue(true)
	}
	src := s.Source
	if src.Type == "" || src.Type == "image" {
		m.Image = types.StringValue(s.Image)
		m.Repository = nil
		if src.RegistryCredential != nil {
			m.RegistryCredentialID = types.StringValue(src.RegistryCredential.ID)
		} else {
			m.RegistryCredentialID = types.StringNull()
		}
		return
	}
	m.Image = types.StringNull()
	m.RegistryCredentialID = types.StringNull()
	prior := m.Repository
	rm := &repositoryModel{Type: types.StringValue(src.Type), Connection: nullStr(src.Connection), InstallationID: types.Int64Null(), RepoID: types.StringNull(), URL: types.StringNull(),
		Branch: nullStr(src.Branch), RootDir: nullStr(src.RootDir), Dockerfile: nullStr(src.Dockerfile), AutoDeploy: types.BoolValue(src.AutoDeploy == nil || *src.AutoDeploy)}
	if src.InstallationID != nil {
		rm.InstallationID = types.Int64Value(*src.InstallationID)
	}
	if len(src.RepoID) > 0 && string(src.RepoID) != "null" {
		var sid string
		if json.Unmarshal(src.RepoID, &sid) != nil {
			var n int64
			if json.Unmarshal(src.RepoID, &n) == nil {
				sid = strconv.FormatInt(n, 10)
			}
		}
		if src.Type != "git" && sid != "" {
			rm.RepoID = types.StringValue(sid)
		}
	}
	if src.Type == "git" {
		// The API keeps the normalised SSH URL: keep the configured spelling when it names the same repository.
		rm.URL = nullStr(src.CloneURL)
		if prior != nil && !prior.URL.IsNull() {
			rm.URL = prior.URL
		}
	}
	if src.Type == "github" {
		rm.Connection = types.StringNull()
	}
	m.Repository = rm
}

func (r *serviceResource) createBody(ctx context.Context, m *serviceModel, accessToken string) (map[string]any, error) {
	b := map[string]any{"env": m.Environment.ValueString(), "name": m.Name.ValueString(), "agent_id": m.ServerID.ValueString(), "port": m.Port.ValueInt64()}
	if !m.MemoryMB.IsNull() && !m.MemoryMB.IsUnknown() {
		b["memory_mb"] = m.MemoryMB.ValueInt64()
	}
	if p := str(m.HealthCheckPath); p != "" {
		b["health"] = map[string]any{"type": "http", "path": p}
	}
	if m.Repository == nil {
		b["image"] = m.Image.ValueString()
		if id := str(m.RegistryCredentialID); id != "" {
			b["source"] = map[string]any{"type": "image", "registry_credential": id}
		}
		return b, nil
	}
	rp := m.Repository
	src := map[string]any{"type": rp.Type.ValueString()}
	for k, v := range map[string]types.String{"connection": rp.Connection, "url": rp.URL, "branch": rp.Branch, "root_dir": rp.RootDir, "dockerfile": rp.Dockerfile} {
		if s := str(v); s != "" {
			src[k] = s
		}
	}
	if !rp.InstallationID.IsNull() && !rp.InstallationID.IsUnknown() {
		src["installation_id"] = rp.InstallationID.ValueInt64()
	}
	if id := str(rp.RepoID); id != "" {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && rp.Type.ValueString() == "github" {
			src["repo_id"] = n
		} else {
			src["repo_id"] = id
		}
	}
	if !rp.AutoDeploy.IsNull() && !rp.AutoDeploy.IsUnknown() {
		src["auto_deploy"] = rp.AutoDeploy.ValueBool()
	}
	if accessToken != "" {
		recipient, err := serverRecipient(ctx, r.c, m.ServerID.ValueString())
		if err != nil {
			return nil, err
		}
		sealed, err := client.Seal(recipient, accessToken)
		if err != nil {
			return nil, err
		}
		src["sealed_token"] = map[string]any{m.ServerID.ValueString(): sealed}
	}
	b["source"] = src
	return b, nil
}

// serverRecipient is a server's public age key (from the storage targets view, which lists every server's).
func serverRecipient(ctx context.Context, c *client.Client, agentID string) (string, error) {
	var out struct {
		Servers []struct {
			AgentID   string `json:"agent_id"`
			Recipient string `json:"recipient"`
		} `json:"servers"`
	}
	if err := c.Get(ctx, client.OpListStorageTargets, nil, nil, &out); err != nil {
		return "", err
	}
	for _, s := range out.Servers {
		if s.AgentID == agentID && s.Recipient != "" {
			return s.Recipient, nil
		}
	}
	return "", errorf("server %s has no public key yet (is it enrolled?): secrets cannot be sealed to it", agentID)
}

func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	var token types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("repository_access_token_wo"), &token)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := r.createBody(ctx, &m, str(token))
	if err != nil {
		fail(&resp.Diagnostics, "Preparing the service failed", err)
		return
	}
	var out struct {
		Deployment *deployment `json:"deployment"`
	}
	appr, err := r.c.Do(ctx, client.OpCreateService, nil, body, &out)
	depID := ""
	if out.Deployment != nil {
		depID = out.Deployment.ID
	}
	if appr != nil {
		depID = appr.DeploymentID
	}
	// The service exists once the API accepted it (201, or 202 with its first job held), even when that first
	// deployment then fails or waits. A refusal (e.g. 409: a service of that name exists) must never adopt the
	// existing service into this state: import it instead.
	accepted := out.Deployment != nil || appr != nil
	if accepted {
		if s, gerr := r.get(ctx, m.Environment.ValueString(), m.Name.ValueString()); gerr == nil {
			r.set(&m, s)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		}
	}
	var ae *client.ApprovalError
	if errors.As(err, &ae) && accepted {
		// Created; its first deployment waits for a passkey. Not a failed create (that would taint the service).
		resp.Diagnostics.AddWarning("The first deployment waits for approval", ae.Error())
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Creating the service failed", err)
		return
	}
	if m.WaitForDeployment.ValueBool() {
		if _, err := waitDeployment(ctx, r.c, depID); err != nil {
			if errors.As(err, &ae) {
				resp.Diagnostics.AddWarning("The first deployment waits for approval", ae.Error())
				return
			}
			fail(&resp.Diagnostics, "The service's first deployment did not succeed", err)
			return
		}
	}
	// ---------- WP2.14: the deploy rule, once the service exists ----------
	if m.Repository != nil && m.Repository.DeployRule != nil {
		if err := putDeployRule(ctx, r.c, m.Environment.ValueString(), m.Name.ValueString(), m.Repository.DeployRule); err != nil {
			fail(&resp.Diagnostics, "Setting the deploy rule failed", err)
			return
		}
	}
	rule := ruleOf(m.Repository)
	if s, err := r.get(ctx, m.Environment.ValueString(), m.Name.ValueString()); err == nil {
		r.set(&m, s)
	}
	r.refreshRule(ctx, &m, rule, false, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.get(ctx, m.Environment.ValueString(), m.Name.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the service failed", err)
		return
	}
	rule := ruleOf(m.Repository)
	importing := m.ServerID.IsNull() // only the id and names: just imported
	r.set(&m, s)
	r.refreshRule(ctx, &m, rule, importing, &resp.Diagnostics) // WP2.14: managed, or imported
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := client.Params{"env": m.Environment.ValueString(), "name": m.Name.ValueString()}
	// Source settings first (a repository's branch, an image's credential), then a deployment of a new image.
	src := map[string]any{}
	if m.Repository != nil && st.Repository != nil {
		for k, pair := range map[string][2]types.String{"branch": {m.Repository.Branch, st.Repository.Branch}, "root_dir": {m.Repository.RootDir, st.Repository.RootDir}, "dockerfile": {m.Repository.Dockerfile, st.Repository.Dockerfile}} {
			if !pair[0].IsUnknown() && !pair[0].Equal(pair[1]) {
				if pair[0].IsNull() {
					src[k] = nil
				} else {
					src[k] = pair[0].ValueString()
				}
			}
		}
		if !m.Repository.AutoDeploy.Equal(st.Repository.AutoDeploy) && !m.Repository.AutoDeploy.IsUnknown() {
			src["auto_deploy"] = m.Repository.AutoDeploy.ValueBool()
		}
	}
	if m.Repository == nil && !m.RegistryCredentialID.Equal(st.RegistryCredentialID) {
		if m.RegistryCredentialID.IsNull() {
			src["registry_credential"] = nil
		} else {
			src["registry_credential"] = m.RegistryCredentialID.ValueString()
		}
	}
	if len(src) > 0 {
		if _, err := r.c.Do(ctx, client.OpUpdateServiceSource, p, src, nil); err != nil {
			fail(&resp.Diagnostics, "Changing the service's source failed", err)
			return
		}
	}
	if m.Repository == nil && !m.Image.Equal(st.Image) {
		var out struct {
			Deployment *deployment `json:"deployment"`
		}
		appr, err := r.c.Do(ctx, client.OpDeploy, p, map[string]any{"image": m.Image.ValueString()}, &out)
		if err != nil {
			fail(&resp.Diagnostics, "Deploying the new image failed", err)
			return
		}
		id := ""
		if out.Deployment != nil {
			id = out.Deployment.ID
		}
		if appr != nil {
			id = appr.DeploymentID
		}
		if m.WaitForDeployment.ValueBool() {
			if _, err := waitDeployment(ctx, r.c, id); err != nil {
				fail(&resp.Diagnostics, "The deployment did not succeed", err)
				return
			}
		}
	}
	// ---------- WP2.14: the deploy rule; removed from the configuration, the rule goes back to branch + auto-deploy ----------
	want, had := ruleOf(m.Repository), ruleOf(st.Repository)
	if m.Repository != nil && !sameRule(want, had) {
		next := want
		if next == nil {
			next = shorthandRule(m.Repository)
		}
		if err := putDeployRule(ctx, r.c, m.Environment.ValueString(), m.Name.ValueString(), next); err != nil {
			fail(&resp.Diagnostics, "Changing the deploy rule failed", err)
			return
		}
	}
	s, err := r.get(ctx, m.Environment.ValueString(), m.Name.ValueString())
	if err != nil {
		fail(&resp.Diagnostics, "Reading the service failed", err)
		return
	}
	r.set(&m, s)
	r.refreshRule(ctx, &m, want, false, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := client.Params{"env": m.Environment.ValueString(), "name": m.Name.ValueString()}
	var out struct {
		Jobs []client.JobRef `json:"jobs"`
	}
	appr, err := r.c.Do(ctx, client.OpDeleteService, p, map[string]any{"confirm": m.Name.ValueString()}, &out)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Deleting the service failed", err)
		return
	}
	ids := jobIDs(out.Jobs)
	if appr != nil {
		ids = appr.JobIDs()
	}
	if err := r.c.WaitJobs(ctx, ids); err != nil {
		fail(&resp.Diagnostics, "Deleting the service failed on its server", err)
		return
	}
	if _, err := r.get(ctx, m.Environment.ValueString(), m.Name.ValueString()); err == nil {
		resp.Diagnostics.AddError("The service still exists", "Every delete job succeeded but the service is still listed; run destroy again.")
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<environment>/<name>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func jobIDs(jobs []client.JobRef) []string {
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.JobID)
	}
	return ids
}
