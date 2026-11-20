// Command docsgen writes the registry documentation (docs/) from the provider's schemas and examples/:
//
//	go run ./internal/docsgen
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"unrent.dev/terraform-provider-unrent/internal/provider"
)

func main() {
	ex, err := provider.Examples(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	docs, err := provider.RegistryDocs(ex)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.RemoveAll("docs"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for p, body := range docs {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d files\n", len(docs))
}
