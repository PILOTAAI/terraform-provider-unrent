package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// ---------------------------------------------------------------- unrent_notification_channel

type channelResource struct{ base }

func newChannelResource() resource.Resource { return &channelResource{} }

type channelModel struct {
	ID              types.String `tfsdk:"id"`
	Type            types.String `tfsdk:"type"`
	Name            types.String `tfsdk:"name"`
	MinSeverity     types.String `tfsdk:"min_severity"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	SettingsWO      types.Map    `tfsdk:"settings_wo"`
	SettingsVersion types.String `tfsdk:"settings_version"`
	Display         types.String `tfsdk:"display"`
}

type channelAPI struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Display     string `json:"display"`
	MinSeverity string `json:"min_severity"`
	Enabled     bool   `json:"enabled"`
}

func (r *channelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_notification_channel"
}

func (r *channelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A channel every alert of the organisation goes to: Slack, Discord, Microsoft Teams, Telegram, ntfy, Pushover or PagerDuty. Its settings (webhook URLs, tokens) are write-only: sent once, checked and sealed by Unrent, never returned, never in the plan or state. Changing `settings_version` replaces the channel with the new settings. Needs an org admin.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"type":         schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{stringvalidator.OneOf("slack", "discord", "teams", "telegram", "ntfy", "pushover", "pagerduty")}},
			"name":         schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Validators: []validator.String{stringvalidator.LengthBetween(1, 64)}},
			"min_severity": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("warning"), Validators: []validator.String{stringvalidator.OneOf("warning", "critical")}},
			"enabled":      schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"settings_wo": schema.MapAttribute{
				Required: true, WriteOnly: true, Sensitive: true, ElementType: types.StringType,
				MarkdownDescription: "slack, discord, teams: `url`; ntfy: `url` and optional `token`; telegram: `bot_token`, `chat_id`; pushover: `token`, `user`; pagerduty: `routing_key`.",
			},
			"settings_version": schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Change it to replace the channel with new `settings_wo`."},
			"display":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "A masked hint of the destination."},
		},
	}
}

func (r *channelResource) set(m *channelModel, c *channelAPI) {
	m.ID = types.StringValue(c.ID)
	m.Type = types.StringValue(c.Type)
	m.Name = types.StringValue(c.Name)
	m.MinSeverity = types.StringValue(c.MinSeverity)
	m.Enabled = types.BoolValue(c.Enabled)
	m.Display = types.StringValue(c.Display)
}

func (r *channelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m channelModel
	var wo types.Map
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("settings_wo"), &wo)...)
	settings := mapOf(ctx, wo, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	b := map[string]any{"type": m.Type.ValueString(), "min_severity": m.MinSeverity.ValueString(), "settings": settings}
	if n := str(m.Name); n != "" {
		b["name"] = n
	}
	var c channelAPI
	if _, err := r.c.Do(ctx, client.OpCreateChannel, nil, b, &c); err != nil {
		fail(&resp.Diagnostics, "Adding the channel failed", err)
		return
	}
	if !m.Enabled.ValueBool() {
		if _, err := r.c.Do(ctx, client.OpUpdateChannel, client.Params{"channel": c.ID}, map[string]any{"enabled": false}, &c); err != nil {
			fail(&resp.Diagnostics, "Turning the channel off failed", err)
		}
	}
	r.set(&m, &c)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *channelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m channelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Channels []channelAPI `json:"channels"`
	}
	if err := r.c.Get(ctx, client.OpListChannels, nil, nil, &out); err != nil {
		fail(&resp.Diagnostics, "Reading channels failed", err)
		return
	}
	for i := range out.Channels {
		if out.Channels[i].ID == m.ID.ValueString() {
			r.set(&m, &out.Channels[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *channelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st channelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := map[string]any{"min_severity": m.MinSeverity.ValueString(), "enabled": m.Enabled.ValueBool()}
	if n := str(m.Name); n != "" {
		b["name"] = n
	}
	var c channelAPI
	if _, err := r.c.Do(ctx, client.OpUpdateChannel, client.Params{"channel": st.ID.ValueString()}, b, &c); err != nil {
		fail(&resp.Diagnostics, "Changing the channel failed", err)
		return
	}
	r.set(&m, &c)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *channelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m channelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.c.Do(ctx, client.OpDeleteChannel, client.Params{"channel": m.ID.ValueString()}, nil, nil); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Removing the channel failed", err)
	}
}

func (r *channelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !channelIDRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected a channel id (chn_…)")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ---------------------------------------------------------------- unrent_status_page

type statusPageResource struct{ base }

func newStatusPageResource() resource.Resource { return &statusPageResource{} }

type statusPageModel struct {
	ID          types.String `tfsdk:"id"`
	Slug        types.String `tfsdk:"slug"`
	Title       types.String `tfsdk:"title"`
	Description types.String `tfsdk:"description"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	URL         types.String `tfsdk:"url"`
}

type statusPageAPI struct {
	Page *struct {
		Slug        *string `json:"slug"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Enabled     bool    `json:"enabled"`
		URL         *string `json:"url"`
	} `json:"page"`
	Components []statusComponentAPI `json:"components"`
}

func (r *statusPageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_status_page"
}

func (r *statusPageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organisation's public status page (one per organisation) at `https://status-<slug>.unrent.page`. Destroying it unpublishes it. Needs an org admin.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"slug":        schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexpMust(`^[a-z0-9](?:[a-z0-9]|-[a-z0-9]){2,39}$`), "3–40 lower-case letters, digits or single hyphens")}},
			"title":       schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 80)}},
			"description": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 300)}},
			"enabled":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Published (default true)."},
			"url":         schema.StringAttribute{Computed: true},
		},
	}
}

func (r *statusPageResource) set(m *statusPageModel, p *statusPageAPI) {
	m.ID = types.StringValue(r.c.Org + "/status-page")
	pg := p.Page
	m.Slug = nullStr(pg.Slug)
	m.Title = types.StringValue(pg.Title)
	m.Description = nullStr(pg.Description)
	m.Enabled = types.BoolValue(pg.Enabled)
	m.URL = nullStr(pg.URL)
}

func (r *statusPageResource) put(ctx context.Context, m *statusPageModel) error {
	b := map[string]any{"slug": m.Slug.ValueString(), "title": m.Title.ValueString(), "enabled": m.Enabled.ValueBool(), "description": nil}
	if d := str(m.Description); d != "" {
		b["description"] = d
	}
	var p statusPageAPI
	if _, err := r.c.Do(ctx, client.OpSetStatusPage, nil, b, &p); err != nil {
		return err
	}
	if p.Page == nil {
		return errorf("the API answered without the page")
	}
	r.set(m, &p)
	return nil
}

func (r *statusPageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m statusPageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Setting up the status page failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *statusPageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m statusPageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Changing the status page failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *statusPageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m statusPageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p statusPageAPI
	if err := r.c.Get(ctx, client.OpGetStatusPage, nil, nil, &p); err != nil {
		fail(&resp.Diagnostics, "Reading the status page failed", err)
		return
	}
	if p.Page == nil || p.Page.Slug == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	r.set(&m, &p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *statusPageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if _, err := r.c.Do(ctx, client.OpSetStatusPage, nil, map[string]any{"enabled": false}, nil); err != nil {
		fail(&resp.Diagnostics, "Unpublishing the status page failed", err)
		return
	}
	resp.Diagnostics.AddWarning("Status page unpublished", "Its address, components and history are kept; publish it again in the console or with this resource.")
}

func (r *statusPageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ---------------------------------------------------------------- unrent_status_component

type statusComponentResource struct{ base }

func newStatusComponentResource() resource.Resource { return &statusComponentResource{} }

type componentServiceModel struct {
	Environment types.String `tfsdk:"environment"`
	Name        types.String `tfsdk:"name"`
}
type componentProbeModel struct {
	Host types.String `tfsdk:"host"`
	Path types.String `tfsdk:"path"`
}

type statusComponentModel struct {
	ID          types.String           `tfsdk:"id"`
	Name        types.String           `tfsdk:"name"`
	Description types.String           `tfsdk:"description"`
	Position    types.Int64            `tfsdk:"position"`
	Service     *componentServiceModel `tfsdk:"service"`
	Probe       *componentProbeModel   `tfsdk:"probe"`
	State       types.String           `tfsdk:"state"`
}

type statusComponentAPI struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Position    int64   `json:"position"`
	Env         *string `json:"env"`
	Service     *string `json:"service"`
	ProbeHost   *string `json:"probe_host"`
	ProbePath   *string `json:"probe_path"`
	State       string  `json:"state"`
}

func (r *statusComponentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_status_component"
}

func (r *statusComponentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A component of the status page: a service's health, an HTTPS probe of one of the organisation's verified domains or default hostnames, or both.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 60)}},
			"description": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"position":    schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Validators: []validator.Int64{int64validator.Between(0, 999)}},
			"service": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
				"environment": schema.StringAttribute{Required: true, Validators: []validator.String{envValidator()}},
				"name":        schema.StringAttribute{Required: true, Validators: []validator.String{nameValidator()}},
			}},
			"probe": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
				"host": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(hostRe, "a domain of this organisation")}},
				"path": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("/")},
			}},
			"state": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *statusComponentResource) body(m *statusComponentModel) map[string]any {
	b := map[string]any{"name": m.Name.ValueString(), "description": nil, "service": nil, "probe": nil}
	if d := str(m.Description); d != "" {
		b["description"] = d
	}
	if m.Service != nil {
		b["service"] = map[string]any{"env": m.Service.Environment.ValueString(), "name": m.Service.Name.ValueString()}
	}
	if m.Probe != nil {
		b["probe"] = map[string]any{"host": m.Probe.Host.ValueString(), "path": firstSet(str(m.Probe.Path), "/")}
	}
	if !m.Position.IsNull() && !m.Position.IsUnknown() {
		b["position"] = m.Position.ValueInt64()
	}
	return b
}

func (r *statusComponentResource) set(m *statusComponentModel, c *statusComponentAPI) {
	m.ID = types.StringValue(c.ID)
	m.Name = types.StringValue(c.Name)
	m.Description = nullStr(c.Description)
	m.Position = types.Int64Value(c.Position)
	m.Service = nil
	if c.Env != nil && c.Service != nil {
		m.Service = &componentServiceModel{Environment: types.StringValue(*c.Env), Name: types.StringValue(*c.Service)}
	}
	m.Probe = nil
	if c.ProbeHost != nil {
		m.Probe = &componentProbeModel{Host: types.StringValue(*c.ProbeHost), Path: types.StringValue(firstSet(deref(c.ProbePath), "/"))}
	}
	if c.State != "" {
		m.State = types.StringValue(c.State)
	} else if m.State.IsNull() || m.State.IsUnknown() {
		m.State = types.StringValue("unknown")
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r *statusComponentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m statusComponentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := r.body(&m)
	delete(b, "position") // a new component goes last; a position is set with an update
	var c statusComponentAPI
	if _, err := r.c.Do(ctx, client.OpCreateStatusComponent, nil, b, &c); err != nil {
		fail(&resp.Diagnostics, "Adding the component failed", err)
		return
	}
	if !m.Position.IsNull() && !m.Position.IsUnknown() && m.Position.ValueInt64() != c.Position {
		if _, err := r.c.Do(ctx, client.OpUpdateStatusComponent, client.Params{"component": c.ID}, map[string]any{"position": m.Position.ValueInt64()}, &c); err != nil {
			fail(&resp.Diagnostics, "Placing the component failed", err)
		}
	}
	r.set(&m, &c)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *statusComponentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st statusComponentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var c statusComponentAPI
	if _, err := r.c.Do(ctx, client.OpUpdateStatusComponent, client.Params{"component": st.ID.ValueString()}, r.body(&m), &c); err != nil {
		fail(&resp.Diagnostics, "Changing the component failed", err)
		return
	}
	r.set(&m, &c)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *statusComponentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m statusComponentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p statusPageAPI
	if err := r.c.Get(ctx, client.OpGetStatusPage, nil, nil, &p); err != nil {
		fail(&resp.Diagnostics, "Reading the status page failed", err)
		return
	}
	for i := range p.Components {
		if p.Components[i].ID == m.ID.ValueString() {
			r.set(&m, &p.Components[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *statusComponentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m statusComponentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.c.Do(ctx, client.OpDeleteStatusComponent, client.Params{"component": m.ID.ValueString()}, nil, nil); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Removing the component failed", err)
	}
}

func (r *statusComponentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
