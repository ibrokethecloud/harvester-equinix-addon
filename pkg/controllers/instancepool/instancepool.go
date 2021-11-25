package instancepool

import (
	"context"
	"fmt"
	"os"
	"time"

	equinix "github.com/harvester/harvester-equinix-addon/pkg/apis/equinix.harvesterhci.io/v1"
	controller "github.com/harvester/harvester-equinix-addon/pkg/generated/controllers/equinix.harvesterhci.io/v1"
	"github.com/pkg/errors"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/rancher/wrangler/pkg/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	ipoolFinalizer          = "ipool.harvesterhci.io"
	DefaultCredentialSecret = "equinix-addon"
	DefaultNamespace        = "harvester-system"
)

type handler struct {
	ctx          context.Context
	instancePool controller.InstancePoolController
	instance     controller.InstanceController
	secret       corecontrollers.SecretController
}

func Register(ctx context.Context, instancePool controller.InstancePoolController, instance controller.InstanceController, secret corecontrollers.SecretController) {
	ipHandler := &handler{
		ctx:          ctx,
		instancePool: instancePool,
		instance:     instance,
		secret:       secret,
	}
	relatedresource.WatchClusterScoped(ctx, "instance-change", ipHandler.ReconcileNodePool, instancePool, instance)
	instancePool.OnChange(ctx, "instance-change", ipHandler.OnInstanceChange)
}

func (h *handler) OnInstanceChange(key string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {
	if ip == nil || ip.DeletionTimestamp != nil {
		return ip, nil
	}
	var err error
	status := ip.Status.DeepCopy()

	switch status.Status {
	case "":
		err = h.identifyToken(ip)
	case "tokenReady":
		err = h.submitInstances(ip)
	case "submitted":
		err = h.findInstances(ip)
		if err == nil {
			return ip, nil
		}
	case "ready":
		err = h.reconcileInstances(ip)
		if err == nil {
			return ip, nil
		}
	}

	// always requeue instancePool as we ignore it via the switch flow
	h.instancePool.EnqueueAfter(key, 5*time.Second)
	return ip, err
}

func (h *handler) ReconcileNodePool(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if instance, ok := obj.(*equinix.Instance); ok {
		if instance.Status.Status == "running" {
			instancePoolName := instance.Annotations["instancePool"]
			return []relatedresource.Key{
				{
					Namespace: "",
					Name:      instancePoolName,
				},
			}, nil
		}
	}

	return nil, nil
}

// identifyToken assumes rancher/config.yaml is mounted into /etc/rancher/rancherd
func (h *handler) identifyToken(ip *equinix.InstancePool) error {

	token, ok := os.LookupEnv("TOKEN")

	if ok {
		ip.Annotations["token"] = token
	} else {
		config, err := os.ReadFile("/etc/rancher/rancherd/config.yaml")
		if err != nil {
			return errors.Wrap(err, "unable to read config.yaml")
		}

		configMap := make(map[string]interface{})
		err = yaml.Unmarshal(config, configMap)
		if err != nil {
			return errors.Wrap(err, "unable to parse config.yaml")
		}

		token, ok := configMap["token"]
		if !ok {
			return errors.Wrap(err, "no token found in config.yaml")
		}

		if ip.Annotations == nil {
			ip.Annotations = make(map[string]string)
		}

		ip.Annotations["token"] = token.(string)
	}
	ip.Status.Status = "tokenReady"
	return nil
}

// submitInstances will create instance objects
func (h *handler) submitInstances(ip *equinix.InstancePool) error {

	credSecret := os.Getenv("EQUINIX_SECRET")
	if credSecret == "" {
		credSecret = DefaultCredentialSecret
	}
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = DefaultNamespace
	}

	secret, err := h.secret.Get(namespace, credSecret, metav1.GetOptions{})
	if err != nil {
		return err
	}

	token, ok := secret.Data["METAL_AUTH_TOKEN"]
	if !ok {
		return fmt.Errorf("operator secret doesnt contain a key METAL_AUTH_TOKEN")
	}

	projectID, ok := secret.Data["PROJECT_ID"]
	if !ok {
		return fmt.Errorf("operator secret doesnt contain a key PROJECT_ID")
	}

	// lookup token and project id from secret
	// set up as annotation
	for i := ip.Status.Requested; i <= ip.Spec.Count-ip.Status.Ready; i++ {
		i := &equinix.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%d", ip.Name, i),
			},
			Spec: equinix.InstanceSpec{
				Plan:           ip.Spec.Plan,
				BillingCycle:   ip.Spec.BillingCycle,
				Metro:          ip.Spec.Metro,
				Facility:       ip.Spec.Facility,
				SpotInstance:   ip.Spec.SpotInstance,
				SpotPriceMax:   ip.Spec.SpotPriceMax,
				UserSSHKeys:    ip.Spec.UserSSHKeys,
				ProjectSSHKeys: ip.Spec.ProjectSSHKeys,
				ProjectID:      string(projectID),
			},
		}

		i.SetOwnerReferences(ip.GetOwnerReferences())
		labels := make(map[string]string)
		labels["instancePool"] = ip.Name
		i.SetLabels(labels)
		annotations := make(map[string]string)
		annotations["token"] = string(token)
		_, err := h.instance.Create(i)
		if err != nil {
			return err
		}
		ip.Status.Requested++
	}

	ip.Status.Status = "submitted"
	ip.Status.Requested = ip.Spec.Count
	return nil
}

// identify instances will reconcile instance states
func (h *handler) findInstances(ip *equinix.InstancePool) error {
	instanceList, err := h.instance.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instancePool=%s", ip.Name),
	})

	if err != nil {
		return err
	}

	for _, instance := range instanceList.Items {
		if instance.Status.Status == "ready" {
			ip.Status.Ready++
		}
	}

	if ip.Status.Requested == ip.Status.Ready {
		ip.Status.Status = "ready"
	}
	return nil
}

// used to decide if we need to trigger resubmission of instances
func (h *handler) reconcileInstances(ip *equinix.InstancePool) error {
	instanceList, err := h.instance.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instancePool=%s", ip.Name),
	})

	if err != nil {
		return err
	}

	var readyCount int
	for _, i := range instanceList.Items {
		if i.Status.Status == "ready" {
			readyCount++
		}
	}

	if ip.Status.Ready != readyCount {
		ip.Status.Ready = readyCount
		ip.Status.Status = "tokenReady"
	}

	return nil
}

func containsFinalizer(arr []string, key string) bool {
	for _, v := range arr {
		if v == key {
			return true
		}
	}

	return false
}
