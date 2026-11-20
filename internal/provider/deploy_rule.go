package provider

// WP2.14 (docs/terraform.md §3, docs/environments-v2.md §4.1): unrent_service.repository.deploy_rule — which refs
// deploy the service in its environment: a push to branches (globs, excludes first), tags (globs, a SemVer range,
// prereleases), manual deploys only, or promotion from another environment. `branch` + `auto_deploy` stay its
// shorthand: without deploy_rule the service follows them. In a protected environment the rule is an admin's (and
// must stay inside the protection); a rule an unrent.toml sets is refused (managed_by_file).

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"unrent.dev/terraform-provider-unrent/internal/client"
)

type deployRuleModel struct {
	Trigger         types.String `tfsdk:"trigger"`
	BranchesInclude types.List   `tfsdk:"branches_include"`
	BranchesExclude types.List   `tfsdk:"branches_exclude"`
	TagsInclude     types.List   `tfsdk:"tags_include"`
	Semver          types.String `tfsdk:"semver"`
	Prerelease      types.Bool   `tfsdk:"prerelease"`
	PromoteFrom     types.String `tfsdk:"promote_from"`
	OnForcePush     types.String `tfsdk:"on_force_push"`
	Checks          types.String `tfsdk:"checks"`
}

func deployRuleSchema() schema.SingleNestedAttribute {
	globs := []validator.List{listvalidator.SizeAtMost(10), listvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexpMust(`^[A-Za-z0-9._/*?-]{1,100}$`), "a glob: * any run without /, ** any run, ? one character"))}
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Which refs deploy the service here (docs/environments-v2.md §4.1). Without it the service follows `branch` and `auto_deploy`. In a protected environment only an admin changes it, inside the protection.",
		Attributes: map[string]schema.Attribute{
			"trigger":          schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("push", "tag", "manual", "promote")}},
			"branches_include": schema.ListAttribute{Optional: true, ElementType: types.StringType, Validators: globs, MarkdownDescription: "push (and manual): branches, e.g. `[\"main\"]` or `[\"release/*\"]`."},
			"branches_exclude": schema.ListAttribute{Optional: true, ElementType: types.StringType, Validators: globs},
			"tags_include":     schema.ListAttribute{Optional: true, ElementType: types.StringType, Validators: globs, MarkdownDescription: "tag: tags, e.g. `[\"v*\"]`."},
			"semver":           schema.StringAttribute{Optional: true, MarkdownDescription: "tag: a SemVer range, e.g. `>=1.4.0 <2`."},
			"prerelease":       schema.BoolAttribute{Optional: true},
			"promote_from":     schema.StringAttribute{Optional: true, Validators: []validator.String{envValidator()}, MarkdownDescription: "promote: the environment whose release this one deploys (never builds)."},
			"on_force_push":    schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("deploy", "hold")}},
			"checks":           schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("none", "required")}},
		},
	}
}

func ruleOf(r *repositoryModel) *deployRuleModel {
	if r == nil {
		return nil
	}
	return r.DeployRule
}

// ruleBody is the API's rule from the model.
func ruleBody(m *deployRuleModel) map[string]any {
	ctx := context.Background()
	b := map[string]any{"trigger": m.Trigger.ValueString()}
	if inc := strList(ctx, m.BranchesInclude); len(inc) > 0 {
		br := map[string]any{"include": inc}
		if exc := strList(ctx, m.BranchesExclude); len(exc) > 0 {
			br["exclude"] = exc
		}
		b["branches"] = br
	}
	if inc := strList(ctx, m.TagsInclude); len(inc) > 0 {
		t := map[string]any{"include": inc}
		if s := str(m.Semver); s != "" {
			t["semver"] = s
		}
		if !m.Prerelease.IsNull() && !m.Prerelease.IsUnknown() {
			t["prerelease"] = m.Prerelease.ValueBool()
		}
		b["tags"] = t
	}
	if f := str(m.PromoteFrom); f != "" {
		b["promote"] = map[string]any{"from": f}
	}
	if v := str(m.OnForcePush); v != "" {
		b["on_force_push"] = v
	}
	if v := str(m.Checks); v != "" {
		b["checks"] = v
	}
	return b
}

// ruleAPI is the rule as GET /services/{env}/{name}/deploy-rule answers it.
type ruleAPI struct {
	Trigger  string `json:"trigger"`
	Branches *struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"branches"`
	Tags *struct {
		Include    []string `json:"include"`
		Semver     string   `json:"semver"`
		Prerelease *bool    `json:"prerelease"`
	} `json:"tags"`
	Promote *struct {
		From string `json:"from"`
	} `json:"promote"`
	OnForcePush string `json:"on_force_push"`
	Checks      string `json:"checks"`
}

// ruleModel is the model of an API rule; prior keeps null where the configuration left an optional attribute out.
func ruleModel(a *ruleAPI, prior *deployRuleModel) *deployRuleModel {
	if prior == nil {
		prior = &deployRuleModel{BranchesInclude: types.ListNull(types.StringType), BranchesExclude: types.ListNull(types.StringType), TagsInclude: types.ListNull(types.StringType)}
	}
	sv := func(v string, p types.String) types.String {
		if v == "" && p.IsNull() {
			return types.StringNull()
		}
		return types.StringValue(v)
	}
	m := &deployRuleModel{Trigger: types.StringValue(a.Trigger), BranchesInclude: types.ListNull(types.StringType), BranchesExclude: listOrNull(nil, prior.BranchesExclude), TagsInclude: types.ListNull(types.StringType),
		Semver: sv("", prior.Semver), Prerelease: types.BoolNull(), PromoteFrom: types.StringNull(), OnForcePush: sv(a.OnForcePush, prior.OnForcePush), Checks: sv(a.Checks, prior.Checks)}
	if a.Branches != nil {
		m.BranchesInclude = listOrNull(a.Branches.Include, prior.BranchesInclude)
		m.BranchesExclude = listOrNull(a.Branches.Exclude, prior.BranchesExclude)
	}
	if a.Tags != nil {
		m.TagsInclude = listOrNull(a.Tags.Include, prior.TagsInclude)
		m.Semver = sv(a.Tags.Semver, prior.Semver)
		if a.Tags.Prerelease != nil {
			m.Prerelease = types.BoolValue(*a.Tags.Prerelease)
		}
	}
	if a.Promote != nil {
		m.PromoteFrom = types.StringValue(a.Promote.From)
	}
	return m
}

func sameRule(a, b *deployRuleModel) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	x, _ := json.Marshal(ruleBody(a))
	y, _ := json.Marshal(ruleBody(b))
	return string(x) == string(y)
}

// shorthandRule: branch + auto_deploy, the rule a service has without a deploy_rule.
func shorthandRule(r *repositoryModel) *deployRuleModel {
	trigger := "push"
	if !r.AutoDeploy.IsNull() && !r.AutoDeploy.IsUnknown() && !r.AutoDeploy.ValueBool() {
		trigger = "manual"
	}
	inc := types.ListNull(types.StringType)
	if b := str(r.Branch); b != "" {
		inc = listOrNull([]string{b}, types.ListNull(types.StringType))
	}
	return &deployRuleModel{Trigger: types.StringValue(trigger), BranchesInclude: inc}
}

func putDeployRule(ctx context.Context, c *client.Client, env, name string, m *deployRuleModel) error {
	_, err := c.Do(ctx, client.OpSetDeployRule, client.Params{"env": env, "name": name}, map[string]any{"rule": ruleBody(m)}, nil)
	return err
}

// refreshRule reads the rule when the configuration manages one (drift shows as a plan) or the service was just
// imported with a stored rule; otherwise deploy_rule stays unset (the console's rules are left alone).
func (r *serviceResource) refreshRule(ctx context.Context, m *serviceModel, prior *deployRuleModel, importing bool, diags *diag.Diagnostics) {
	if m.Repository == nil || (prior == nil && !importing) {
		if m.Repository != nil {
			m.Repository.DeployRule = prior
		}
		return
	}
	var out struct {
		Rule   ruleAPI `json:"rule"`
		Stored bool    `json:"stored"`
	}
	if err := r.c.Get(ctx, client.OpGetDeployRule, client.Params{"env": m.Environment.ValueString(), "name": m.Name.ValueString()}, nil, &out); err != nil {
		diags.AddWarning("Reading the deploy rule failed", err.Error())
		m.Repository.DeployRule = prior
		return
	}
	if prior == nil && !out.Stored {
		return // imported, following its branch: no deploy_rule
	}
	m.Repository.DeployRule = ruleModel(&out.Rule, prior)
}
