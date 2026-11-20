package provider

// P21 (docs/terraform.md §3, docs/sandboxes.md §4): unrent_sandboxes reads the organisation's open sandboxes (what
// coding agents created for their tasks) and each sandbox server's budget. Sandboxes are short-lived and created over
// MCP, the CLI or the API, so they are data here, never resources.

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type sandboxesDataSource struct{ dsBase }

func newSandboxesDataSource() datasource.DataSource { return &sandboxesDataSource{} }

type sandboxServiceDSModel struct {
	Name types.String `tfsdk:"name"`
	URL  types.String `tfsdk:"url"`
	Live types.Bool   `tfsdk:"live"`
}

type sandboxDSModel struct {
	ID          types.String            `tfsdk:"id"`
	Environment types.String            `tfsdk:"environment"`
	ProjectID   types.String            `tfsdk:"project_id"`
	Task        types.String            `tfsdk:"task"`
	Source      types.String            `tfsdk:"source"`
	ServerID    types.String            `tfsdk:"server_id"`
	State       types.String            `tfsdk:"state"`
	Status      types.String            `tfsdk:"status"`
	Client      types.String            `tfsdk:"client"`
	ExpiresAt   types.String            `tfsdk:"expires_at"`
	Services    []sandboxServiceDSModel `tfsdk:"services"`
}

type sandboxServerDSModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Problem   types.String `tfsdk:"problem"`
	BudgetMB  types.Int64  `tfsdk:"budget_mb"`
	PlannedMB types.Int64  `tfsdk:"planned_mb"`
	FreeMB    types.Int64  `tfsdk:"free_mb"`
	Open      types.Int64  `tfsdk:"open"`
}

type sandboxesDSModel struct {
	ProjectID types.String           `tfsdk:"project_id"`
	Sandboxes []sandboxDSModel       `tfsdk:"sandboxes"`
	Servers   []sandboxServerDSModel `tfsdk:"servers"`
}

type sandboxAPI struct {
	ID        string  `json:"id"`
	Env       string  `json:"env"`
	Project   string  `json:"project"`
	Task      string  `json:"task"`
	Source    string  `json:"source"`
	Server    string  `json:"server"`
	State     string  `json:"state"`
	Status    string  `json:"status"`
	Client    *string `json:"client"`
	ExpiresAt *int64  `json:"expires_at"`
	Services  []struct {
		Name string  `json:"name"`
		URL  *string `json:"url"`
		Live bool    `json:"live"`
	} `json:"services"`
}

type sandboxServerAPI struct {
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	Problem   *string `json:"problem"`
	BudgetMB  *int64  `json:"budget_mb"`
	PlannedMB int64   `json:"planned_mb"`
	FreeMB    *int64  `json:"free_mb"`
	Open      int64   `json:"open"`
}

func (d *sandboxesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandboxes"
}

func (d *sandboxesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organisation's open sandboxes (disposable copies of an app that coding agents created for one task) and each sandbox server's budget: half its memory, what its open sandboxes plan, its free memory, and why it takes no new sandbox. Filter the sandboxes with `project_id`.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only the sandboxes of this project.", Validators: []validator.String{projectValidator()}},
			"sandboxes": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Computed: true}, "environment": schema.StringAttribute{Computed: true}, "project_id": schema.StringAttribute{Computed: true},
				"task": schema.StringAttribute{Computed: true}, "source": schema.StringAttribute{Computed: true}, "server_id": schema.StringAttribute{Computed: true},
				"state":      schema.StringAttribute{Computed: true, MarkdownDescription: "`preparing` (its database is being copied) or `open`."},
				"status":     schema.StringAttribute{Computed: true, MarkdownDescription: "`preparing`, `deploying`, `live` or `failed`."},
				"client":     schema.StringAttribute{Computed: true, MarkdownDescription: "The app or token that created it."},
				"expires_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it is torn down (RFC 3339)."},
				"services": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{Computed: true}, "url": schema.StringAttribute{Computed: true}, "live": schema.BoolAttribute{Computed: true},
				}}},
			}}},
			"servers": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
				"problem":    schema.StringAttribute{Computed: true, MarkdownDescription: "Why it takes no new sandbox (it runs production, or it is gone); null when it does."},
				"budget_mb":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Half its memory; null until it reported its memory."},
				"planned_mb": schema.Int64Attribute{Computed: true}, "free_mb": schema.Int64Attribute{Computed: true}, "open": schema.Int64Attribute{Computed: true},
			}}},
		},
	}
}

func nullInt(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

// sandboxRows keeps the sandboxes of project (all when empty), in the API's order (newest first).
func sandboxRows(all []sandboxAPI, project string) []sandboxDSModel {
	out := []sandboxDSModel{}
	for _, s := range all {
		if project != "" && s.Project != project {
			continue
		}
		exp := types.StringNull()
		if s.ExpiresAt != nil {
			exp = types.StringValue(time.UnixMilli(*s.ExpiresAt).UTC().Format(time.RFC3339))
		}
		svcs := []sandboxServiceDSModel{}
		for _, v := range s.Services {
			svcs = append(svcs, sandboxServiceDSModel{Name: types.StringValue(v.Name), URL: nullStr(v.URL), Live: types.BoolValue(v.Live)})
		}
		out = append(out, sandboxDSModel{
			ID: types.StringValue(s.ID), Environment: types.StringValue(s.Env), ProjectID: types.StringValue(s.Project), Task: types.StringValue(s.Task), Source: types.StringValue(s.Source),
			ServerID: types.StringValue(s.Server), State: types.StringValue(s.State), Status: types.StringValue(s.Status), Client: nullStr(s.Client), ExpiresAt: exp, Services: svcs,
		})
	}
	return out
}

func sandboxServerRows(all []sandboxServerAPI) []sandboxServerDSModel {
	out := []sandboxServerDSModel{}
	for _, v := range all {
		out = append(out, sandboxServerDSModel{
			ID: types.StringValue(v.ID), Name: nullStr(v.Name), Problem: nullStr(v.Problem), BudgetMB: nullInt(v.BudgetMB), PlannedMB: types.Int64Value(v.PlannedMB), FreeMB: nullInt(v.FreeMB), Open: types.Int64Value(v.Open),
		})
	}
	return out
}

func (d *sandboxesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m sandboxesDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Sandboxes []sandboxAPI       `json:"sandboxes"`
		Servers   []sandboxServerAPI `json:"servers"`
	}
	if err := d.c.Get(ctx, client.OpListSandboxes, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Listing sandboxes failed", err)
		return
	}
	m.Sandboxes = sandboxRows(out.Sandboxes, str(m.ProjectID))
	m.Servers = sandboxServerRows(out.Servers)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
