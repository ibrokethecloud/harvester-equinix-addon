package instance

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	equinix "github.com/harvester/harvester-equinix-addon/pkg/apis/equinix.harvesterhci.io/v1"
	equinixClient "github.com/harvester/harvester-equinix-addon/pkg/equinix"
	controller "github.com/harvester/harvester-equinix-addon/pkg/generated/controllers/equinix.harvesterhci.io/v1"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	ctx      context.Context
	instance controller.InstanceController
}

func Register(ctx context.Context, instance controller.InstanceController, core corecontrollers.NodeController) {
	iHandler := &handler{
		ctx:      ctx,
		instance: instance,
	}

	relatedresource.WatchClusterScoped(ctx, "node-change", iHandler.ResolveNode, instance, core)
	instance.OnChange(ctx, "instance-change", iHandler.OnInstanceChange)
	instance.OnRemove(ctx, "instance-remove", iHandler.OnInstanceRemove)
}

func (h *handler) OnInstanceChange(key string, i *equinix.Instance) (*equinix.Instance, error) {
	var err error
	if i == nil || i.DeletionTimestamp != nil {
		return i, nil
	}

	status := i.Status.DeepCopy()
	switch status.Status {
	case "": // identify the token
		err = h.submitRequest(i)
	case "submitted": // submit api creation request
		err = h.checkDeviceStatus(i)
	case "ready": // node has processed, disable pxe boot and join config scripts
		return i, nil
	}
	h.instance.EnqueueAfter(key, 5*time.Second)
	return i, err
}

func (h *handler) OnInstanceRemove(_ string, i *equinix.Instance) (*equinix.Instance, error) {
	if i == nil || i.DeletionTimestamp == nil {
		return i, nil
	}

	logrus.Infof("object deleted %s", i.Name)
	return i, nil
}

func (h *handler) ResolveNode(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if node, ok := obj.(*v1.Node); ok {
		instanceName, err := h.findInstanceNodeMapping(node)
		if err != nil {
			return nil, err
		}

		if instanceName != "" {
			return []relatedresource.Key{
				{
					Namespace: "",
					Name:      instanceName,
				},
			}, nil
		}

	}
	return nil, nil
}

func (h *handler) findInstanceNodeMapping(node *v1.Node) (instanceName string, err error) {
	instance, err := h.instance.Get(node.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return instanceName, nil
		} else {
			return instanceName, err
		}
	}

	instanceName = instance.Name
	return instanceName, nil
}

func (h *handler) submitRequest(i *equinix.Instance) error {
	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CreateNewDevice(i)
	if err != nil {
		return err
	}
	i.Status = *status
	return nil
}

func (h *handler) checkDeviceStatus(i *equinix.Instance) error {
	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CheckDeviceStatus(i)
	if err != nil {
		return err
	}
	i.Status = *status
	return nil
}
