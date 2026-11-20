package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// RegistryDocs renders the registry documentation (docs/index.md, docs/resources/*.md, docs/data-sources/*.md, the
// layout both registries read) from the provider's own schemas, so the docs cannot drift from the code
// (docs_test.go fails when the committed files differ). examples are the HCL snippets by file path, when present.
func RegistryDocs(examples map[string]string) (map[string]string, error) {
	srv, err := providerserver.NewProtocol6WithError(New("docs")())()
	if err != nil {
		return nil, err
	}
	resp, err := srv.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var idx strings.Builder
	idx.WriteString("---\npage_title: \"Unrent Provider\"\ndescription: |-\n  Manage an Unrent organisation through its public API.\n---\n\n# Unrent Provider\n\n")
	idx.WriteString(resp.Provider.Block.Description + "\n\n")
	idx.WriteString("Every change needs a token with the `manage` scope (deployments and variable applies also `deploy`). Jobs that need a person's passkey — deleting a service, dropping a database, storage targets, environments with an approval floor — wait for the approval (`approval_timeout`) or fail with its page; the provider never approves anything. Secret values are write-only attributes (Terraform and OpenTofu 1.11 or later), sealed to the server's public key before they are sent: they are never in the plan or the state.\n\n")
	if ex, ok := examples["examples/provider/provider.tf"]; ok {
		idx.WriteString("## Example Usage\n\n```terraform\n" + strings.TrimSpace(ex) + "\n```\n\n")
	}
	idx.WriteString("## Schema\n\n")
	writeBlock(&idx, resp.Provider.Block, "")
	out["docs/index.md"] = idx.String()
	for kind, set := range map[string]map[string]*tfprotov6.Schema{"resources": resp.ResourceSchemas, "data-sources": resp.DataSourceSchemas} {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			short := strings.TrimPrefix(n, "unrent_")
			label := "Resource"
			if kind == "data-sources" {
				label = "Data Source"
			}
			var b strings.Builder
			fmt.Fprintf(&b, "---\npage_title: \"%s %s - unrent\"\nsubcategory: \"\"\ndescription: |-\n  %s\n---\n\n# %s (%s)\n\n%s\n\n", n, label, firstSentence(set[n].Block.Description), n, label, set[n].Block.Description)
			if ex, ok := examples[fmt.Sprintf("examples/%s/%s/%s.tf", kind, n, kind[:len(kind)-1])]; ok {
				b.WriteString("## Example Usage\n\n```terraform\n" + strings.TrimSpace(ex) + "\n```\n\n")
			}
			b.WriteString("## Schema\n\n")
			writeBlock(&b, set[n].Block, "")
			if kind == "resources" && hasImport(n) {
				fmt.Fprintf(&b, "## Import\n\n```shell\ntofu import %s.example %s\n```\n", n, importFormat[n])
			}
			out[fmt.Sprintf("docs/%s/%s.md", kind, short)] = b.String()
		}
	}
	return out, nil
}

var importFormat = map[string]string{
	"unrent_project":              "prj_…",
	"unrent_project_role":         "<project_id>/<user_id>",
	"unrent_environment":          "<name>",
	"unrent_service":              "<environment>/<name>",
	"unrent_service_variables":    "<environment>/<service>",
	"unrent_cron":                 "<environment>/<service>/<name>",
	"unrent_database":             "<environment>/<name>",
	"unrent_database_link":        "<environment>/<database>/<service>",
	"unrent_domain":               "<host>",
	"unrent_storage_target":       "<server_id>/<target_id>",
	"unrent_notification_channel": "chn_…",
	"unrent_status_page":          "status-page",
	"unrent_status_component":     "cmp_…",
}

func hasImport(n string) bool { _, ok := importFormat[n]; return ok }

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func writeBlock(b *strings.Builder, blk *tfprotov6.SchemaBlock, prefix string) {
	groups := map[string][]*tfprotov6.SchemaAttribute{}
	for _, a := range blk.Attributes {
		g := "Read-Only"
		switch {
		case a.Required:
			g = "Required"
		case a.Optional:
			g = "Optional"
		}
		groups[g] = append(groups[g], a)
	}
	for _, g := range []string{"Required", "Optional", "Read-Only"} {
		attrs := groups[g]
		if len(attrs) == 0 {
			continue
		}
		sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name < attrs[j].Name })
		fmt.Fprintf(b, "### %s%s\n\n", prefix, g)
		for _, a := range attrs {
			var flags []string
			if a.Sensitive {
				flags = append(flags, "Sensitive")
			}
			if a.WriteOnly {
				flags = append(flags, "Write-only: never stored in the plan or state")
			}
			typ := "Attributes"
			if a.Type != nil {
				typ = typeName(a.Type.String())
			}
			f := ""
			if len(flags) > 0 {
				f = ", " + strings.Join(flags, ", ")
			}
			fmt.Fprintf(b, "- `%s` (%s%s) %s\n", a.Name, typ, f, strings.TrimSpace(a.Description))
			if a.NestedType != nil {
				for _, na := range a.NestedType.Attributes {
					fmt.Fprintf(b, "  - `%s.%s` %s\n", a.Name, na.Name, strings.TrimSpace(na.Description))
				}
			}
		}
		b.WriteString("\n")
	}
	for _, nb := range blk.BlockTypes {
		fmt.Fprintf(b, "### Block `%s`\n\n%s\n\n", nb.TypeName, nb.Block.Description)
		writeBlock(b, nb.Block, nb.TypeName+" ")
	}
}

// Examples reads examples/**/*.tf under root, by path relative to root.
func Examples(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tf") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return out, err
}

func typeName(t string) string {
	t = strings.TrimPrefix(t, "tftypes.")
	return strings.ReplaceAll(t, "tftypes.", "")
}
