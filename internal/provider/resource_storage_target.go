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

type storageTargetResource struct{ base }

func newStorageTargetResource() resource.Resource { return &storageTargetResource{} }

type storageTargetModel struct {
	ID                 types.String `tfsdk:"id"`
	ServerID           types.String `tfsdk:"server_id"`
	TargetID           types.String `tfsdk:"target_id"`
	Provider           types.String `tfsdk:"storage_provider"`
	Bucket             types.String `tfsdk:"bucket"`
	Region             types.String `tfsdk:"region"`
	Prefix             types.String `tfsdk:"prefix"`
	Endpoint           types.String `tfsdk:"endpoint"`
	ForcePathStyle     types.Bool   `tfsdk:"force_path_style"`
	StorageClass       types.String `tfsdk:"storage_class"`
	Versioning         types.String `tfsdk:"versioning"`
	AccessKeyIDWO      types.String `tfsdk:"access_key_id_wo"`
	SecretAccessKeyWO  types.String `tfsdk:"secret_access_key_wo"`
	CredentialsVersion types.String `tfsdk:"credentials_version"`
	State              types.String `tfsdk:"state"`
}

type storageTargetAPI struct {
	AgentID  string  `json:"agent_id"`
	ID       string  `json:"id"`
	Provider string  `json:"provider"`
	Bucket   *string `json:"bucket"`
	Prefix   *string `json:"prefix"`
	Region   *string `json:"region"`
	Endpoint *string `json:"endpoint"`
	State    string  `json:"state"`
}

func (r *storageTargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_target"
}

func (r *storageTargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An S3-compatible bucket (AWS, Cloudflare R2, Backblaze B2 or any S3 endpoint) a server backs databases up to. The keys are write-only: sealed to that server before they are sent, never in the plan or the state. Every change is a policy job: the provider prints the approval page and waits for an approver's passkey. The server must encrypt backups first (Settings → Backup encryption).",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"server_id": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{agentValidator()}},
			"target_id": schema.StringAttribute{Required: true, PlanModifiers: replace, MarkdownDescription: "The target's name on the server (what `unrent_database.storage_id` refers to).",
				Validators: []validator.String{stringvalidator.RegexMatches(storageRe, "lower-case letters, digits, - and _"), stringvalidator.NoneOf("local")}},
			"storage_provider":     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("aws", "r2", "b2", "s3")}},
			"bucket":               schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(3, 63)}},
			"region":               schema.StringAttribute{Optional: true, MarkdownDescription: "e.g. `us-east-1`; `auto` for R2 (the default there)."},
			"prefix":               schema.StringAttribute{Optional: true},
			"endpoint":             schema.StringAttribute{Optional: true, MarkdownDescription: "https:// endpoint; required for r2, b2 and s3.", Validators: []validator.String{stringvalidator.RegexMatches(regexpMust(`^https://`), "an https:// URL")}},
			"force_path_style":     schema.BoolAttribute{Optional: true},
			"storage_class":        schema.StringAttribute{Optional: true},
			"versioning":           schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("detect", "enabled", "disabled")}},
			"access_key_id_wo":     schema.StringAttribute{Required: true, WriteOnly: true, Sensitive: true, MarkdownDescription: "Write-only (Terraform/OpenTofu ≥ 1.11); sealed to the server."},
			"secret_access_key_wo": schema.StringAttribute{Required: true, WriteOnly: true, Sensitive: true, MarkdownDescription: "Write-only (Terraform/OpenTofu ≥ 1.11); sealed to the server."},
			"credentials_version":  schema.StringAttribute{Optional: true, MarkdownDescription: "Change it to send new keys (any other change sends them too)."},
			"state":                schema.StringAttribute{Computed: true, MarkdownDescription: "pending_approval, configuring, testing, ok, …"},
		},
	}
}

func (r *storageTargetResource) find(ctx context.Context, agent, id string) (*storageTargetAPI, error) {
	var out struct {
		Targets []storageTargetAPI `json:"targets"`
	}
	if err := r.c.Get(ctx, client.OpListStorageTargets, nil, nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Targets {
		if out.Targets[i].AgentID == agent && out.Targets[i].ID == id {
			return &out.Targets[i], nil
		}
	}
	return nil, nil
}

// keep is an optional string the API reports (empty means not set).
func keep(_ types.String, v *string) types.String {
	if v == nil || *v == "" {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

func (r *storageTargetResource) set(m *storageTargetModel, t *storageTargetAPI) {
	m.ID = types.StringValue(t.AgentID + "/" + t.ID)
	m.ServerID = types.StringValue(t.AgentID)
	m.TargetID = types.StringValue(t.ID)
	m.Provider = types.StringValue(t.Provider)
	m.Bucket = nullStr(t.Bucket)
	m.Prefix = keep(m.Prefix, t.Prefix)
	m.Endpoint = keep(m.Endpoint, t.Endpoint)
	// R2's region defaults to auto when none was configured.
	if !(m.Region.IsNull() && t.Region != nil && *t.Region == "auto" && t.Provider == "r2") {
		m.Region = keep(m.Region, t.Region)
	}
	m.State = types.StringValue(t.State)
}

func (r *storageTargetResource) put(ctx context.Context, m *storageTargetModel, keyID, secret string) error {
	recipient, err := serverRecipient(ctx, r.c, m.ServerID.ValueString())
	if err != nil {
		return err
	}
	ak, err := client.Seal(recipient, keyID)
	if err != nil {
		return err
	}
	sk, err := client.Seal(recipient, secret)
	if err != nil {
		return err
	}
	b := map[string]any{"agent_id": m.ServerID.ValueString(), "id": m.TargetID.ValueString(), "provider": m.Provider.ValueString(), "bucket": m.Bucket.ValueString(), "sealed": map[string]any{"access_key_id": ak, "secret_access_key": sk}}
	for k, v := range map[string]types.String{"region": m.Region, "prefix": m.Prefix, "endpoint": m.Endpoint, "storage_class": m.StorageClass, "versioning": m.Versioning} {
		if s := str(v); s != "" {
			b[k] = s
		}
	}
	if !m.ForcePathStyle.IsNull() && !m.ForcePathStyle.IsUnknown() {
		b["force_path_style"] = m.ForcePathStyle.ValueBool()
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	appr, err := r.c.Do(ctx, client.OpPutStorageTarget, nil, b, &out)
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
	t, err := r.find(ctx, m.ServerID.ValueString(), m.TargetID.ValueString())
	if err != nil {
		return err
	}
	if t != nil {
		r.set(m, t)
	}
	return nil
}

func (r *storageTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m storageTargetModel
	var ak, sk types.String
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("access_key_id_wo"), &ak)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secret_access_key_wo"), &sk)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m, str(ak), str(sk)); err != nil {
		fail(&resp.Diagnostics, "Setting the storage target failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageTargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m storageTargetModel
	var ak, sk types.String
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("access_key_id_wo"), &ak)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secret_access_key_wo"), &sk)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m, str(ak), str(sk)); err != nil {
		fail(&resp.Diagnostics, "Changing the storage target failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m storageTargetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.find(ctx, m.ServerID.ValueString(), m.TargetID.ValueString())
	if err != nil {
		fail(&resp.Diagnostics, "Reading storage targets failed", err)
		return
	}
	if t == nil || t.State == "deleted" {
		resp.State.RemoveResource(ctx)
		return
	}
	r.set(&m, t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m storageTargetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	appr, err := r.c.Do(ctx, client.OpDeleteStorageTarget, client.Params{"agent": m.ServerID.ValueString(), "storage": m.TargetID.ValueString()}, nil, &out)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Removing the storage target failed", err)
		return
	}
	ids := []string{out.JobID}
	if appr != nil {
		ids = appr.JobIDs()
	}
	if err := r.c.WaitJobs(ctx, ids); err != nil {
		fail(&resp.Diagnostics, "Removing the storage target failed on its server", err)
	}
}

func (r *storageTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<server_id>/<target_id>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target_id"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
