package provider

// WP2.14 (docs/terraform.md §3, docs/branch-environments.md): unrent_branch_environments — a project's
// branch-environment rules (every matching branch gets an ephemeral environment from a source environment or a
// template, with a time to live). The API sets a project's rules as a whole, so this resource owns all of them:
// destroying it removes them (their branch environments are torn down). Two rules that match one branch are
// refused by the API, naming the branch.

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type branchEnvsResource struct{ base }

func newBranchEnvsResource() resource.Resource { return &branchEnvsResource{} }

type branchRuleModel struct {
	ID           types.String `tfsdk:"id"`
	Repo         types.String `tfsdk:"repo"`
	Include      types.List   `tfsdk:"include"`
	Exclude      types.List   `tfsdk:"exclude"`
	From         types.String `tfsdk:"from"`
	ServerID     types.String `tfsdk:"server_id"`
	TTLHours     types.Int64  `tfsdk:"ttl_hours"`
	ResetOnPush  types.Bool   `tfsdk:"reset_on_push"`
	MaxOpen      types.Int64  `tfsdk:"max_open"`
	AllowSecrets types.List   `tfsdk:"allow_secrets"`
}

type branchEnvsModel struct {
	ID        types.String      `tfsdk:"id"`
	ProjectID types.String      `tfsdk:"project_id"`
	Rule      []branchRuleModel `tfsdk:"rule"`
}

type branchRuleAPI struct {
	ID           string   `json:"id"`
	Repo         string   `json:"repo"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
	From         string   `json:"from"`
	Server       string   `json:"server"`
	TTLHours     int64    `json:"ttl_hours"`
	ResetOnPush  bool     `json:"reset_on_push"`
	MaxOpen      int64    `json:"max_open"`
	AllowSecrets []string `json:"allow_secrets"`
}

func (r *branchEnvsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_branch_environments"
}

func (r *branchEnvsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	globs := []validator.List{listvalidator.SizeBetween(1, 10), listvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexpMust(`^[A-Za-z0-9._/*?-]{1,100}$`), "a glob"))}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project's branch-environment rules (all of them): each branch a rule matches gets an ephemeral environment `b-<slug>-<hash>`, created from `from` (an environment of the project or a template), on `server_id`, torn down after `ttl_hours` without a push or when the branch is deleted. Destroying this resource removes the rules and tears their environments down.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{projectValidator()}},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Validators: []validator.List{listvalidator.SizeAtMost(10)},
				NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
					"id":            schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"repo":          schema.StringAttribute{Required: true, MarkdownDescription: "`github:<repository id>` of a repository the project's services are built from.", Validators: []validator.String{stringvalidator.RegexMatches(regexpMust(`^github:[0-9]{1,20}$`), "github:<repository id>")}},
					"include":       schema.ListAttribute{Required: true, ElementType: types.StringType, Validators: globs},
					"exclude":       schema.ListAttribute{Optional: true, ElementType: types.StringType},
					"from":          schema.StringAttribute{Required: true, MarkdownDescription: "An environment of the project, or a template id (`etp_…`)."},
					"server_id":     schema.StringAttribute{Required: true, Validators: []validator.String{agentValidator()}},
					"ttl_hours":     schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(72), Validators: []validator.Int64{int64validator.Between(1, 720)}},
					"reset_on_push": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"max_open":      schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(5), Validators: []validator.Int64{int64validator.Between(0, 20)}},
					"allow_secrets": schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Secret names a branch environment may receive (re-sealed with a passkey, never from a production-tier source)."},
				}},
			},
		},
	}
}

func (r *branchEnvsResource) get(ctx context.Context, project string) ([]branchRuleAPI, error) {
	var out struct {
		Rules []branchRuleAPI `json:"rules"`
	}
	if err := r.c.Get(ctx, client.OpGetBranchEnvironments, client.Params{"project": project}, nil, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

func (r *branchEnvsResource) put(ctx context.Context, project string, rules []map[string]any) ([]branchRuleAPI, error) {
	var out struct {
		Rules []branchRuleAPI `json:"rules"`
	}
	if _, err := r.c.Do(ctx, client.OpSetBranchEnvironments, client.Params{"project": project}, map[string]any{"rules": rules}, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

func branchRuleBody(ctx context.Context, m branchRuleModel) map[string]any {
	b := map[string]any{"repo": m.Repo.ValueString(), "include": strList(ctx, m.Include), "exclude": strList(ctx, m.Exclude), "from": m.From.ValueString(), "server": m.ServerID.ValueString(),
		"ttl_hours": m.TTLHours.ValueInt64(), "reset_on_push": m.ResetOnPush.ValueBool(), "max_open": m.MaxOpen.ValueInt64(), "allow_secrets": strList(ctx, m.AllowSecrets)}
	if id := str(m.ID); id != "" {
		b["id"] = id
	}
	return b
}

func setRules(m *branchEnvsModel, project string, rules []branchRuleAPI) {
	m.ID = types.StringValue(project)
	m.ProjectID = types.StringValue(project)
	prior := m.Rule
	out := make([]branchRuleModel, 0, len(rules))
	for i, a := range rules {
		var p branchRuleModel
		if i < len(prior) {
			p = prior[i]
		} else {
			p = branchRuleModel{Exclude: types.ListNull(types.StringType), AllowSecrets: types.ListNull(types.StringType)}
		}
		out = append(out, branchRuleModel{
			ID: types.StringValue(a.ID), Repo: types.StringValue(a.Repo), Include: listOrNull(a.Include, types.ListNull(types.StringType)), Exclude: listOrNull(a.Exclude, p.Exclude),
			From: types.StringValue(a.From), ServerID: types.StringValue(a.Server), TTLHours: types.Int64Value(a.TTLHours), ResetOnPush: types.BoolValue(a.ResetOnPush),
			MaxOpen: types.Int64Value(a.MaxOpen), AllowSecrets: listOrNull(a.AllowSecrets, p.AllowSecrets),
		})
	}
	m.Rule = out
}

func (r *branchEnvsResource) apply(ctx context.Context, m *branchEnvsModel) error {
	rules := make([]map[string]any, 0, len(m.Rule))
	for _, x := range m.Rule {
		rules = append(rules, branchRuleBody(ctx, x))
	}
	got, err := r.put(ctx, m.ProjectID.ValueString(), rules)
	if err != nil {
		return err
	}
	setRules(m, m.ProjectID.ValueString(), got)
	return nil
}

func (r *branchEnvsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m branchEnvsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i := range m.Rule {
		m.Rule[i].ID = types.StringNull() // new rules get their ids from the API
	}
	if err := r.apply(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Saving the branch-environment rules failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *branchEnvsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m branchEnvsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rules, err := r.get(ctx, m.ProjectID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the branch-environment rules failed", err)
		return
	}
	setRules(&m, m.ProjectID.ValueString(), rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *branchEnvsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st branchEnvsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A rule keeps its id (and its open environments) when it stays at its position; a new one gets a new id.
	for i := range m.Rule {
		m.Rule[i].ID = types.StringNull()
		if i < len(st.Rule) {
			m.Rule[i].ID = st.Rule[i].ID
		}
	}
	if err := r.apply(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Saving the branch-environment rules failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *branchEnvsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m branchEnvsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.put(ctx, m.ProjectID.ValueString(), []map[string]any{}); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Removing the branch-environment rules failed", err)
	}
}

func (r *branchEnvsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !projectRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected a project id (prj_…)")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
