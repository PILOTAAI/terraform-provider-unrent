package provider

// P21 (docs/terraform.md §3, docs/sandboxes.md §3): unrent_sandbox_settings — the organisation's sandbox servers and
// caps, one per organisation (a singleton, like unrent_status_page). Coding agents create sandboxes over MCP, people
// with the CLI or the API; this resource only decides where they may go and how many. Destroying it turns sandboxes
// off (max_open 0, no server): open sandboxes run until their time to live or until they are closed. There is no
// resource for a single sandbox: its time to live would show as drift.

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type sandboxSettingsResource struct{ base }

func newSandboxSettingsResource() resource.Resource { return &sandboxSettingsResource{} }

type sandboxSettingsModel struct {
	ID         types.String `tfsdk:"id"`
	ServerIDs  types.Set    `tfsdk:"server_ids"`
	MaxOpen    types.Int64  `tfsdk:"max_open"`
	PerProject types.Int64  `tfsdk:"per_project"`
	TTLHours   types.Int64  `tfsdk:"ttl_hours"`
	MaxHours   types.Int64  `tfsdk:"max_hours"`
}

type sandboxSettingsAPI struct {
	Servers    []string `json:"servers"`
	MaxOpen    int64    `json:"max_open"`
	PerProject int64    `json:"per_project"`
	TTLHours   int64    `json:"ttl_hours"`
	MaxHours   int64    `json:"max_hours"`
}

func (r *sandboxSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_settings"
}

func (r *sandboxSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organisation's sandbox settings (one per organisation): the sandbox servers coding agents' sandboxes go to, and the caps. " +
			"A sandbox with a database copy goes to the server holding that database; any other to the server with the most budget (half its memory) left. " +
			"No sandbox server may run anything of a production-tier environment: the API refuses one that does, naming it. " +
			"Destroying this resource turns sandboxes off (`max_open` 0, no server); open sandboxes run until their time to live. Needs an org admin and the `manage` scope. " +
			"There is no resource for a single sandbox (its time to live would be drift): read them with the `unrent_sandboxes` data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"server_ids": schema.SetAttribute{
				Required: true, ElementType: types.StringType, MarkdownDescription: "The sandbox servers (at most 20; empty for none). See the `unrent_servers` data source.",
				Validators: []validator.Set{setvalidator.SizeAtMost(20), setvalidator.ValueStringsAre(agentValidator())},
			},
			"max_open":    schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(5), Validators: []validator.Int64{int64validator.Between(0, 50)}, MarkdownDescription: "Open sandboxes in the organisation (0 turns them off). Default 5."},
			"per_project": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3), Validators: []validator.Int64{int64validator.Between(0, 20)}, MarkdownDescription: "Open sandboxes per project. Default 3."},
			"ttl_hours":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(4), Validators: []validator.Int64{int64validator.Between(1, 168)}, MarkdownDescription: "The default time to live, at most `max_hours`. Default 4."},
			"max_hours":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(72), Validators: []validator.Int64{int64validator.Between(1, 168)}, MarkdownDescription: "The longest life of a sandbox, from its creation. Default 72."},
		},
	}
}

// sandboxSettingsBody is the PUT body for the model (the whole settings: servers replace the list).
func sandboxSettingsBody(ctx context.Context, m *sandboxSettingsModel) (map[string]any, error) {
	servers := []string{}
	if !m.ServerIDs.IsNull() && !m.ServerIDs.IsUnknown() {
		if d := m.ServerIDs.ElementsAs(ctx, &servers, false); d.HasError() {
			return nil, errorf("server_ids: %v", d)
		}
	}
	sort.Strings(servers)
	return map[string]any{"servers": servers, "max_open": m.MaxOpen.ValueInt64(), "per_project": m.PerProject.ValueInt64(), "ttl_hours": m.TTLHours.ValueInt64(), "max_hours": m.MaxHours.ValueInt64()}, nil
}

func setSandboxSettings(ctx context.Context, m *sandboxSettingsModel, org string, a *sandboxSettingsAPI) {
	m.ID = types.StringValue(org + "/sandbox-settings")
	servers := a.Servers
	if servers == nil {
		servers = []string{}
	}
	m.ServerIDs, _ = types.SetValueFrom(ctx, types.StringType, servers)
	m.MaxOpen = types.Int64Value(a.MaxOpen)
	m.PerProject = types.Int64Value(a.PerProject)
	m.TTLHours = types.Int64Value(a.TTLHours)
	m.MaxHours = types.Int64Value(a.MaxHours)
}

func (r *sandboxSettingsResource) put(ctx context.Context, m *sandboxSettingsModel) error {
	b, err := sandboxSettingsBody(ctx, m)
	if err != nil {
		return err
	}
	var out struct {
		Settings sandboxSettingsAPI `json:"settings"`
	}
	if _, err := r.c.Do(ctx, client.OpSetSandboxSettings, nil, b, &out); err != nil {
		return err
	}
	setSandboxSettings(ctx, m, r.c.Org, &out.Settings)
	return nil
}

func (r *sandboxSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m sandboxSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Setting the sandbox settings failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sandboxSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m sandboxSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Changing the sandbox settings failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sandboxSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sandboxSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Settings sandboxSettingsAPI `json:"settings"`
	}
	if err := r.c.Get(ctx, client.OpGetSandboxSettings, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Reading the sandbox settings failed", err)
		return
	}
	setSandboxSettings(ctx, &m, r.c.Org, &out.Settings)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Delete turns sandboxes off: no server, max_open 0 (the other caps stay as they are).
func (r *sandboxSettingsResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if _, err := r.c.Do(ctx, client.OpSetSandboxSettings, nil, map[string]any{"servers": []string{}, "max_open": 0}, nil); err != nil {
		fail(&resp.Diagnostics, "Turning sandboxes off failed", err)
		return
	}
	resp.Diagnostics.AddWarning("Sandboxes turned off", "No new sandbox can be created (no sandbox server, max_open 0). Open sandboxes run until their time to live or until they are closed.")
}

func (r *sandboxSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
