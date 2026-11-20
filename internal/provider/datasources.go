package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// ---------------------------------------------------------------- unrent_servers

type serversDataSource struct{ dsBase }

func newServersDataSource() datasource.DataSource { return &serversDataSource{} }

type serverModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Mode      types.String `tfsdk:"mode"`
	Version   types.String `tfsdk:"version"`
	OS        types.String `tfsdk:"os"`
	Arch      types.String `tfsdk:"arch"`
	Connected types.Bool   `tfsdk:"connected"`
}

type serversModel struct {
	Name    types.String  `tfsdk:"name"`
	Servers []serverModel `tfsdk:"servers"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Servers enrolled in the organisation (enrolling runs the installer on the server, not Terraform). Filter by hostname with `name`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Optional: true, MarkdownDescription: "Only the server with this hostname."},
			"servers": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true}, "mode": schema.StringAttribute{Computed: true},
				"version": schema.StringAttribute{Computed: true}, "os": schema.StringAttribute{Computed: true}, "arch": schema.StringAttribute{Computed: true},
				"connected": schema.BoolAttribute{Computed: true},
			}}},
		},
	}
}

func (d *serversDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serversModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Servers []struct {
			ID        string  `json:"id"`
			Name      *string `json:"name"`
			Mode      string  `json:"mode"`
			Version   *string `json:"version"`
			OS        *string `json:"os"`
			Arch      *string `json:"arch"`
			Connected bool    `json:"connected"`
		} `json:"servers"`
	}
	if err := d.c.Get(ctx, client.OpListServers, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Listing servers failed", err)
		return
	}
	m.Servers = []serverModel{}
	for _, s := range out.Servers {
		if n := str(m.Name); n != "" && (s.Name == nil || *s.Name != n) {
			continue
		}
		m.Servers = append(m.Servers, serverModel{ID: types.StringValue(s.ID), Name: nullStr(s.Name), Mode: types.StringValue(s.Mode), Version: nullStr(s.Version), OS: nullStr(s.OS), Arch: nullStr(s.Arch), Connected: types.BoolValue(s.Connected)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------- unrent_releases

type releasesDataSource struct{ dsBase }

func newReleasesDataSource() datasource.DataSource { return &releasesDataSource{} }

type releaseModel struct {
	Digest        types.String `tfsdk:"digest"`
	Release       types.String `tfsdk:"release"`
	ConfigVersion types.Int64  `tfsdk:"config_version"`
	DeployedAt    types.Int64  `tfsdk:"deployed_at"`
	Live          types.Bool   `tfsdk:"live"`
}

type releasesModel struct {
	Environment types.String   `tfsdk:"environment"`
	Service     types.String   `tfsdk:"service"`
	LiveDigest  types.String   `tfsdk:"live_digest"`
	Releases    []releaseModel `tfsdk:"releases"`
}

func (d *releasesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_releases"
}

func (d *releasesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A service's recent releases (newest first), e.g. to pin another environment to the digest that runs in staging.",
		Attributes: map[string]schema.Attribute{
			"environment": schema.StringAttribute{Required: true, Validators: []validator.String{envValidator()}},
			"service":     schema.StringAttribute{Required: true, Validators: []validator.String{nameValidator()}},
			"live_digest": schema.StringAttribute{Computed: true},
			"releases": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"digest": schema.StringAttribute{Computed: true}, "release": schema.StringAttribute{Computed: true}, "config_version": schema.Int64Attribute{Computed: true},
				"deployed_at": schema.Int64Attribute{Computed: true, MarkdownDescription: "ms since the epoch"}, "live": schema.BoolAttribute{Computed: true},
			}}},
		},
	}
}

func (d *releasesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m releasesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Live *struct {
			Digest string `json:"digest"`
		} `json:"live"`
		Releases []struct {
			Digest        string  `json:"digest"`
			Release       *string `json:"release"`
			ConfigVersion *int64  `json:"config_version"`
			DeployedAt    int64   `json:"deployed_at"`
			Live          bool    `json:"live"`
		} `json:"releases"`
	}
	if err := d.c.Get(ctx, client.OpGetService, client.Params{"env": m.Environment.ValueString(), "name": m.Service.ValueString()}, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Reading the service failed", err)
		return
	}
	m.LiveDigest = types.StringNull()
	if out.Live != nil {
		m.LiveDigest = types.StringValue(out.Live.Digest)
	}
	m.Releases = []releaseModel{}
	for _, r := range out.Releases {
		cv := types.Int64Null()
		if r.ConfigVersion != nil {
			cv = types.Int64Value(*r.ConfigVersion)
		}
		m.Releases = append(m.Releases, releaseModel{Digest: types.StringValue(r.Digest), Release: nullStr(r.Release), ConfigVersion: cv, DeployedAt: types.Int64Value(r.DeployedAt), Live: types.BoolValue(r.Live)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------- unrent_project

type projectDataSource struct{ dsBase }

func newProjectDataSource() datasource.DataSource { return &projectDataSource{} }

type projectDSModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Environments types.List   `tfsdk:"environments"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project by id or by name (`Default` is the organisation's default project).",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Optional: true, Computed: true},
			"name":         schema.StringAttribute{Optional: true, Computed: true},
			"environments": schema.ListAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *projectDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m projectDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Projects []projectAPI `json:"projects"`
	}
	if err := d.c.Get(ctx, client.OpListProjects, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Listing projects failed", err)
		return
	}
	for _, p := range out.Projects {
		if p.Archived || (str(m.ID) != "" && p.ID != str(m.ID)) || (str(m.Name) != "" && p.Name != str(m.Name)) {
			continue
		}
		m.ID, m.Name = types.StringValue(p.ID), types.StringValue(p.Name)
		m.Environments, _ = types.ListValueFrom(ctx, types.StringType, p.Environments)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	resp.Diagnostics.AddError("No such project", "No active project matches "+firstSet(str(m.ID), str(m.Name))+".")
}

// ---------------------------------------------------------------- unrent_members

type membersDataSource struct{ dsBase }

func newMembersDataSource() datasource.DataSource { return &membersDataSource{} }

type memberModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Email    types.String `tfsdk:"email"`
	Role     types.String `tfsdk:"role"`
	Approver types.Bool   `tfsdk:"approver"`
}

type membersModel struct {
	Members []memberModel `tfsdk:"members"`
}

func (d *membersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_members"
}

func (d *membersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Members of the organisation (for `unrent_project_role.user_id`). People join by signing in with a passkey; that is not a Terraform step.",
		Attributes: map[string]schema.Attribute{
			"members": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true}, "email": schema.StringAttribute{Computed: true},
				"role": schema.StringAttribute{Computed: true}, "approver": schema.BoolAttribute{Computed: true},
			}}},
		},
	}
}

func (d *membersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var out struct {
		Members []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Approver bool   `json:"approver"`
		} `json:"members"`
	}
	if err := d.c.Get(ctx, client.OpListMembers, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Listing members failed", err)
		return
	}
	m := membersModel{Members: []memberModel{}}
	for _, x := range out.Members {
		m.Members = append(m.Members, memberModel{ID: types.StringValue(x.ID), Name: types.StringValue(x.Name), Email: types.StringValue(x.Email), Role: types.StringValue(x.Role), Approver: types.BoolValue(x.Approver)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
