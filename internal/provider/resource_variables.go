package provider

// WP2.14 (docs/terraform.md §3, docs/environments-v2.md §3.4): unrent_variables — the project layer or one
// environment's layer of variables that this configuration owns (service variables are unrent_service_variables).
// Plain values are compared on every refresh. Secret values are write-only: sealed here to each environment's key
// (WP2.3) before they are sent, never in the plan or the state, re-sent when secrets_version changes. A project
// secret is one ciphertext per environment of the project that has a key: secrets never inherit implicitly, so it
// exists only where it was sealed.

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
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

type variablesResource struct{ base }

func newVariablesResource() resource.Resource { return &variablesResource{} }

type variablesModel struct {
	ID             types.String `tfsdk:"id"`
	Scope          types.String `tfsdk:"scope"`
	ProjectID      types.String `tfsdk:"project_id"`
	Environment    types.String `tfsdk:"environment"`
	Variables      types.Map    `tfsdk:"variables"`
	SecretsWO      types.Map    `tfsdk:"secrets_wo"`
	SecretsVersion types.String `tfsdk:"secrets_version"`
	SecretNames    types.List   `tfsdk:"secret_names"`
}

type layerAPI struct {
	Recipient    *string `json:"recipient"`
	Environments []struct {
		Env       string  `json:"env"`
		Recipient *string `json:"recipient"`
	} `json:"environments"`
	Vars []struct {
		Name  string  `json:"name"`
		Kind  string  `json:"kind"`
		Value *string `json:"value"`
	} `json:"vars"`
}

func (r *variablesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_variables"
}

func (r *variablesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	names := mapvalidator.KeysAre(stringvalidator.RegexMatches(variableRe, "upper-case names such as LOG_FORMAT"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "A layer of variables this configuration owns: the project's (every environment of the project, a service's own variable wins) or one environment's. Plain values are compared on every refresh; secrets are write-only, sealed here to each environment's key, never in the plan or the state (bump `secrets_version` to send new values). Values an `unrent.toml` sets are refused (managed_by_file). Each service the layer reaches gets a new config version, applied under its own gates.",
		Attributes: map[string]schema.Attribute{
			"id":    schema.StringAttribute{Computed: true, MarkdownDescription: "`project/<project_id>` or `environment/<name>`", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"scope": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.OneOf("project", "environment")}},
			"project_id": schema.StringAttribute{
				Optional: true, PlanModifiers: replace, Validators: []validator.String{projectValidator(), stringvalidator.ConflictsWith(path.MatchRoot("environment"))},
				MarkdownDescription: "scope project: the project.",
			},
			"environment": schema.StringAttribute{Optional: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}, MarkdownDescription: "scope environment: the environment."},
			"variables":   schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Plain values by name.", Validators: []validator.Map{names}},
			"secrets_wo": schema.MapAttribute{
				Optional: true, WriteOnly: true, Sensitive: true, ElementType: types.StringType,
				MarkdownDescription: "Secret values by name (write-only, Terraform/OpenTofu ≥ 1.11): sealed to the environment's key, or to the key of every environment of the project that has one. Sent when the names change or `secrets_version` changes.",
				Validators:          []validator.Map{names},
			},
			"secrets_version": schema.StringAttribute{Optional: true, MarkdownDescription: "Change it to send `secrets_wo` again."},
			"secret_names":    schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Every secret of the layer (names only)."},
		},
	}
}

// path of the layer, and the params of a call to one variable of it.
func (r *variablesResource) list(m *variablesModel) (client.Operation, client.Params) {
	if m.Scope.ValueString() == "project" {
		return client.OpListProjectVariables, client.Params{"project": m.ProjectID.ValueString()}
	}
	return client.OpListEnvironmentVariables, client.Params{"env": m.Environment.ValueString()}
}

func (r *variablesResource) one(m *variablesModel, name string, del bool) (client.Operation, client.Params) {
	if m.Scope.ValueString() == "project" {
		op := client.OpSetProjectVariable
		if del {
			op = client.OpDeleteProjectVariable
		}
		return op, client.Params{"project": m.ProjectID.ValueString(), "var": name}
	}
	op := client.OpSetEnvironmentVariable
	if del {
		op = client.OpDeleteEnvironmentVariable
	}
	return op, client.Params{"env": m.Environment.ValueString(), "var": name}
}

// sealedBody seals value for the layer: the environment's key, or every keyed environment of the project.
func sealedBody(scope string, l *layerAPI, value string) (map[string]any, error) {
	if scope == "environment" {
		if l.Recipient == nil || *l.Recipient == "" {
			return nil, errorf("the environment has no key yet (create one in the console): secrets cannot be sealed to it")
		}
		s, err := client.Seal(*l.Recipient, value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "secret", "sealed": s}, nil
	}
	per := map[string]string{}
	for _, e := range l.Environments {
		if e.Recipient == nil || *e.Recipient == "" {
			continue
		}
		s, err := client.Seal(*e.Recipient, value)
		if err != nil {
			return nil, err
		}
		per[e.Env] = s
	}
	if len(per) == 0 {
		return nil, errorf("no environment of the project has a key yet: secrets cannot be sealed")
	}
	return map[string]any{"kind": "secret", "sealed_for_env": per}, nil
}

func (r *variablesResource) get(ctx context.Context, m *variablesModel) (*layerAPI, error) {
	var l layerAPI
	op, p := r.list(m)
	return &l, r.c.Get(ctx, op, p, nil, &l)
}

func (r *variablesResource) change(ctx context.Context, m *variablesModel, plain, secrets map[string]string, del []string) error {
	var l *layerAPI
	if len(secrets) > 0 {
		var err error
		if l, err = r.get(ctx, m); err != nil {
			return err
		}
	}
	for _, k := range keys(plain) {
		op, p := r.one(m, k, false)
		if _, err := r.c.Do(ctx, op, p, map[string]any{"kind": "plain", "value": plain[k]}, nil); err != nil {
			return err
		}
	}
	for _, k := range keys(secrets) {
		body, err := sealedBody(m.Scope.ValueString(), l, secrets[k])
		if err != nil {
			return err
		}
		op, p := r.one(m, k, false)
		if _, err := r.c.Do(ctx, op, p, body, nil); err != nil {
			return err
		}
	}
	for _, k := range del {
		op, p := r.one(m, k, true)
		if _, err := r.c.Do(ctx, op, p, nil, nil); err != nil && !client.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *variablesResource) refresh(ctx context.Context, m *variablesModel, own managed, importing bool) error {
	l, err := r.get(ctx, m)
	if err != nil {
		return err
	}
	plain := map[string]string{}
	var secretNames []string
	have := map[string]bool{}
	for _, v := range l.Vars {
		have[v.Name] = true
		if v.Kind == "secret" {
			secretNames = append(secretNames, v.Name)
			continue
		}
		if (importing || contains(own.Plain, v.Name)) && v.Value != nil {
			plain[v.Name] = *v.Value
		}
	}
	sort.Strings(secretNames)
	if len(plain) > 0 || !m.Variables.IsNull() {
		mv, _ := types.MapValueFrom(ctx, types.StringType, plain)
		m.Variables = mv
	}
	var missing []string
	for _, s := range own.Secrets {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		m.SecretsVersion = types.StringValue(str(m.SecretsVersion) + " (missing on the server: " + strings.Join(missing, ", ") + ")")
	}
	sn, _ := types.ListValueFrom(ctx, types.StringType, secretNames)
	m.SecretNames = sn
	if m.Scope.ValueString() == "project" {
		m.ID = types.StringValue("project/" + m.ProjectID.ValueString())
	} else {
		m.ID = types.StringValue("environment/" + m.Environment.ValueString())
	}
	return nil
}

func (r *variablesResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m variablesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if m.Scope.IsUnknown() {
		return
	}
	if m.Scope.ValueString() == "project" && m.ProjectID.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("project_id"), "project_id required", "scope = \"project\" names its project")
	}
	if m.Scope.ValueString() == "environment" && m.Environment.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("environment"), "environment required", "scope = \"environment\" names its environment")
	}
}

func (r *variablesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m variablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	var wo types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secrets_wo"), &wo)...)
	plain := mapOf(ctx, m.Variables, &resp.Diagnostics)
	secrets := mapOf(ctx, wo, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.change(ctx, &m, plain, secrets, nil); err != nil {
		fail(&resp.Diagnostics, "Setting the variables failed", err)
		return
	}
	own := managed{Plain: keys(plain), Secrets: keys(secrets)}
	b, _ := json.Marshal(own)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, "managed", b)...)
	if err := r.refresh(ctx, &m, own, false); err != nil {
		fail(&resp.Diagnostics, "Reading the variables failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *variablesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m variablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, _ := req.Private.GetKey(ctx, "managed")
	own := loadManaged(ctx, req.Private)
	err := r.refresh(ctx, &m, own, b == nil)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		fail(&resp.Diagnostics, "Reading the variables failed", err)
		return
	}
	if b == nil {
		nb, _ := json.Marshal(managed{Plain: keys(mapOf(ctx, m.Variables, &resp.Diagnostics))})
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "managed", nb)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *variablesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st variablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	var wo types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secrets_wo"), &wo)...)
	plain := mapOf(ctx, m.Variables, &resp.Diagnostics)
	secrets := mapOf(ctx, wo, &resp.Diagnostics)
	before := mapOf(ctx, st.Variables, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	own := loadManaged(ctx, req.Private)
	setPlain := map[string]string{}
	for k, v := range plain {
		if old, ok := before[k]; !ok || old != v {
			setPlain[k] = v
		}
	}
	sendSecrets := map[string]string{}
	if !m.SecretsVersion.Equal(st.SecretsVersion) || strings.Join(keys(secrets), ",") != strings.Join(own.Secrets, ",") {
		sendSecrets = secrets
	}
	var del []string
	for _, k := range append(append([]string{}, own.Plain...), own.Secrets...) {
		_, p := plain[k]
		_, s := secrets[k]
		if !p && !s && !contains(del, k) {
			del = append(del, k)
		}
	}
	if err := r.change(ctx, &m, setPlain, sendSecrets, del); err != nil {
		fail(&resp.Diagnostics, "Changing the variables failed", err)
		return
	}
	own = managed{Plain: keys(plain), Secrets: keys(secrets)}
	b, _ := json.Marshal(own)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, "managed", b)...)
	if err := r.refresh(ctx, &m, own, false); err != nil {
		fail(&resp.Diagnostics, "Reading the variables failed", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *variablesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m variablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	own := loadManaged(ctx, req.Private)
	if err := r.change(ctx, &m, nil, nil, append(append([]string{}, own.Plain...), own.Secrets...)); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Deleting the variables failed", err)
	}
}

// ImportState: `project/<project_id>` or `environment/<name>`.
func (r *variablesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "project/<project_id> or environment/<name>", &resp.Diagnostics)
	if p == nil {
		return
	}
	switch {
	case p[0] == "project" && projectRe.MatchString(p[1]):
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), p[1])...)
	case p[0] == "environment" && envRe.MatchString(p[1]):
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), p[1])...)
	default:
		resp.Diagnostics.AddError("Invalid import id", "expected project/<project_id> or environment/<name>, got "+url.PathEscape(req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
