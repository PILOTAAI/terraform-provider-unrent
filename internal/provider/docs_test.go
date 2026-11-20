package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed registry docs are the ones the schemas generate (go run ./internal/docsgen regenerates them).
func TestRegistryDocsAreCurrent(t *testing.T) {
	ex, err := Examples("../..")
	if err != nil {
		t.Fatal(err)
	}
	want, err := RegistryDocs(ex)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	_ = filepath.WalkDir("../../docs", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			got[strings.TrimPrefix(filepath.ToSlash(p), "../../")] = string(b)
		}
		return nil
	})
	for p, body := range want {
		if got[p] != body {
			t.Errorf("%s is stale: go run ./internal/docsgen", p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s is not generated any more: go run ./internal/docsgen", p)
		}
	}
	if !strings.Contains(want["docs/resources/service_variables.md"], "Write-only") {
		t.Error("write-only attributes must say so in the docs")
	}
}
