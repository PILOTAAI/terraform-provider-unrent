package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// ---------------------------------------------------------------- unrent_database

type databaseResource struct{ base }

func newDatabaseResource() resource.Resource { return &databaseResource{} }

type databaseModel struct {
	ID          types.String `tfsdk:"id"`
	Environment types.String `tfsdk:"environment"`
	Name        types.String `tfsdk:"name"`
	ServerID    types.String `tfsdk:"server_id"`
	Engine      types.String `tfsdk:"engine"`
	Version     types.String `tfsdk:"version"`
	MemoryMB    types.Int64  `tfsdk:"memory_mb"`
	StorageID   types.String `tfsdk:"storage_id"`
	FinalBackup types.String `tfsdk:"final_backup"`
	KeepDays    types.Int64  `tfsdk:"keep_days"`
	State       types.String `tfsdk:"state"`
	Connection  types.Map    `tfsdk:"connection_values"`
	SecretNames types.List   `tfsdk:"secret_names"`
}

type databaseAPI struct {
	Env      string  `json:"env"`
	Name     string  `json:"name"`
	AgentID  string  `json:"agent_id"`
	Engine   string  `json:"engine"`
	Version  string  `json:"version"`
	MemoryMB int64   `json:"memory_mb"`
	State    string  `json:"state"`
	Error    *string `json:"error"`
	Storage  struct {
		ID string `json:"id"`
	} `json:"storage"`
	Connection *struct {
		Plain       map[string]string `json:"plain"`
		SecretNames []string          `json:"secret_names"`
	} `json:"connection"`
	Links []string `json:"links"`
}

func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A database (Postgres, MySQL, MariaDB, Valkey, Redis, ClickHouse, Meilisearch, OpenSearch, NATS JetStream, RabbitMQ, MongoDB or DocumentDB (MongoDB-compatible); ClickHouse and OpenSearch need a server with at least 4 GB) on one server with verified backups to a storage target. Its password is generated on the server and sealed there: only connection names and plain values are visible. **Destroying it drops it**: a destructive job (after a final backup, unless waived) that waits for an approver's passkey; unlink every service first. Version, memory and storage changes are not made through the API (use the console: they are upgrades and verified moves), so the plan refuses them rather than dropping the database.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"name":        schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{dbValidator()}},
			"server_id":   schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{agentValidator()}},
			"engine": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("postgres"), PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.OneOf("postgres", "mysql", "mariadb", "valkey", "redis", "clickhouse", "meilisearch", "opensearch", "nats", "rabbitmq", "mongodb", "documentdb")}},
			"version":    schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "The engine version (default: the newest Unrent supports)."},
			"memory_mb":  schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Validators: []validator.Int64{int64validator.Between(256, 262144)}},
			"storage_id": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("local"), MarkdownDescription: "Where backups go: `local` or an `unrent_storage_target` of the same server.", Validators: []validator.String{stringvalidator.RegexMatches(storageRe, "a storage target id")}},
			"final_backup": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("take"),
				MarkdownDescription: "On destroy: `take` a final backup first (default) or `waive` it.", Validators: []validator.String{stringvalidator.OneOf("take", "waive")}},
			"keep_days":         schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(7), MarkdownDescription: "On destroy: days the data volume is kept (1–30).", Validators: []validator.Int64{int64validator.Between(1, 30)}},
			"state":             schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"connection_values": schema.MapAttribute{Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Plain connection values (host, port, database, user)."},
			"secret_names":      schema.ListAttribute{Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Names of the sealed connection values (the password): link a service to receive them."},
		},
	}
}

// ModifyPlan refuses changes the API does not make in place, instead of replacing (dropping) the database.
func (r *databaseResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, st databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, changed := range map[string]bool{
		"version":    !plan.Version.IsUnknown() && !plan.Version.Equal(st.Version),
		"memory_mb":  !plan.MemoryMB.IsUnknown() && !plan.MemoryMB.Equal(st.MemoryMB),
		"storage_id": !plan.StorageID.IsUnknown() && !plan.StorageID.Equal(st.StorageID),
	} {
		if changed {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Not changed through the API", fmt.Sprintf("Changing %s of an existing database is an upgrade or a verified move: do it in the console (Databases → %s), then set the new value here.", name, st.Name.ValueString()))
		}
	}
}

func (r *databaseResource) params(m *databaseModel) client.Params {
	return client.Params{"env": m.Environment.ValueString(), "db": m.Name.ValueString()}
}

func (r *databaseResource) get(ctx context.Context, m *databaseModel) (*databaseAPI, error) {
	var d databaseAPI
	if err := r.c.Get(ctx, client.OpGetDatabase, r.params(m), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *databaseResource) set(ctx context.Context, m *databaseModel, d *databaseAPI) {
	m.ID = types.StringValue(d.Env + "/" + d.Name)
	m.Environment = types.StringValue(d.Env)
	m.Name = types.StringValue(d.Name)
	m.ServerID = types.StringValue(d.AgentID)
	m.Engine = types.StringValue(d.Engine)
	m.Version = types.StringValue(d.Version)
	m.MemoryMB = types.Int64Value(d.MemoryMB)
	m.StorageID = types.StringValue(d.Storage.ID)
	m.State = types.StringValue(d.State)
	plain := map[string]string{}
	names := []string{}
	if d.Connection != nil {
		plain = d.Connection.Plain
		names = append(names, d.Connection.SecretNames...)
	}
	sort.Strings(names)
	m.Connection, _ = types.MapValueFrom(ctx, types.StringType, plain)
	m.SecretNames, _ = types.ListValueFrom(ctx, types.StringType, names)
	if m.FinalBackup.IsNull() || m.FinalBackup.IsUnknown() {
		m.FinalBackup = types.StringValue("take")
	}
	if m.KeepDays.IsNull() || m.KeepDays.IsUnknown() {
		m.KeepDays = types.Int64Value(7)
	}
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"env": m.Environment.ValueString(), "name": m.Name.ValueString(), "agent_id": m.ServerID.ValueString(), "engine": m.Engine.ValueString(), "storage_id": m.StorageID.ValueString()}
	if v := str(m.Version); v != "" {
		body["version"] = v
	}
	if !m.MemoryMB.IsNull() && !m.MemoryMB.IsUnknown() {
		body["memory_mb"] = m.MemoryMB.ValueInt64()
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	appr, err := r.c.Do(ctx, client.OpCreateDatabase, nil, body, &out)
	if err != nil {
		fail(&resp.Diagnostics, "Creating the database failed", err)
		return
	}
	ids := []string{out.JobID}
	if appr != nil {
		ids = appr.JobIDs()
	}
	werr := r.c.WaitJobs(ctx, ids)
	d, gerr := r.get(ctx, &m)
	if gerr == nil {
		r.set(ctx, &m, d)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
	if werr != nil {
		fail(&resp.Diagnostics, "The database was not created on its server", werr)
		return
	}
	if gerr != nil {
		fail(&resp.Diagnostics, "Reading the database failed", gerr)
	}
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.get(ctx, &m)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the database failed", err)
		return
	}
	r.set(ctx, &m, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update only changes what happens on destroy (final_backup, keep_days): ModifyPlan refused everything else.
func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.get(ctx, &m)
	if err != nil {
		fail(&resp.Diagnostics, "Reading the database failed", err)
		return
	}
	r.set(ctx, &m, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"confirm": m.Name.ValueString(), "final_backup": m.FinalBackup.ValueString(), "keep_days": m.KeepDays.ValueInt64()}
	if m.FinalBackup.ValueString() == "waive" {
		body["waiver"] = "drop " + m.Name.ValueString() + " without a final backup"
	}
	var out struct {
		Jobs []client.JobRef `json:"jobs"`
	}
	appr, err := r.c.Do(ctx, client.OpDropDatabase, r.params(&m), body, &out)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Dropping the database failed", err)
		return
	}
	ids := jobIDs(out.Jobs)
	if appr != nil {
		ids = appr.JobIDs()
	}
	if err := r.c.WaitJobs(ctx, ids); err != nil {
		fail(&resp.Diagnostics, "Dropping the database failed on its server", err)
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<environment>/<name>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ---------------------------------------------------------------- unrent_database_link

type databaseLinkResource struct{ base }

func newDatabaseLinkResource() resource.Resource { return &databaseLinkResource{} }

type databaseLinkModel struct {
	ID          types.String `tfsdk:"id"`
	Environment types.String `tfsdk:"environment"`
	Database    types.String `tfsdk:"database"`
	Service     types.String `tfsdk:"service"`
	Apply       types.Bool   `tfsdk:"apply"`
	Names       types.List   `tfsdk:"names"`
}

func (r *databaseLinkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database_link"
}

func (r *databaseLinkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Links a database to a service on the same server: its connection (the password sealed to that server) becomes the service's variables. With `apply` (default) the service is reconfigured at once.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"database":    schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{dbValidator()}},
			"service":     schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{nameValidator()}},
			"apply":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"names":       schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The variables the link set.", PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *databaseLinkResource) applyEnv(ctx context.Context, m *databaseLinkModel) error {
	if !m.Apply.ValueBool() {
		return nil
	}
	var dep struct {
		Deployment *deployment `json:"deployment"`
	}
	appr, err := r.c.Do(ctx, client.OpApplyEnv, client.Params{"env": m.Environment.ValueString(), "name": m.Service.ValueString()}, map[string]any{}, &dep)
	if err != nil {
		return err
	}
	id := ""
	if dep.Deployment != nil {
		id = dep.Deployment.ID
	}
	if appr != nil {
		id = appr.DeploymentID
	}
	_, err = waitDeployment(ctx, r.c, id)
	return err
}

func (r *databaseLinkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m databaseLinkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Linked struct {
			Names []string `json:"names"`
		} `json:"linked"`
	}
	if _, err := r.c.Do(ctx, client.OpLinkDatabase, client.Params{"env": m.Environment.ValueString(), "db": m.Database.ValueString()}, map[string]any{"service": m.Service.ValueString()}, &out); err != nil {
		fail(&resp.Diagnostics, "Linking the database failed", err)
		return
	}
	m.ID = types.StringValue(m.Environment.ValueString() + "/" + m.Database.ValueString() + "/" + m.Service.ValueString())
	m.Names, _ = types.ListValueFrom(ctx, types.StringType, out.Linked.Names)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if err := r.applyEnv(ctx, &m); err != nil {
		fail(&resp.Diagnostics, "Applying the link to the service failed", err)
	}
}

func (r *databaseLinkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m databaseLinkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var d databaseAPI
	err := r.c.Get(ctx, client.OpGetDatabase, client.Params{"env": m.Environment.ValueString(), "db": m.Database.ValueString()}, nil, &d)
	if client.IsNotFound(err) || (err == nil && !contains(d.Links, m.Service.ValueString())) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the database failed", err)
		return
	}
	m.ID = types.StringValue(m.Environment.ValueString() + "/" + m.Database.ValueString() + "/" + m.Service.ValueString())
	if m.Apply.IsNull() || m.Apply.IsUnknown() {
		m.Apply = types.BoolValue(true)
	}
	if m.Names.IsNull() || m.Names.IsUnknown() {
		names := []string{}
		if d.Connection != nil {
			for k := range d.Connection.Plain {
				names = append(names, k)
			}
			names = append(names, d.Connection.SecretNames...)
		}
		sort.Strings(names)
		m.Names, _ = types.ListValueFrom(ctx, types.StringType, names)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *databaseLinkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st databaseLinkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	m.Names = st.Names
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *databaseLinkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m databaseLinkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.c.Do(ctx, client.OpUnlinkDatabase, client.Params{"env": m.Environment.ValueString(), "db": m.Database.ValueString(), "name": m.Service.ValueString()}, nil, nil)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Unlinking the database failed", err)
		return
	}
	if err := r.applyEnv(ctx, &m); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Applying the unlink to the service failed", err)
	}
}

func (r *databaseLinkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 3, "<environment>/<database>/<service>", &resp.Diagnostics)
	if p == nil {
		return
	}
	for i, k := range []string{"environment", "database", "service"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), p[i])...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
