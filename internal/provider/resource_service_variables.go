package provider

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

// unrent_service_variables: the variables a configuration owns on one service (others — a database link's, the
// console's — are left alone). Plain values are compared on every refresh; secret values are write-only: sealed
// to the server's public key at apply, never in the plan or the state, re-sent when secrets_version changes.

type serviceVariablesResource struct{ base }

func newServiceEnvResource() resource.Resource { return &serviceVariablesResource{} }

type serviceVariablesModel struct {
	ID             types.String `tfsdk:"id"`
	Environment    types.String `tfsdk:"environment"`
	Service        types.String `tfsdk:"service"`
	Variables      types.Map    `tfsdk:"variables"`
	SecretsWO      types.Map    `tfsdk:"secrets_wo"`
	SecretsVersion types.String `tfsdk:"secrets_version"`
	SecretNames    types.List   `tfsdk:"secret_names"`
	Apply          types.Bool   `tfsdk:"apply"`
	ConfigVersion  types.Int64  `tfsdk:"config_version"`
}

// managed is the private state: which names this resource set (values are never kept for secrets).
type managed struct {
	Plain   []string `json:"plain"`
	Secrets []string `json:"secrets"`
}

type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

func loadManaged(ctx context.Context, p privateState) managed {
	var m managed
	if p == nil {
		return m
	}
	b, _ := p.GetKey(ctx, "managed")
	_ = json.Unmarshal(b, &m)
	return m
}

type envVarsAPI struct {
	Version int64 `json:"version"`
	Vars    []struct {
		Name  string  `json:"name"`
		Kind  string  `json:"kind"`
		Value *string `json:"value"`
	} `json:"vars"`
}

func (r *serviceVariablesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_variables"
}

func (r *serviceVariablesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	names := mapvalidator.KeysAre(stringvalidator.RegexMatches(variableRe, "upper-case names such as DATABASE_URL"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Variables of a service that this configuration owns: plain values (compared on every refresh) and secrets (write-only: sealed to the service's server before they are sent, never in the plan or the state; bump `secrets_version` to send new values). Variables set elsewhere, such as a database link's, are left alone. With `apply` (default) every change is applied to the running service; in an environment with an approval floor that waits for a passkey.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"service":     schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{nameValidator()}},
			"variables":   schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Plain values by name.", Validators: []validator.Map{names}},
			"secrets_wo": schema.MapAttribute{
				Optional: true, WriteOnly: true, Sensitive: true, ElementType: types.StringType,
				MarkdownDescription: "Secret values by name (write-only, Terraform/OpenTofu ≥ 1.11). Sent when the names change or `secrets_version` changes.",
				Validators:          []validator.Map{names},
			},
			"secrets_version": schema.StringAttribute{Optional: true, MarkdownDescription: "Change it (e.g. a date or a counter) to send `secrets_wo` again. A managed secret deleted elsewhere shows as a change here."},
			"secret_names":    schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Every secret the service has (names only)."},
			"apply":           schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Apply each change to the running service (config.apply), and wait for it."},
			"config_version":  schema.Int64Attribute{Computed: true, MarkdownDescription: "The service's config version after the last change."},
		},
	}
}

func (r *serviceVariablesResource) params(m *serviceVariablesModel) client.Params {
	return client.Params{"env": m.Environment.ValueString(), "name": m.Service.ValueString()}
}

func mapOf(ctx context.Context, v types.Map, diags *diag.Diagnostics) map[string]string {
	out := map[string]string{}
	if v.IsNull() || v.IsUnknown() {
		return out
	}
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	return out
}

func keys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// change sends one PATCH (sealing secrets to the service's recipient), then applies it when asked.
func (r *serviceVariablesResource) change(ctx context.Context, m *serviceVariablesModel, plain, secrets map[string]string, del []string) (*envVarsAPI, error) {
	p := r.params(m)
	set := map[string]any{}
	for k, v := range plain {
		set[k] = map[string]any{"kind": "plain", "value": v}
	}
	if len(secrets) > 0 {
		var svc struct {
			Recipient *string `json:"recipient"`
		}
		if err := r.c.Get(ctx, client.OpGetService, p, nil, &svc); err != nil {
			return nil, err
		}
		if svc.Recipient == nil {
			return nil, errorf("the service's server has no public key yet: secrets cannot be sealed")
		}
		for k, v := range secrets {
			sealed, err := client.Seal(*svc.Recipient, v)
			if err != nil {
				return nil, err
			}
			set[k] = map[string]any{"kind": "secret", "sealed": sealed}
		}
	}
	var out envVarsAPI
	if len(set) == 0 && len(del) == 0 {
		return &out, r.c.Get(ctx, client.OpGetEnv, p, nil, &out)
	}
	body := map[string]any{}
	if len(set) > 0 {
		body["set"] = set
	}
	if len(del) > 0 {
		body["delete"] = del
	}
	if _, err := r.c.Do(ctx, client.OpSetEnv, p, body, &out); err != nil {
		return nil, err
	}
	if m.Apply.ValueBool() {
		var dep struct {
			Deployment *deployment `json:"deployment"`
		}
		appr, err := r.c.Do(ctx, client.OpApplyEnv, p, map[string]any{}, &dep)
		if err != nil {
			return &out, err
		}
		id := ""
		if dep.Deployment != nil {
			id = dep.Deployment.ID
		}
		if appr != nil {
			id = appr.DeploymentID
		}
		if _, err := waitDeployment(ctx, r.c, id); err != nil {
			return &out, err
		}
	}
	return &out, nil
}

func (r *serviceVariablesResource) refresh(ctx context.Context, m *serviceVariablesModel, own managed, importing bool) error {
	var out envVarsAPI
	if err := r.c.Get(ctx, client.OpGetEnv, r.params(m), nil, &out); err != nil {
		return err
	}
	plain := map[string]string{}
	var secretNames []string
	have := map[string]bool{}
	for _, v := range out.Vars {
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
		// A secret this configuration set was deleted elsewhere: a changed version makes the plan send it again.
		m.SecretsVersion = types.StringValue(str(m.SecretsVersion) + " (missing on the server: " + strings.Join(missing, ", ") + ")")
	}
	sn, _ := types.ListValueFrom(ctx, types.StringType, secretNames)
	m.SecretNames = sn
	m.ConfigVersion = types.Int64Value(out.Version)
	m.ID = types.StringValue(m.Environment.ValueString() + "/" + m.Service.ValueString())
	if m.Apply.IsNull() || m.Apply.IsUnknown() {
		m.Apply = types.BoolValue(true)
	}
	return nil
}

func (r *serviceVariablesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serviceVariablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	var wo types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secrets_wo"), &wo)...)
	plain := mapOf(ctx, m.Variables, &resp.Diagnostics)
	secrets := mapOf(ctx, wo, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.change(ctx, &m, plain, secrets, nil); err != nil {
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

func (r *serviceVariablesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serviceVariablesModel
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
		// Imported: every plain value is now owned; secrets cannot be imported (their values never leave the server).
		nb, _ := json.Marshal(managed{Plain: keys(mapOf(ctx, m.Variables, &resp.Diagnostics))})
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "managed", nb)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceVariablesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st serviceVariablesModel
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
	if _, err := r.change(ctx, &m, setPlain, sendSecrets, del); err != nil {
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

func (r *serviceVariablesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serviceVariablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	own := loadManaged(ctx, req.Private)
	del := append(append([]string{}, own.Plain...), own.Secrets...)
	if len(del) == 0 {
		return
	}
	if _, err := r.change(ctx, &m, nil, nil, del); err != nil && !client.IsNotFound(err) {
		fail(&resp.Diagnostics, "Deleting the variables failed", err)
	}
}

func (r *serviceVariablesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitID(req.ID, 2, "<environment>/<service>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
