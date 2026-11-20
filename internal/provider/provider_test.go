package provider

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func schemas(t *testing.T) *tfprotov6.GetProviderSchemaResponse {
	t.Helper()
	srv, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s %s", d.Severity, d.Summary, d.Detail)
	}
	return resp
}

// The framework validates every schema here (e.g. a write-only attribute cannot be computed).
func TestEveryResourceAndDataSourceTheRoadmapLists(t *testing.T) {
	resp := schemas(t)
	var res, ds []string
	for k := range resp.ResourceSchemas {
		res = append(res, k)
	}
	for k := range resp.DataSourceSchemas {
		ds = append(ds, k)
	}
	sort.Strings(res)
	sort.Strings(ds)
	want := "unrent_branch_environments unrent_cron unrent_database unrent_database_link unrent_domain unrent_environment unrent_environment_protection unrent_environment_template unrent_notification_channel unrent_project unrent_project_role unrent_sandbox_settings unrent_service unrent_service_variables unrent_status_component unrent_status_page unrent_storage_target unrent_variables"
	if strings.Join(res, " ") != want {
		t.Fatalf("resources: %v", res)
	}
	if strings.Join(ds, " ") != "unrent_environments unrent_members unrent_project unrent_releases unrent_sandboxes unrent_servers" {
		t.Fatalf("data sources: %v", ds)
	}
}

// Secret material is only ever in write-only attributes (never in the plan or the state), all named *_wo and
// sensitive; nothing named like a secret is a normal attribute.
func TestSecretsAreWriteOnly(t *testing.T) {
	resp := schemas(t)
	found := 0
	for name, s := range resp.ResourceSchemas {
		var walk func(prefix string, attrs []*tfprotov6.SchemaAttribute)
		walk = func(prefix string, attrs []*tfprotov6.SchemaAttribute) {
			for _, a := range attrs {
				n := prefix + a.Name
				if strings.HasSuffix(a.Name, "_wo") {
					found++
					if !a.WriteOnly || !a.Sensitive || a.Computed {
						t.Errorf("%s.%s must be write-only, sensitive and not computed", name, n)
					}
				} else if a.WriteOnly {
					t.Errorf("%s.%s is write-only but not named *_wo", name, n)
				}
				for _, bad := range []string{"password", "secret_value", "token", "access_key", "private_key"} {
					if strings.Contains(a.Name, bad) && !a.WriteOnly {
						t.Errorf("%s.%s looks like a secret but is stored in the state", name, n)
					}
				}
				if a.NestedType != nil {
					walk(n+".", a.NestedType.Attributes)
				}
			}
		}
		walk("", s.Block.Attributes)
	}
	if found != 6 {
		t.Fatalf("expected 6 write-only attributes (service token, service variables' secrets, two storage keys, channel settings, WP2.14 layered variables' secrets), found %d", found)
	}
	if !resp.Provider.Block.Attributes[indexOf(resp.Provider.Block.Attributes, "token")].Sensitive {
		t.Fatal("the provider token must be sensitive")
	}
}

func indexOf(attrs []*tfprotov6.SchemaAttribute, name string) int {
	for i, a := range attrs {
		if a.Name == name {
			return i
		}
	}
	return -1
}

// Identifiers are validated at plan time with the API's own patterns.
func TestPlanTimeValidators(t *testing.T) {
	for re, cases := range map[string]map[string]bool{
		envRe.String():      {"production": true, "pr-12": true, "Prod": false, "p": false, "a_b": false},
		nameRe.String():     {"web": true, "w": false, "web_1": false},
		dbRe.String():       {"shop": true, "shop_eu": true, "Shop": false, "1db": false},
		hostRe.String():     {"app.example.com": true, "*.example.com": true, "App.example.com": false, "example": false},
		cronExprRe.String(): {"0 3 * * *": true, "@daily": false, "0 3 * *": false},
		variableRe.String(): {"DATABASE_URL": true, "lower": false, "1A": false},
	} {
		r := regexpMust(re)
		for v, ok := range cases {
			if r.MatchString(v) != ok {
				t.Errorf("%s on %q: want %v", re, v, ok)
			}
		}
	}
}
