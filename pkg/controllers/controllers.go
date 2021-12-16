package controllers

import (
	"context"
	"time"

	"github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	"k8s.io/client-go/util/workqueue"

	instanceController "github.com/harvester/harvester-equinix-addon/pkg/controllers/instance"
	instancePoolController "github.com/harvester/harvester-equinix-addon/pkg/controllers/instancepool"
	"github.com/harvester/harvester-equinix-addon/pkg/crd"
	instance "github.com/harvester/harvester-equinix-addon/pkg/generated/controllers/equinix.harvesterhci.io"
	"github.com/rancher/wrangler/pkg/start"
	"k8s.io/client-go/tools/clientcmd"
)

func Start(ctx context.Context, cfg clientcmd.ClientConfig) error {
	clientConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	if err := crd.Create(ctx, clientConfig); err != nil {
		return err
	}

	return Register(ctx, cfg)
}

func Register(ctx context.Context, cfg clientcmd.ClientConfig) error {
	restConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	rateLimit := workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 5*time.Minute)
	workqueue.DefaultControllerRateLimiter()
	clientFactory, err := client.NewSharedClientFactory(restConfig, nil)
	if err != nil {
		return err
	}

	cacheFactory := cache.NewSharedCachedFactory(clientFactory, nil)
	scf := controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{
		DefaultRateLimiter: rateLimit,
		DefaultWorkers:     5,
	})

	if err != nil {
		return err
	}

	instanceFactory, err := instance.NewFactoryFromConfigWithOptions(restConfig, &instance.FactoryOptions{
		SharedControllerFactory: scf,
	})

	if err != nil {
		return err
	}

	corecontrollers, err := core.NewFactoryFromConfigWithOptions(restConfig, &core.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return err
	}

	instanceController.Register(ctx, instanceFactory.Equinix().V1().Instance(), corecontrollers.Core().V1().Node())
	instancePoolController.Register(ctx, instanceFactory.Equinix().V1().InstancePool(),
		instanceFactory.Equinix().V1().Instance(), corecontrollers.Core().V1().Secret(), corecontrollers.Core().V1().Node(), corecontrollers.Core().V1().Service())
	return start.All(ctx, 5, instanceFactory)
}
