// Command gen writes internal/client/ops_gen.go from the Unrent API's OpenAPI document (apps/control/openapi/v1.json,
// generated in turn from the Worker's route table): one Operation per operationId with its method, path template,
// scope and whether it can answer 202 approval_required. The provider calls the API only through these, so an
// operation the API drops or renames breaks the build, and ops_gen_test.go fails when the file is stale.
//
//	go run ./internal/client/gen ../apps/control/openapi/v1.json > internal/client/ops_gen.go
package main

import (
	"fmt"
	"os"

	"unrent.dev/terraform-provider-unrent/internal/client/opsgen"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <openapi.json>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := opsgen.Generate(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
