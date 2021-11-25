package main

import (
	"fmt"
	"os"

	"github.com/harvester/harvester-equinix-addon/pkg/crd"
	controllergen "github.com/rancher/wrangler/pkg/controller-gen"
	"github.com/rancher/wrangler/pkg/controller-gen/args"

	// Ensure gvk gets loaded in wrangler/pkg/gvk cache
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io/v1"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "crds" {
		fmt.Println("Writing CRDs to", os.Args[2])
		if err := crd.WriteFile(os.Args[2]); err != nil {
			panic(err)
		}
		return
	}

	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/harvester/harvester-equinix-addon/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"equinix.harvesterhci.io": {
				Types: []interface{}{
					"./pkg/apis/equinix.harvesterhci.io/v1",
				},
				GenerateTypes: true,
			},
		},
	})
}
