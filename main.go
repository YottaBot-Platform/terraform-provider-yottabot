// Command terraform-provider-yottabot serves the YottaBot Terraform provider.
//
// The binary name is load-bearing: Terraform discovers a provider by the
// `terraform-provider-<type>` convention, so this must stay
// `terraform-provider-yottabot` for the `yottabot` provider type and the
// `YottaBot-Platform/yottabot` source address.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/provider"
)

// version is overwritten at release time via -ldflags. It reaches Terraform
// through provider.Metadata and shows up in `terraform version`, so a released
// binary reporting "dev" is a packaging bug, not a cosmetic one.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false,
		"run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		// Must match the source address customers write in
		// required_providers, and the dev_overrides key used locally.
		Address: "registry.terraform.io/YottaBot-Platform/yottabot",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
