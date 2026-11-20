package provider

import (
	"context"

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

// ---------------------------------------------------------------- unrent_project

type projectResource struct{ base }

func newProjectResource() resource.Resource { return &projectResource{} }

type projectModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Environments types.List   `tfsdk:"environments"`
}

type projectAPI struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Default      bool     `json:"default"`
	Archived     bool     `json:"archived"`
	Environments []string `json:"environments"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project groups environments and carries per-project roles. Creating and archiving need an org admin. `terraform destroy` **archives** the project (Unrent keeps it for its audit trail); restore it in the console or import it again.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "`prj_…`", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":         schema.StringAttribute{Required: true, MarkdownDescription: "1–64 characters, unique in the organisation.", Validators: []validator.String{stringvalidator.LengthBetween(1, 64)}},
			"environments": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Environments in the project (manage them with `unrent_environment.project_id`)."},
		},
	}
}

func (r *projectResource) set(ctx context.Context, m *projectModel, p *projectAPI) {
	m.ID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	envs, _ := types.ListValueFrom(ctx, types.StringType, p.Environments)
	m.Environments = envs
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectAPI
	if _, err := r.c.Do(ctx, client.OpCreateProject, nil, map[string]any{"name": m.Name.ValueString()}, &p); err != nil {
		fail(&resp.Diagnostics, "Creating the project failed", err)
		return
	}
	r.set(ctx, &m, &p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectAPI
	err := r.c.Get(ctx, client.OpGetProject, client.Params{"project": m.ID.ValueString()}, nil, &p)
	if client.IsNotFound(err) || (err == nil && p.Archived) {
		resp.State.RemoveResource(ctx) // archived in the console counts as gone
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the project failed", err)
		return
	}
	r.set(ctx, &m, &p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectAPI
	if _, err := r.c.Do(ctx, client.OpUpdateProject, client.Params{"project": st.ID.ValueString()}, map[string]any{"name": m.Name.ValueString()}, &p); err != nil {
		fail(&resp.Diagnostics, "Renaming the project failed", err)
		return
	}
	r.set(ctx, &m, &p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.c.Do(ctx, client.OpUpdateProject, client.Params{"project": m.ID.ValueString()}, map[string]any{"archived": true}, nil)
	if err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Archiving the project failed", err)
		return
	}
	resp.Diagnostics.AddWarning("Project archived", "Unrent archives projects instead of deleting them: "+m.ID.ValueString()+" can be restored in the console.")
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !projectRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected a project id (prj_…)")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ---------------------------------------------------------------- unrent_project_role

type projectRoleResource struct{ base }

func newProjectRoleResource() resource.Resource { return &projectRoleResource{} }

type projectRoleModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	UserID    types.String `tfsdk:"user_id"`
	Role      types.String `tfsdk:"role"`
}

func (r *projectRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_role"
}

func (r *projectRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member's role in one project (raises or lowers their org role there). Only for members whose org role is developer or viewer; owners and admins keep theirs everywhere. Needs admin in the project.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "`<project_id>/<user_id>`", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{projectValidator()}},
			"user_id":    schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(userRe, "a member id (usr_…, see the unrent_members data source)")}},
			"role":       schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("admin", "developer", "viewer")}},
		},
	}
}

func (r *projectRoleResource) put(ctx context.Context, m *projectRoleModel) error {
	_, err := r.c.Do(ctx, client.OpSetProjectRole, client.Params{"project": m.ProjectID.ValueString(), "user": m.UserID.ValueString()}, map[string]any{"role": m.Role.ValueString()}, nil)
	m.ID = types.StringValue(m.ProjectID.ValueString() + "/" + m.UserID.ValueString())
	return err
}

func (r *projectRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m projectRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Setting the project role failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m projectRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Changing the project role failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m projectRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Members []struct {
			ID       string  `json:"id"`
			Override *string `json:"override"`
		} `json:"members"`
	}
	err := r.c.Get(ctx, client.OpListProjectMembers, client.Params{"project": m.ProjectID.ValueString()}, nil, &out)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading project roles failed", err)
		return
	}
	for _, x := range out.Members {
		if x.ID == m.UserID.ValueString() && x.Override != nil {
			m.Role = types.StringValue(*x.Override)
			m.ID = types.StringValue(m.ProjectID.ValueString() + "/" + x.ID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *projectRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m projectRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.c.Do(ctx, client.OpRemoveProjectRole, client.Params{"project": m.ProjectID.ValueString(), "user": m.UserID.ValueString()}, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Removing the project role failed", err)
	}
}

func (r *projectRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<project_id>/<user_id>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
