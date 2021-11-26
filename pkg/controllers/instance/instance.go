package instance

import (
	"context"
	"time"

	"github.com/harvester/harvester-equinix-addon/pkg/util"

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
	node     corecontrollers.NodeController
}

const (
	finalizer = "equinix.instance.harvesterhci.io"
)

func Register(ctx context.Context, instance controller.InstanceController, node corecontrollers.NodeController) {
	iHandler := &handler{
		ctx:      ctx,
		instance: instance,
		node:     node,
	}

	relatedresource.WatchClusterScoped(ctx, "node-change", iHandler.ResolveNode, instance, node)
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
		logrus.Infof("creating node %s in equinix metal\n", i.Name)
		err = h.submitRequest(i)
	case "submitted": // submit api creation request
		logrus.Infof("checking status of node %s\n", i.Name)
		err = h.checkDeviceStatus(i)
	case "ready": // node has processed, disable pxe boot and join config scripts
		logrus.Infof("node %s is ready\n", i.Name)
		return i, nil
	}

	if err != nil {
		return i, err
	}

	// update status of object //
	logrus.Info("updating instance status")
	_, err = h.instance.UpdateStatus(i)

	if err != nil {
		return i, err
	}

	h.instance.EnqueueAfter(key, 5*time.Second)
	return i, nil
}

func (h *handler) OnInstanceRemove(_ string, i *equinix.Instance) (*equinix.Instance, error) {
	var err error
	if i == nil || i.DeletionTimestamp == nil {
		return i, nil
	}

	if util.ContainsFinalizer(i.GetFinalizers(), finalizer) {
		m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
		logrus.Infof("object deleted %s", i.Name)
		err = m.DeleteDevice(i)
		if err != nil {
			return i, err
		}
		err = h.findAndDeleteNode(i)
		if err != nil {
			return i, err
		}
	}

	modifiedFinalizers, modified := util.RemoveFinalizer(i.GetFinalizers(), finalizer)
	if modified {
		i.SetFinalizers(modifiedFinalizers)
		_, err = h.instance.Update(i)
	}

	return i, err
}

func (h *handler) ResolveNode(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if node, ok := obj.(*v1.Node); ok {
		if node.DeletionTimestamp == nil {
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
		} else {
			err := h.findAndDeleteInstance(node)
			if err != nil {
				return nil, err
			}
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
	// breaker

	i.Status.Status = "ready"
	return nil

	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CreateNewDevice(i)
	if err != nil {
		return err
	}
	i.SetFinalizers([]string{finalizer})
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

func (h *handler) findAndDeleteNode(i *equinix.Instance) error {
	_, err := h.node.Get(i.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}

	return h.node.Delete(i.Name, &metav1.DeleteOptions{})
}

func (h *handler) findAndDeleteInstance(node *v1.Node) error {
	_, err := h.instance.Get(node.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}

	return h.instance.Delete(node.Name, &metav1.DeleteOptions{})
}
