package provider

import (
	"context"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type domainResource struct{ base }

func newDomainResource() resource.Resource { return &domainResource{} }

type domainModel struct {
	ID          types.String `tfsdk:"id"`
	Host        types.String `tfsdk:"host"`
	Environment types.String `tfsdk:"environment"`
	Service     types.String `tfsdk:"service"`
	WWW         types.Bool   `tfsdk:"www"`
	State       types.String `tfsdk:"state"`
	Records     types.List   `tfsdk:"records"`
}

type domainAPI struct {
	Host    string  `json:"host"`
	Env     string  `json:"env"`
	Service string  `json:"service"`
	State   string  `json:"state"`
	Pair    *string `json:"pair"`
	Records []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"records"`
}

var recordType = types.ObjectType{AttrTypes: map[string]attr.Type{"type": types.StringType, "name": types.StringType, "value": types.StringType}}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A custom domain routed to a service (`*.<zone>` for a wildcard; `www = true` also routes `www.<apex>`). It serves with TLS once its DNS verifies: create the `records` (e.g. with your DNS provider's Terraform resources), or let a DNS connection do it.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"host":        schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(hostRe, "a lower-case domain name such as app.example.com, or *.example.com")}},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{envValidator()}},
			"service":     schema.StringAttribute{Required: true, PlanModifiers: replace, Validators: []validator.String{nameValidator()}},
			"www":         schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()}},
			"state":       schema.StringAttribute{Computed: true, MarkdownDescription: "pending, verified, active, …"},
			"records":     schema.ListAttribute{Computed: true, ElementType: recordType, MarkdownDescription: "DNS records to create: the ownership TXT record, then A/AAAA (or a CNAME alternative)."},
		},
	}
}

func (r *domainResource) find(ctx context.Context, env, host string) (*domainAPI, error) {
	var out struct {
		Domains []domainAPI `json:"domains"`
	}
	if err := r.c.Get(ctx, client.OpListDomains, nil, url.Values{"env": {env}}, &out); err != nil {
		return nil, err
	}
	for i := range out.Domains {
		if out.Domains[i].Host == host {
			return &out.Domains[i], nil
		}
	}
	return nil, nil
}

func (r *domainResource) set(ctx context.Context, m *domainModel, d *domainAPI, all []domainAPI) {
	m.ID = types.StringValue(d.Host)
	m.Host = types.StringValue(d.Host)
	m.Environment = types.StringValue(d.Env)
	m.Service = types.StringValue(d.Service)
	m.State = types.StringValue(d.State)
	if m.WWW.IsNull() || m.WWW.IsUnknown() {
		m.WWW = types.BoolValue(d.Pair != nil && *d.Pair == "www."+d.Host)
	}
	var recs []attr.Value
	seen := map[string]bool{}
	for _, x := range append([]domainAPI{*d}, all...) {
		for _, rec := range x.Records {
			k := rec.Type + " " + rec.Name + " " + rec.Value
			if seen[k] {
				continue
			}
			seen[k] = true
			recs = append(recs, types.ObjectValueMust(recordType.AttrTypes, map[string]attr.Value{"type": types.StringValue(rec.Type), "name": types.StringValue(rec.Name), "value": types.StringValue(rec.Value)}))
		}
	}
	m.Records = types.ListValueMust(recordType, recs)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Domains []domainAPI `json:"domains"`
	}
	body := map[string]any{"host": m.Host.ValueString(), "env": m.Environment.ValueString(), "service": m.Service.ValueString(), "www": m.WWW.ValueBool()}
	if _, err := r.c.Do(ctx, client.OpAddDomain, nil, body, &out); err != nil {
		fail(&resp.Diagnostics, "Adding the domain failed", err)
		return
	}
	if len(out.Domains) == 0 {
		resp.Diagnostics.AddError("Adding the domain failed", "the API answered without the domain")
		return
	}
	// With www the API adds the apex first, then www.<apex>; the resource is the apex.
	r.set(ctx, &m, &out.Domains[0], out.Domains[1:])
	m.WWW = types.BoolValue(len(out.Domains) > 1)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := str(m.Environment)
	var all struct {
		Domains []domainAPI `json:"domains"`
	}
	q := url.Values{}
	if env != "" {
		q.Set("env", env)
	}
	if err := r.c.Get(ctx, client.OpListDomains, nil, q, &all); err != nil {
		fail(&resp.Diagnostics, "Reading domains failed", err)
		return
	}
	var d *domainAPI
	var pair []domainAPI
	for i := range all.Domains {
		if all.Domains[i].Host == m.Host.ValueString() {
			d = &all.Domains[i]
		}
	}
	if d == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	for _, x := range all.Domains {
		if d.Pair != nil && x.Host == *d.Pair {
			pair = append(pair, x)
		}
	}
	r.set(ctx, &m, d, pair)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hosts := []string{m.Host.ValueString()}
	if m.WWW.ValueBool() && !strings.HasPrefix(m.Host.ValueString(), "*.") {
		hosts = append(hosts, "www."+m.Host.ValueString())
	}
	for _, h := range hosts {
		var out struct {
			JobID *string `json:"job_id"`
		}
		appr, err := r.c.Do(ctx, client.OpRemoveDomain, client.Params{"host": h}, nil, &out)
		if client.IsNotFound(err) {
			continue
		}
		if err != nil {
			fail(&resp.Diagnostics, "Removing the domain failed", err)
			return
		}
		var ids []string
		if out.JobID != nil {
			ids = []string{*out.JobID}
		}
		if appr != nil {
			ids = appr.JobIDs()
		}
		if err := r.c.WaitJobs(ctx, ids); err != nil {
			fail(&resp.Diagnostics, "The server did not stop routing the domain", err)
			return
		}
	}
}

func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !hostRe.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import id", "expected a domain name")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("host"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
