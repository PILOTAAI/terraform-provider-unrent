package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// WP2.14: the models of the environments v2 resources against the API's shapes (the acceptance run in
// scripts/e2e-terraform.sh applies them against the real control plane).

func list(v ...string) types.List {
	l, _ := types.ListValueFrom(context.Background(), types.StringType, v)
	return l
}

func TestDeployRuleBodyAndBack(t *testing.T) {
	m := &deployRuleModel{Trigger: types.StringValue("push"), BranchesInclude: list("release/*"), BranchesExclude: list("release/old"), TagsInclude: types.ListNull(types.StringType),
		Semver: types.StringNull(), Prerelease: types.BoolNull(), PromoteFrom: types.StringNull(), OnForcePush: types.StringValue("hold"), Checks: types.StringNull()}
	b, _ := json.Marshal(ruleBody(m))
	if string(b) != `{"branches":{"exclude":["release/old"],"include":["release/*"]},"on_force_push":"hold","trigger":"push"}` {
		t.Fatalf("body %s", b)
	}
	// The API's answer comes back to the same model: no drift on a refresh.
	var a ruleAPI
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	back := ruleModel(&a, m)
	if !sameRule(back, m) || !back.Checks.IsNull() || !back.Semver.IsNull() {
		t.Fatalf("round trip %+v", back)
	}
	// A promote rule; a tag rule with SemVer.
	p := &deployRuleModel{Trigger: types.StringValue("promote"), PromoteFrom: types.StringValue("staging")}
	if b, _ := json.Marshal(ruleBody(p)); string(b) != `{"promote":{"from":"staging"},"trigger":"promote"}` {
		t.Fatalf("promote %s", b)
	}
	tg := &deployRuleModel{Trigger: types.StringValue("tag"), TagsInclude: list("v*"), Semver: types.StringValue(">=1 <2"), Prerelease: types.BoolValue(false)}
	if b, _ := json.Marshal(ruleBody(tg)); string(b) != `{"tags":{"include":["v*"],"prerelease":false,"semver":"\u003e=1 \u003c2"},"trigger":"tag"}` {
		t.Fatalf("tag %s", b)
	}
	// Without deploy_rule the service goes back to its branch and auto-deploy.
	sh := shorthandRule(&repositoryModel{Branch: types.StringValue("main"), AutoDeploy: types.BoolValue(false)})
	if b, _ := json.Marshal(ruleBody(sh)); string(b) != `{"branches":{"include":["main"]},"trigger":"manual"}` {
		t.Fatalf("shorthand %s", b)
	}
	if sameRule(m, nil) || !sameRule(nil, nil) {
		t.Fatal("sameRule")
	}
}

func TestProtectionBodyAndBack(t *testing.T) {
	m := &protectionModel{Environment: types.StringValue("production"), Branches: list("main"), Tags: types.ListNull(types.StringType), Semver: types.StringNull(), Images: types.StringValue("deny"),
		PromoteFrom: list("staging"), Checks: types.StringValue("none"), WaitTimerMinutes: types.Int64Value(5), PreventSelfApproval: types.BoolValue(true), BypassApprovals: types.Int64Value(2),
		Freeze: []freezeModel{{StartCron: types.StringValue("0 18 * * 5"), EndCron: types.StringValue("0 6 * * 1"), Timezone: types.StringValue("UTC")}}}
	b, _ := json.Marshal(protectionBody(context.Background(), m))
	want := `{"bypass_approvals":2,"checks":"none","freeze":[{"end_cron":"0 6 * * 1","start_cron":"0 18 * * 5","tz":"UTC"}],"images":"deny","prevent_self_approval":true,"promote_from":["staging"],"refs":{"branches":["main"]},"wait_timer_min":5}`
	if string(b) != want {
		t.Fatalf("body %s", b)
	}
	// What GET answers (with the repositories the API defaults) refreshes to the same model.
	var a protectionAPI
	if err := json.Unmarshal([]byte(`{"refs":{"branches":["main"]},"repos":["github:1"],"images":"deny","promote_from":["staging"],"checks":"none","wait_timer_min":5,"freeze":[{"start_cron":"0 18 * * 5","end_cron":"0 6 * * 1","tz":"UTC"}],"prevent_self_approval":true,"bypass_approvals":2,"max_depth":2048}`), &a); err != nil {
		t.Fatal(err)
	}
	got := *m
	setProtection(&got, "production", &a)
	g, _ := json.Marshal(protectionBody(context.Background(), &got))
	if string(g) != want || !got.Tags.IsNull() || !got.Semver.IsNull() {
		t.Fatalf("refresh %s", g)
	}
}

// A project secret is sealed to every environment that has a key, and only to those; the plaintext is not sent.
func TestLayerSecretsSealedPerEnvironment(t *testing.T) {
	staging, _ := age.GenerateX25519Identity()
	rc := staging.Recipient().String()
	l := &layerAPI{}
	l.Environments = append(l.Environments, struct {
		Env       string  `json:"env"`
		Recipient *string `json:"recipient"`
	}{Env: "staging", Recipient: &rc}, struct {
		Env       string  `json:"env"`
		Recipient *string `json:"recipient"`
	}{Env: "production"})
	b, err := sealedBody("project", l, "dsn-canary")
	if err != nil {
		t.Fatal(err)
	}
	per := b["sealed_for_env"].(map[string]string)
	if len(per) != 1 || per["staging"] == "" || strings.Contains(per["staging"], "dsn-canary") {
		t.Fatalf("sealed %v", per)
	}
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader([]byte(per["staging"]))), staging)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := io.ReadAll(r); string(v) != "dsn-canary" {
		t.Fatalf("opened %q", v)
	}
	if _, err := sealedBody("environment", &layerAPI{}, "x"); err == nil {
		t.Fatal("an environment without a key must refuse to seal")
	}
	if _, err := sealedBody("project", &layerAPI{}, "x"); err == nil {
		t.Fatal("a project without keys must refuse to seal")
	}
}
