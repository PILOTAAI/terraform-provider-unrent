package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// P21: the sandbox settings' body and refresh, and the data source's rows, against the API's shapes (the acceptance
// run in scripts/e2e-terraform.sh applies them against the real control plane).

func TestSandboxSettingsBodyAndBack(t *testing.T) {
	ctx := context.Background()
	ids, _ := types.SetValueFrom(ctx, types.StringType, []string{"agt_bbbbbbbbbbbbbbbb", "agt_aaaaaaaaaaaaaaaa"})
	m := &sandboxSettingsModel{ServerIDs: ids, MaxOpen: types.Int64Value(10), PerProject: types.Int64Value(3), TTLHours: types.Int64Value(4), MaxHours: types.Int64Value(24)}
	b, err := sandboxSettingsBody(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(b)
	// The whole settings, the servers sorted (the API answers them sorted: no drift from the order in the file).
	if string(j) != `{"max_hours":24,"max_open":10,"per_project":3,"servers":["agt_aaaaaaaaaaaaaaaa","agt_bbbbbbbbbbbbbbbb"],"ttl_hours":4}` {
		t.Fatalf("body %s", j)
	}
	// An empty set is an empty list (no sandbox server), never null.
	empty, _ := types.SetValueFrom(ctx, types.StringType, []string{})
	b, _ = sandboxSettingsBody(ctx, &sandboxSettingsModel{ServerIDs: empty, MaxOpen: types.Int64Value(0), PerProject: types.Int64Value(3), TTLHours: types.Int64Value(4), MaxHours: types.Int64Value(72)})
	if j, _ := json.Marshal(b["servers"]); string(j) != `[]` {
		t.Fatalf("empty servers %s", j)
	}
	// What GET answers refreshes to the same model.
	var a sandboxSettingsAPI
	if err := json.Unmarshal([]byte(`{"servers":["agt_aaaaaaaaaaaaaaaa","agt_bbbbbbbbbbbbbbbb"],"max_open":10,"per_project":3,"ttl_hours":4,"max_hours":24}`), &a); err != nil {
		t.Fatal(err)
	}
	var got sandboxSettingsModel
	setSandboxSettings(ctx, &got, "org_0123456789abcdef", &a)
	if !got.ServerIDs.Equal(ids) || got.MaxOpen.ValueInt64() != 10 || got.MaxHours.ValueInt64() != 24 || got.ID.ValueString() != "org_0123456789abcdef/sandbox-settings" {
		t.Fatalf("refresh %+v", got)
	}
	// No server at all comes back as an empty set, not null (a config of [] does not drift).
	setSandboxSettings(ctx, &got, "org_0123456789abcdef", &sandboxSettingsAPI{MaxOpen: 0, PerProject: 3, TTLHours: 4, MaxHours: 72})
	if got.ServerIDs.IsNull() || len(got.ServerIDs.Elements()) != 0 {
		t.Fatalf("no servers %v", got.ServerIDs)
	}
}

func TestSandboxRows(t *testing.T) {
	var out struct {
		Sandboxes []sandboxAPI       `json:"sandboxes"`
		Servers   []sandboxServerAPI `json:"servers"`
	}
	raw := `{"sandboxes":[
	  {"id":"sbx_00000000000000000001","env":"b-sbx-a-0001","project":"prj_shop","task":"A","source":"staging","server":"agt_aaaaaaaaaaaaaaaa","state":"open","status":"live","client":"Claude Code","expires_at":1763304400000,
	   "services":[{"name":"web","url":"https://web.example","live":true},{"name":"worker","url":null,"live":false}],"database":null},
	  {"id":"sbx_00000000000000000002","env":"b-sbx-b-0002","project":"prj_default","task":"B","source":"staging","server":"agt_aaaaaaaaaaaaaaaa","state":"preparing","status":"preparing","client":null,"expires_at":null,"services":[]}],
	 "servers":[{"id":"agt_aaaaaaaaaaaaaaaa","name":null,"problem":"it runs production","budget_mb":null,"planned_mb":1280,"free_mb":null,"open":2}]}`
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	rows := sandboxRows(out.Sandboxes, "prj_shop")
	if len(rows) != 1 || rows[0].ID.ValueString() != "sbx_00000000000000000001" || rows[0].ExpiresAt.ValueString() != "2025-11-16T14:46:40Z" || rows[0].Client.ValueString() != "Claude Code" {
		t.Fatalf("rows %+v", rows)
	}
	if len(rows[0].Services) != 2 || !rows[0].Services[1].URL.IsNull() || rows[0].Services[1].Live.ValueBool() {
		t.Fatalf("services %+v", rows[0].Services)
	}
	all := sandboxRows(out.Sandboxes, "")
	if len(all) != 2 || !all[1].ExpiresAt.IsNull() || !all[1].Client.IsNull() || all[1].Services == nil {
		t.Fatalf("all %+v", all)
	}
	srv := sandboxServerRows(out.Servers)
	if len(srv) != 1 || !srv[0].BudgetMB.IsNull() || !srv[0].Name.IsNull() || srv[0].Problem.ValueString() != "it runs production" || srv[0].Open.ValueInt64() != 2 {
		t.Fatalf("servers %+v", srv)
	}
}
