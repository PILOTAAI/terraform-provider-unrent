package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

type cronResource struct{ base }

func newCronResource() resource.Resource { return &cronResource{} }

type cronModel struct {
	ID          types.String `tfsdk:"id"`
	Environment types.String `tfsdk:"environment"`
	Service     types.String `tfsdk:"service"`
	Name        types.String `tfsdk:"name"`
	Schedule    types.String `tfsdk:"schedule"`
	Command     types.List   `tfsdk:"command"`
	TimeoutS    types.Int64  `tfsdk:"timeout_s"`
	State       types.String `tfsdk:"state"`
}

type cronAPI struct {
	Name     string   `json:"name"`
	Schedule string   `json:"schedule"`
	Argv     []string `json:"argv"`
	TimeoutS int64    `json:"timeout_s"`
	State    string   `json:"state"`
}

func (r *cronResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cron"
}

func (r *cronResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A schedule: `command` runs in a new container of the service's image, on its server, at `schedule` (UTC). The provider waits for the server to confirm each change.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"service":     schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{nameValidator()}},
			"name":        schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(cronRe, "lower-case letters, digits and hyphens")}},
			"schedule":    schema.StringAttribute{Required: true, MarkdownDescription: "A 5-field cron expression in UTC, e.g. `0 3 * * *`.", Validators: []validator.String{stringvalidator.RegexMatches(cronExprRe, "5 fields, e.g. 0 3 * * *")}},
			"command": schema.ListAttribute{
				Required: true, ElementType: types.StringType, MarkdownDescription: "The argv (no shell): e.g. `[\"node\", \"cleanup.js\"]`.",
				Validators: []validator.List{listvalidator.SizeBetween(1, 32), listvalidator.ValueStringsAre(stringvalidator.LengthBetween(0, 4096))},
			},
			"timeout_s": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3600), Validators: []validator.Int64{int64validator.Between(10, 86400)}},
			"state":     schema.StringAttribute{Computed: true},
		},
	}
}

func (r *cronResource) params(m *cronModel) client.Params {
	return client.Params{"env": m.Environment.ValueString(), "name": m.Service.ValueString(), "cron": m.Name.ValueString()}
}

func (r *cronResource) find(ctx context.Context, m *cronModel) (*cronAPI, error) {
	var out struct {
		Crons []cronAPI `json:"crons"`
	}
	if err := r.c.Get(ctx, client.OpListCrons, r.params(m), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Crons {
		if out.Crons[i].Name == m.Name.ValueString() && out.Crons[i].State != "deleting" {
			return &out.Crons[i], nil
		}
	}
	return nil, nil
}

func (r *cronResource) set(ctx context.Context, m *cronModel, c *cronAPI) {
	m.ID = types.StringValue(m.Environment.ValueString() + "/" + m.Service.ValueString() + "/" + c.Name)
	m.Schedule = types.StringValue(c.Schedule)
	cmd, _ := types.ListValueFrom(ctx, types.StringType, c.Argv)
	m.Command = cmd
	m.TimeoutS = types.Int64Value(c.TimeoutS)
	m.State = types.StringValue(c.State)
}

func (r *cronResource) put(ctx context.Context, m *cronModel) error {
	var argv []string
	if d := m.Command.ElementsAs(ctx, &argv, false); d.HasError() {
		return errorf("command: %v", d)
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	appr, err := r.c.Do(ctx, client.OpPutCron, r.params(m), map[string]any{"schedule": m.Schedule.ValueString(), "argv": argv, "timeout_s": m.TimeoutS.ValueInt64()}, &out)
	if err != nil {
		return err
	}
	ids := []string{out.JobID}
	if appr != nil {
		ids = appr.JobIDs()
	}
	if err := r.c.WaitJobs(ctx, ids); err != nil {
		return err
	}
	c, err := r.find(ctx, m)
	if err != nil {
		return err
	}
	if c == nil {
		return errorf("the schedule is not listed after it was saved")
	}
	r.set(ctx, m, c)
	return nil
}

func (r *cronResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m cronModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Saving the schedule failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *cronResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m cronModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Saving the schedule failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *cronResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m cronModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && c == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the schedule failed", err)
		return
	}
	r.set(ctx, &m, c)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *cronResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m cronModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	appr, err := r.c.Do(ctx, client.OpDeleteCron, r.params(&m), nil, &out)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Deleting the schedule failed", err)
		return
	}
	ids := []string{out.JobID}
	if appr != nil {
		ids = appr.JobIDs()
	}
	if err := r.c.WaitJobs(ctx, ids); err != nil {
		fail(&resp.Diagnostics, "Deleting the schedule failed on its server", err)
	}
}

func (r *cronResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 3, "<environment>/<service>/<name>", &resp.Diagnostics)
	if p == nil {
		return
	}
	for i, k := range []string{"environment", "service", "name"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), p[i])...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
