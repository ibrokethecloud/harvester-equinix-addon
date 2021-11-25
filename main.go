package main

import (
	"flag"

	"github.com/harvester/harvester-equinix-addon/pkg/controllers"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/signals"
	"github.com/sirupsen/logrus"
)

var (
	KubeConfig string
)

func init() {
	flag.StringVar(&KubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.Parse()
}

func main() {

	//scheme := runtime.NewScheme()
	//v1alpha1.AddToScheme(scheme)

	ctx := signals.SetupSignalContext()
	kc := kubeconfig.GetNonInteractiveClientConfig(KubeConfig)

	// register controller
	err := controllers.Start(ctx, kc)
	if err != nil {
		logrus.Fatal(err)
	}

	<-ctx.Done()
}
