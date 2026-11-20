package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// environments-v3 (docs/environments-v3.md §2.5): no environment name is reserved, the tier sets the floor unless it
// is configured, a display label, and unrent_environments reads what the organisation already has.

func TestEnvironmentCreateBodyAnyNameTierDefaults(t *testing.T) {
	m := &environmentModel{
		Name: types.StringValue("production"), ProjectID: types.StringValue("prj_default"), Approvals: types.Int64Unknown(),
		Tier: types.StringValue("production"), Label: types.StringValue("Live"), Colour: types.StringNull(), Description: types.StringNull(),
		CloneFrom: types.StringNull(), TemplateID: types.StringNull(),
	}
	// production is an ordinary name now: the body is the request, with no floor (the tier's default applies).
	b, _ := json.Marshal(createBody(m))
	if string(b) != `{"label":"Live","name":"production","project":"prj_default","tier":"production"}` {
		t.Fatalf("body %s", b)
	}
	// A configured floor is sent as configured.
	m.Name, m.Tier, m.Approvals, m.Label = types.StringValue("staging"), types.StringNull(), types.Int64Value(0), types.StringNull()
	if b, _ := json.Marshal(createBody(m)); string(b) != `{"approvals":0,"name":"staging","project":"prj_default"}` {
		t.Fatalf("staging %s", b)
	}
}

func TestEnvironmentLabelRoundTrip(t *testing.T) {
	var e environmentAPI
	if err := json.Unmarshal([]byte(`{"name":"eu","kind":"production","tier":"production","approvals":1,"project":"prj_default","network":"unrent-eu","label":"EU production"}`), &e); err != nil {
		t.Fatal(err)
	}
	m := &environmentModel{Label: types.StringValue("EU production"), Description: types.StringNull()}
	(&environmentResource{}).set(m, &e)
	if m.Label.ValueString() != "EU production" || len(meta(m, &e)) != 0 {
		t.Fatalf("label %v meta %v", m.Label, meta(m, &e))
	}
	m.Label = types.StringValue("Live")
	if got := meta(m, &e); got["label"] != "Live" {
		t.Fatalf("change %v", got)
	}
	m.Label = types.StringNull()
	if got, ok := meta(m, &e)["label"]; !ok || got != nil {
		t.Fatalf("clear %v", got)
	}
}

func TestEnvironmentsDataSourceFiltersByTier(t *testing.T) {
	var out struct {
		Environments []environmentAPI `json:"environments"`
	}
	_ = json.Unmarshal([]byte(`{"environments":[
	  {"name":"live","tier":"production","project":"prj_default","approvals":1,"network":"unrent-live","label":"Live"},
	  {"name":"production","tier":"development","project":"prj_default","approvals":0,"network":"unrent-production"}]}`), &out)
	all := environmentRows(out.Environments, "")
	prod := environmentRows(out.Environments, "production")
	if len(all) != 2 || len(prod) != 1 || prod[0].Name.ValueString() != "live" || prod[0].Label.ValueString() != "Live" {
		t.Fatalf("all %v prod %v", all, prod)
	}
	if !all[1].Label.IsNull() {
		t.Fatalf("no label: %v", all[1].Label)
	}
	if len(environmentRows(nil, "staging")) != 0 {
		t.Fatal("none")
	}
}

func TestEnvironmentSchemaReservesNoName(t *testing.T) {
	resp := schemas(t)
	s := resp.ResourceSchemas["unrent_environment"]
	if s == nil {
		t.Fatal("unrent_environment missing")
	}
	if strings.Contains(s.Block.Description, "always exist") {
		t.Fatalf("description still reserves names: %s", s.Block.Description)
	}
	found := false
	for _, a := range s.Block.Attributes {
		if a.Name == "label" && a.Optional {
			found = true
		}
	}
	if !found {
		t.Fatal("label attribute missing")
	}
}
