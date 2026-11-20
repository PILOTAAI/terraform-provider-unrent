package provider

// environments-v3 §2.5: unrent_environments lists the organisation's environments (any name; the tier decides what
// each is for), so the ones made at organisation setup can be read, or imported into unrent_environment by name.

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type environmentsDataSource struct{ dsBase }

func newEnvironmentsDataSource() datasource.DataSource { return &environmentsDataSource{} }

type environmentDSModel struct {
	Name      types.String `tfsdk:"name"`
	Label     types.String `tfsdk:"label"`
	Tier      types.String `tfsdk:"tier"`
	ProjectID types.String `tfsdk:"project_id"`
	Approvals types.Int64  `tfsdk:"approvals"`
	Protected types.Bool   `tfsdk:"protected"`
	Network   types.String `tfsdk:"network"`
}

type environmentsDSModel struct {
	Tier         types.String         `tfsdk:"tier"`
	Environments []environmentDSModel `tfsdk:"environments"`
}

func (d *environmentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environments"
}

func (d *environmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organisation's environments, production-tier first. Filter with `tier` (e.g. the production-tier environment, whatever its name). Import one into `unrent_environment` by its name.",
		Attributes: map[string]schema.Attribute{
			"tier": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only environments of this tier: `production`, `staging`, `development`, `preview` or `ephemeral`.",
				Validators: []validator.String{stringvalidator.OneOf("production", "staging", "development", "preview", "ephemeral")},
			},
			"environments": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{Computed: true}, "label": schema.StringAttribute{Computed: true}, "tier": schema.StringAttribute{Computed: true},
				"project_id": schema.StringAttribute{Computed: true}, "approvals": schema.Int64Attribute{Computed: true},
				"protected": schema.BoolAttribute{Computed: true}, "network": schema.StringAttribute{Computed: true},
			}}},
		},
	}
}

// environmentRows keeps the environments of tier (all when empty), in the API's order.
func environmentRows(all []environmentAPI, tier string) []environmentDSModel {
	out := []environmentDSModel{}
	for _, e := range all {
		if tier != "" && e.Tier != tier {
			continue
		}
		out = append(out, environmentDSModel{
			Name: types.StringValue(e.Name), Label: nullStr(e.Label), Tier: types.StringValue(e.Tier), ProjectID: types.StringValue(e.Project),
			Approvals: types.Int64Value(e.Approvals), Protected: types.BoolValue(e.Protected), Network: types.StringValue(e.Network),
		})
	}
	return out
}

func (d *environmentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m environmentsDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Environments []environmentAPI `json:"environments"`
	}
	if err := d.c.Get(ctx, client.OpListEnvironments, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Listing environments failed", err)
		return
	}
	m.Environments = environmentRows(out.Environments, str(m.Tier))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
