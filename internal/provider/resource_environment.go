package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type environmentResource struct{ base }

func newEnvironmentResource() resource.Resource { return &environmentResource{} }

type environmentModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	ProjectID types.String `tfsdk:"project_id"`
	Approvals types.Int64  `tfsdk:"approvals"`
	Kind      types.String `tfsdk:"kind"`
	Network   types.String `tfsdk:"network"`
	// WP2.14: the environment model v2 (WP2.12)
	Tier        types.String `tfsdk:"tier"`
	Colour      types.String `tfsdk:"colour"`
	Description types.String `tfsdk:"description"`
	CloneFrom   types.String `tfsdk:"clone_from"`
	TemplateID  types.String `tfsdk:"template_id"`
	Protected   types.Bool   `tfsdk:"protected"`
	// environments-v3 §2.2: a display label; the name stays the id on every server.
	Label types.String `tfsdk:"label"`
}

type environmentAPI struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Approvals int64  `json:"approvals"`
	Project   string `json:"project"`
	Network   string `json:"network"`
	// WP2.14
	Tier        string  `json:"tier"`
	Colour      string  `json:"colour"`
	Description *string `json:"description"`
	ClonedFrom  *string `json:"cloned_from"`
	Protected   bool    `json:"protected"`
	Label       *string `json:"label"`
	PendingTier *struct {
		Tier string `json:"tier"`
	} `json:"pending_tier"`
	ApproveURL string `json:"approve_url"`
}

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An environment (its own Docker network on every server, its own variables and approval floor) in a project, with any name. Its `tier` decides its defaults (a `production` tier starts with an approval floor of 1 and waits for an admin's passkey in the console). Environments that already exist (an organisation's first ones, made at setup) are imported by name (`unrent_environments` lists them). Destroying deletes the environment; one that still holds services, databases or domains is refused with their names.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{envValidator()}},
			"project_id": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("prj_default"),
				MarkdownDescription: "The project (default: the organisation's default project). Moving needs admin in both projects.",
				Validators:          []validator.String{projectValidator()},
			},
			"approvals": schema.Int64Attribute{
				Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				MarkdownDescription: "Approver passkeys a deploy, config apply or rollback in this environment needs (0–2; default: the tier's, 1 for `production`, else 0). Servers enforce it too: a change is a policy job approved in the console.",
				Validators:          []validator.Int64{int64validator.Between(0, 2)},
			},
			"kind":    schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"network": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			// ---------- WP2.14 (docs/environments-v2.md §3, §7) ----------
			"tier": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "`production`, `staging` or `development` (default `development`; the name never decides it). Creating with, or changing to or from, `production` waits for an admin's passkey in the console (the provider prints the page; apply again once it is confirmed).",
				Validators:          []validator.String{stringvalidator.OneOf("production", "staging", "development")},
			},
			"colour":      schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "The switcher's colour (default: the tier's)."},
			"description": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(300)}},
			"clone_from": schema.StringAttribute{
				Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{envValidator()},
				MarkdownDescription: "Create as a clone of this environment (create-time): services, rules as manual, plain variables and secret names; databases empty, crons paused; never domains or protection. Needs developer in its project.",
			},
			"template_id": schema.StringAttribute{
				Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "Create from this template of the project (`etp_…`, `unrent_environment_template`), create-time.",
				Validators:          []validator.String{stringvalidator.RegexMatches(regexpMust(`^etp_[0-9a-f]{20}$`), "a template id (etp_…)"), stringvalidator.ConflictsWith(path.MatchRoot("clone_from"))},
			},
			"protected": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether `unrent_environment_protection` (or the console) protects it."},
			"label": schema.StringAttribute{
				Optional: true, MarkdownDescription: "A display label (`Live`, `EU production`), up to 40 characters. The name stays the environment's id on every server.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 40)},
			},
		},
	}
}

func (r *environmentResource) find(ctx context.Context, name string) (*environmentAPI, error) {
	var out struct {
		Environments []environmentAPI `json:"environments"`
	}
	if err := r.c.Get(ctx, client.OpListEnvironments, nil, nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Environments {
		if out.Environments[i].Name == name {
			return &out.Environments[i], nil
		}
	}
	return nil, nil
}

func (r *environmentResource) set(m *environmentModel, e *environmentAPI) {
	m.ID = types.StringValue(e.Name)
	m.Name = types.StringValue(e.Name)
	m.ProjectID = types.StringValue(e.Project)
	m.Approvals = types.Int64Value(e.Approvals)
	m.Kind = types.StringValue(e.Kind)
	m.Network = types.StringValue(e.Network)
	// WP2.14
	if e.Tier != "" {
		m.Tier = types.StringValue(e.Tier)
	}
	if e.Colour != "" {
		m.Colour = types.StringValue(e.Colour)
	}
	if e.Description != nil || !m.Description.IsNull() {
		m.Description = nullStr(e.Description)
	}
	if e.Label != nil || !m.Label.IsNull() {
		m.Label = nullStr(e.Label)
	}
	m.Protected = types.BoolValue(e.Protected)
}

// meta is the tier, colour and description the plan asks for and the API does not have yet (WP2.14).
func meta(m *environmentModel, e *environmentAPI) map[string]any {
	b := map[string]any{}
	if !m.Tier.IsNull() && !m.Tier.IsUnknown() && m.Tier.ValueString() != e.Tier {
		b["tier"] = m.Tier.ValueString()
	}
	if !m.Colour.IsNull() && !m.Colour.IsUnknown() && m.Colour.ValueString() != e.Colour {
		b["colour"] = m.Colour.ValueString()
	}
	for k, v := range map[string]struct {
		want types.String
		have *string
	}{"description": {m.Description, e.Description}, "label": {m.Label, e.Label}} {
		if v.want.IsUnknown() {
			continue
		}
		want := v.want.ValueStringPointer()
		if (want == nil) != (v.have == nil) || (want != nil && *want != *v.have) {
			if want == nil {
				b[k] = nil
			} else {
				b[k] = *want
			}
		}
	}
	return b
}

// createBody is POST /environments for the plan: any name (environments-v3: none is reserved), the floor only when
// configured (else the tier's default), a clone or a template, the tier, colour, description and label.
func createBody(m *environmentModel) map[string]any {
	body := map[string]any{"name": m.Name.ValueString(), "project": m.ProjectID.ValueString()}
	if !m.Approvals.IsNull() && !m.Approvals.IsUnknown() {
		body["approvals"] = m.Approvals.ValueInt64()
	}
	for k, v := range map[string]types.String{"from": m.CloneFrom, "template": m.TemplateID, "tier": m.Tier, "colour": m.Colour, "description": m.Description, "label": m.Label} {
		if !v.IsNull() && !v.IsUnknown() {
			body[k] = v.ValueString()
		}
	}
	return body
}

// pendingTier: production ⇄ another tier waits for an admin's passkey in the console.
func pendingTier(diags *diag.Diagnostics, e *environmentAPI) {
	if e.PendingTier != nil {
		diags.AddWarning("The tier change waits for a passkey", fmt.Sprintf("%s becomes %s once an admin confirms it with a passkey at %s; run apply again afterwards.", e.Name, e.PendingTier.Tier, e.ApproveURL))
	}
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var e environmentAPI
	if _, err := r.c.Do(ctx, client.OpCreateEnvironment, nil, createBody(&m), &e); err != nil {
		fail(&resp.Diagnostics, "Creating the environment failed", err)
		return
	}
	// A production tier waits for an admin's passkey (202): the state keeps what runs, so the next plan asks again.
	pendingTier(&resp.Diagnostics, &e)
	r.set(&m, &e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.find(ctx, m.Name.ValueString())
	if err != nil {
		fail(&resp.Diagnostics, "Reading environments failed", err)
		return
	}
	if e == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	r.set(&m, e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var e environmentAPI
	body := map[string]any{"project": m.ProjectID.ValueString()}
	if !m.Approvals.IsNull() && !m.Approvals.IsUnknown() {
		body["approvals"] = m.Approvals.ValueInt64()
	}
	cur, err := r.find(ctx, m.Name.ValueString())
	if err != nil || cur == nil {
		fail(&resp.Diagnostics, "Reading the environment failed", errorf("%v", err))
		return
	}
	for k, v := range meta(&m, cur) { // WP2.14
		body[k] = v
	}
	if _, err := r.c.Do(ctx, client.OpUpdateEnvironment, client.Params{"env": m.Name.ValueString()}, body, &e); err != nil {
		fail(&resp.Diagnostics, "Changing the environment failed", err)
		return
	}
	pendingTier(&resp.Diagnostics, &e)
	if e.PendingTier != nil {
		e.Tier = cur.Tier // not yet: the state keeps what runs, so the next plan asks again
	}
	r.set(&m, &e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// environments-v3 §2.3: any environment is deleted when empty (the API refuses one that is not, with the list).
	_, err := r.c.Do(ctx, client.OpDeleteEnvironment, client.Params{"env": m.Name.ValueString()}, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Deleting the environment failed", err)
	}
}

func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !envRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected an environment name")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
