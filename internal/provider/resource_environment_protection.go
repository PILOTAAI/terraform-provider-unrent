package provider

// WP2.14 (docs/terraform.md §3, docs/environments-v2.md §5): unrent_environment_protection — which code may reach an
// environment. Saving it issues a policy.refs.set job to every server of the environment, held for approver
// passkeys: apply prints the approval page and waits (approval_timeout), as a destroy of a service does. Destroying
// it turns protection off, which is a policy job too. The control plane enforces the strictest policy until every
// server applied the new one.

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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type protectionResource struct{ base }

func newProtectionResource() resource.Resource { return &protectionResource{} }

type freezeModel struct {
	StartCron types.String `tfsdk:"start_cron"`
	EndCron   types.String `tfsdk:"end_cron"`
	Timezone  types.String `tfsdk:"timezone"`
}

type protectionModel struct {
	ID                  types.String  `tfsdk:"id"`
	Environment         types.String  `tfsdk:"environment"`
	Branches            types.List    `tfsdk:"branches"`
	Tags                types.List    `tfsdk:"tags"`
	Semver              types.String  `tfsdk:"semver"`
	Images              types.String  `tfsdk:"images"`
	PromoteFrom         types.List    `tfsdk:"promote_from"`
	Checks              types.String  `tfsdk:"checks"`
	WaitTimerMinutes    types.Int64   `tfsdk:"wait_timer_minutes"`
	Freeze              []freezeModel `tfsdk:"freeze"`
	PreventSelfApproval types.Bool    `tfsdk:"prevent_self_approval"`
	BypassApprovals     types.Int64   `tfsdk:"bypass_approvals"`
}

// protectionAPI is GET /environments/{env}/protection's `protection` (the desired one; null when off).
type protectionAPI struct {
	Refs struct {
		Branches []string `json:"branches"`
		Tags     []string `json:"tags"`
		Semver   string   `json:"semver"`
	} `json:"refs"`
	Images              string           `json:"images"`
	PromoteFrom         []string         `json:"promote_from"`
	Checks              string           `json:"checks"`
	WaitTimerMin        int64            `json:"wait_timer_min"`
	FreezeRaw           []map[string]any `json:"freeze"`
	PreventSelfApproval *bool            `json:"prevent_self_approval"`
	BypassApprovals     *int64           `json:"bypass_approvals"`
}

func (r *protectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment_protection"
}

func (r *protectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	globs := []validator.List{listvalidator.SizeAtMost(10), listvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexpMust(`^[A-Za-z0-9._/*?-]{1,100}$`), "a glob: * any run without /, ** any run, ? one character"))}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Which code may reach an environment (docs/environments-v2.md §5): allowed branches and tags, repositories, images not built from an allowed ref, promotions, the checks gate, a wait timer, freeze windows and break-glass. Every server of the environment enforces it too. Saving it, and destroying it (protection off), are policy jobs: the provider prints the approval page and waits for approver passkeys (approval_timeout).",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{envValidator()}},
			"branches":    schema.ListAttribute{Optional: true, ElementType: types.StringType, Validators: globs, MarkdownDescription: "Branches (globs) a deploy may come from, e.g. `[\"main\"]`."},
			"tags":        schema.ListAttribute{Optional: true, ElementType: types.StringType, Validators: globs, MarkdownDescription: "Tags (globs), e.g. `[\"v*\"]`."},
			"semver":      schema.StringAttribute{Optional: true, MarkdownDescription: "A SemVer range the tag (after its pattern's prefix) must satisfy, e.g. `>=1.0.0 <2`."},
			"images": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("approval"),
				MarkdownDescription: "Deploys of an image not built from an allowed ref: `allow`, `approval` (one approver passkey) or `deny`.",
				Validators:          []validator.String{stringvalidator.OneOf("allow", "approval", "deny")},
			},
			"promote_from": schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Environments a promotion may come from (the commit must also be on an allowed ref)."},
			"checks":       schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("none"), Validators: []validator.String{stringvalidator.OneOf("none", "required")}, MarkdownDescription: "`required`: wait for the commit's GitHub check suites."},
			"wait_timer_minutes": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.Between(0, 43200)},
				MarkdownDescription: "Minutes a deploy waits before it starts (0–43,200).",
			},
			"prevent_self_approval": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "The requester's own passkey does not count."},
			"bypass_approvals": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(2), Validators: []validator.Int64{int64validator.Between(0, 2)},
				MarkdownDescription: "Break-glass: distinct approver passkeys a deploy outside the protection needs (0: none possible).",
			},
		},
		Blocks: map[string]schema.Block{
			"freeze": schema.ListNestedBlock{
				MarkdownDescription: "A freeze window from each `start_cron` minute to the next `end_cron` minute in `timezone` (IANA); deploys wait, rollbacks stay possible.",
				Validators:          []validator.List{listvalidator.SizeAtMost(10)},
				NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
					"start_cron": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(cronExprRe, "a 5-field cron expression")}},
					"end_cron":   schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(cronExprRe, "a 5-field cron expression")}},
					"timezone":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("UTC")},
				}},
			},
		},
	}
}

func strList(ctx context.Context, v types.List) []string {
	out := []string{}
	if v.IsNull() || v.IsUnknown() {
		return out
	}
	_ = v.ElementsAs(ctx, &out, false)
	return out
}

func listOrNull(v []string, prior types.List) types.List {
	if len(v) == 0 && prior.IsNull() {
		return types.ListNull(types.StringType)
	}
	elems := make([]string, len(v))
	copy(elems, v)
	l, _ := types.ListValueFrom(context.Background(), types.StringType, elems)
	return l
}

// protectionBody is the API's protection object from the model.
func protectionBody(ctx context.Context, m *protectionModel) map[string]any {
	refs := map[string]any{}
	if b := strList(ctx, m.Branches); len(b) > 0 {
		refs["branches"] = b
	}
	if t := strList(ctx, m.Tags); len(t) > 0 {
		refs["tags"] = t
	}
	if s := str(m.Semver); s != "" {
		refs["semver"] = s
	}
	b := map[string]any{"refs": refs, "images": str(m.Images), "checks": str(m.Checks), "wait_timer_min": m.WaitTimerMinutes.ValueInt64(), "prevent_self_approval": m.PreventSelfApproval.ValueBool(), "bypass_approvals": m.BypassApprovals.ValueInt64()}
	if p := strList(ctx, m.PromoteFrom); len(p) > 0 {
		b["promote_from"] = p
	}
	fr := []map[string]any{}
	for _, f := range m.Freeze {
		fr = append(fr, map[string]any{"start_cron": f.StartCron.ValueString(), "end_cron": f.EndCron.ValueString(), "tz": str(f.Timezone)})
	}
	if len(fr) > 0 {
		b["freeze"] = fr
	}
	return b
}

// setProtection copies the API's protection into the model (drift shows as a plan).
func setProtection(m *protectionModel, env string, p *protectionAPI) {
	m.ID = types.StringValue(env)
	m.Environment = types.StringValue(env)
	m.Branches = listOrNull(p.Refs.Branches, m.Branches)
	m.Tags = listOrNull(p.Refs.Tags, m.Tags)
	if p.Refs.Semver != "" || !m.Semver.IsNull() {
		m.Semver = types.StringValue(p.Refs.Semver)
		if p.Refs.Semver == "" {
			m.Semver = types.StringNull()
		}
	}
	m.Images = types.StringValue(p.Images)
	m.PromoteFrom = listOrNull(p.PromoteFrom, m.PromoteFrom)
	if p.Checks == "" {
		p.Checks = "none"
	}
	m.Checks = types.StringValue(p.Checks)
	m.WaitTimerMinutes = types.Int64Value(p.WaitTimerMin)
	if p.PreventSelfApproval != nil {
		m.PreventSelfApproval = types.BoolValue(*p.PreventSelfApproval)
	}
	if p.BypassApprovals != nil {
		m.BypassApprovals = types.Int64Value(*p.BypassApprovals)
	}
	var fr []freezeModel
	for _, f := range p.FreezeRaw {
		sc, _ := f["start_cron"].(string)
		ec, _ := f["end_cron"].(string)
		tz, _ := f["tz"].(string)
		if sc == "" {
			continue
		}
		if tz == "" {
			tz = "UTC"
		}
		fr = append(fr, freezeModel{StartCron: types.StringValue(sc), EndCron: types.StringValue(ec), Timezone: types.StringValue(tz)})
	}
	m.Freeze = fr
}

func (r *protectionResource) get(ctx context.Context, env string) (*protectionAPI, error) {
	var out struct {
		Protection *protectionAPI `json:"protection"`
	}
	if err := r.c.Get(ctx, client.OpGetProtection, client.Params{"env": env}, nil, &out); err != nil {
		return nil, err
	}
	return out.Protection, nil
}

// put saves the protection: approved, it waits until every server applied its policy job.
func (r *protectionResource) put(ctx context.Context, env string, body map[string]any) error {
	appr, err := r.c.Do(ctx, client.OpSetProtection, client.Params{"env": env}, body, nil)
	if err != nil {
		return err
	}
	if appr != nil {
		return r.c.WaitJobs(ctx, appr.JobIDs())
	}
	return nil
}

func (r *protectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m protectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := m.Environment.ValueString()
	if err := r.put(ctx, env, protectionBody(ctx, &m)); err != nil {
		fail(&resp.Diagnostics, "Protecting the environment failed", err)
		return
	}
	m.ID = types.StringValue(env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *protectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m protectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.get(ctx, m.Environment.ValueString())
	if client.IsNotFound(err) || (err == nil && p == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the protection failed", err)
		return
	}
	setProtection(&m, m.Environment.ValueString(), p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *protectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m protectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, m.Environment.ValueString(), protectionBody(ctx, &m)); err != nil {
		fail(&resp.Diagnostics, "Changing the protection failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *protectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m protectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, m.Environment.ValueString(), map[string]any{"enabled": false}); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Turning the protection off failed", err)
	}
}

func (r *protectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !envRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected an environment name")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
