package provider

// WP2.14 (docs/terraform.md §3, docs/environments-v2.md §3.2): unrent_environment_template — a definition-only
// snapshot of an environment of a project (services, databases, rules, variable names and plain values, placement
// hints; never a ciphertext), saved once. Environments are made from it with unrent_environment.template_id, and
// branch-environment rules may name it. A template is a snapshot: changing its source or name makes a new one.

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type templateResource struct{ base }

func newEnvTemplateResource() resource.Resource { return &templateResource{} }

type templateModel struct {
	ID              types.String `tfsdk:"id"`
	ProjectID       types.String `tfsdk:"project_id"`
	Name            types.String `tfsdk:"name"`
	FromEnvironment types.String `tfsdk:"from_environment"`
	Spec            types.String `tfsdk:"spec"`
}

type templateAPI struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	FromEnv *string         `json:"from_env"`
	Spec    json.RawMessage `json:"spec"`
}

func (r *templateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment_template"
}

func (r *templateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A definition-only template saved from an environment of a project: services, databases, rules, variable names and plain values, placement hints (never a secret value). A snapshot: changing `from_environment` or `name` saves a new one.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Computed: true, MarkdownDescription: "`etp_…`", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id":       schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{projectValidator()}},
			"name":             schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.LengthBetween(1, 64)}},
			"from_environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}, MarkdownDescription: "An environment of the project to snapshot."},
			"spec":             schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "The snapshot as JSON."},
		},
	}
}

func (r *templateResource) set(m *templateModel, project string, t *templateAPI) {
	m.ID = types.StringValue(t.ID)
	m.ProjectID = types.StringValue(project)
	m.Name = types.StringValue(t.Name)
	if t.FromEnv != nil {
		m.FromEnvironment = types.StringValue(*t.FromEnv)
	}
	if len(t.Spec) > 0 && string(t.Spec) != "null" {
		m.Spec = types.StringValue(string(t.Spec))
	} else if m.Spec.IsUnknown() {
		m.Spec = types.StringNull()
	}
}

func (r *templateResource) read(ctx context.Context, project, id string) (*templateAPI, error) {
	var out struct {
		Template templateAPI `json:"template"`
	}
	if err := r.c.Get(ctx, client.OpGetEnvironmentTemplate, client.Params{"project": project, "etp": id}, nil, &out); err != nil {
		return nil, err
	}
	return &out.Template, nil
}

func (r *templateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m templateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Template templateAPI `json:"template"`
	}
	project := m.ProjectID.ValueString()
	if _, err := r.c.Do(ctx, client.OpCreateEnvironmentTemplate, client.Params{"project": project}, map[string]any{"name": m.Name.ValueString(), "from": m.FromEnvironment.ValueString()}, &out); err != nil {
		fail(&resp.Diagnostics, "Saving the template failed", err)
		return
	}
	t, err := r.read(ctx, project, out.Template.ID)
	if err != nil {
		t = &out.Template
	}
	r.set(&m, project, t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *templateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m templateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.read(ctx, m.ProjectID.ValueString(), m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the template failed", err)
		return
	}
	r.set(&m, m.ProjectID.ValueString(), t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: every attribute but the computed ones replaces the template; nothing to change in place.
func (r *templateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m templateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *templateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m templateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.c.Do(ctx, client.OpDeleteEnvironmentTemplate, client.Params{"project": m.ProjectID.ValueString(), "etp": m.ID.ValueString()}, nil, nil); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Deleting the template failed", err)
	}
}

// ImportState: `<project_id>/<etp_…>`.
func (r *templateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<project_id>/<template id>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), p[1])...)
}
