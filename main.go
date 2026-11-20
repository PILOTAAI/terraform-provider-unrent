// terraform-provider-unrent: the Unrent provider for OpenTofu and Terraform (docs/terraform.md in the Unrent
// repository). Plugin protocol 6, through the Terraform Plugin Framework.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"unrent.dev/terraform-provider-unrent/internal/provider"
)

// version is set by the release build (goreleaser: -X main.version).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.opentofu.org/pilotaai/unrent",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
