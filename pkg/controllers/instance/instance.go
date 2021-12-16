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
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	node.OnChange(ctx, "node-change", iHandler.ResolveNode)
	instance.OnChange(ctx, "instance-change", iHandler.OnInstanceChange)
	instance.OnRemove(ctx, "instance-remove", iHandler.OnInstanceRemove)
}

func (h *handler) OnInstanceChange(key string, i *equinix.Instance) (*equinix.Instance, error) {
	if i == nil || i.DeletionTimestamp != nil {
		return i, nil
	}

	switch i.Status.Status {
	case "": // identify the token
		logrus.Infof("creating node %s in equinix metal\n", i.Name)
		return h.submitRequest(key, i)
	case "submitted", "queued": // submit api creation request
		logrus.Infof("waiting to reconfigure the node %s\n", i.Name)
		return h.reinstallDevice(key, i)
	case "reinstalling":
		logrus.Infof("waiting for node %s to be active\n", i.Name)
		return h.checkDeviceStatus(key, i)
	case "ready": // node has processed, disable pxe boot and join config scripts
		logrus.Infof("instance %s is ready\n", i.Name)
		return h.manageNodes(key, i)
	case "managed":
		logrus.Infof("instance %s is managed \n", i.Name)
		return i, nil
	}

	return i, nil
}

func (h *handler) OnInstanceRemove(_ string, i *equinix.Instance) (*equinix.Instance, error) {
	var err error
	if i == nil || i.DeletionTimestamp == nil {
		return i, nil
	}

	if util.ContainsFinalizer(i.GetFinalizers(), finalizer) {
		m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.Spec.ProjectID)
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

func (h *handler) ResolveNode(_ string, node *v1.Node) (*v1.Node, error) {
	if node != nil && node.DeletionTimestamp == nil {
		instanceName, err := h.findInstanceNodeMapping(node)
		if err != nil {
			return node, err
		}

		if instanceName != "" {
			// check if node needs to be cleaned
			i, ok, err := h.managedNodeNotReady(node)
			if err != nil {
				return nil, err
			}

			if ok {
				err = h.instance.Delete(i.Name, &metav1.DeleteOptions{})
				return nil, err
			} else { // no clean up needed. Possible use case where node only popped up into the cluster
				h.instance.Enqueue(instanceName)
			}
			return node, nil
		}

	}

	// node has a deletion timestamp
	err := h.findAndDeleteInstance(node)
	return node, err
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

func (h *handler) submitRequest(_ string, i *equinix.Instance) (*equinix.Instance, error) {

	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CreateNewDevice(i)
	if err != nil {
		return i, err
	}
	i.Status = *status
	i, err = h.instance.UpdateStatus(i)
	if err != nil {
		return i, err
	}
	i.SetFinalizers([]string{finalizer})
	return h.instance.Update(i)
}

func (h *handler) checkDeviceStatus(key string, i *equinix.Instance) (*equinix.Instance, error) {
	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CheckDeviceStatus(i)
	if err != nil {
		return i, err
	}

	if status.Status != "ready" {
		h.instance.EnqueueAfter(key, 2*time.Minute)
		return i, nil
	}

	i.Status = *status
	return h.instance.UpdateStatus(i)
}

func (h *handler) reinstallDevice(key string, i *equinix.Instance) (*equinix.Instance, error) {
	m := equinixClient.NewClient(i.ObjectMeta.Annotations["token"], i.ObjectMeta.Annotations["projectID"])
	status, err := m.CheckDeviceStatus(i)
	if err != nil {
		return i, err
	}

	if status.Status != "ready" {
		h.instance.EnqueueAfter(key, 2*time.Minute)
		return i, nil
	}

	// device is inactive and has been powered off...
	status, err = m.ReInstallDevice(i)
	if err != nil {
		return i, err
	}
	i.Status = *status
	logrus.Infof("reconfigured node %s\n", i.Name)
	return h.instance.UpdateStatus(i)
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
	if node == nil {
		return nil
	}
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

// managedNodeNotReady deletes instance objects which are not ready for more than a specified time.
// this helps the instancePool controller to manage a fleet of healthy worker nodes
func (h *handler) managedNodeNotReady(node *v1.Node) (*equinix.Instance, bool, error) {
	nodeUnhealthy := false
	var lastHealthyTime metav1.Time
	for _, condition := range node.Status.Conditions {
		if condition.Type == "Ready" && condition.Status != "True" {
			nodeUnhealthy = true
			lastHealthyTime = condition.LastHeartbeatTime
		}
	}

	if nodeUnhealthy {
		// check if a corresponding instance object exists
		i, err := h.instance.Get(node.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// no instance object exists. Ignore and move along
				logrus.Infof("no instance matching node %s found. Ignoring this node", node.Name)
				return nil, false, nil
			} else {
				// retry again
				return nil, false, err
			}
		}

		//if node is managed and has been healthy for longer than specified time
		if i.Status.Status == "managed" && i.Spec.NodeCleanupWaitInterval != nil {
			if lastHealthyTime.Time.Add(i.Spec.NodeCleanupWaitInterval.Duration).Before(time.Now()) {
				logrus.Infof("node %s will be cleaned up", node.Name)
				return i, true, nil
			} else {
				// lets requeue the node and check again
				h.node.EnqueueAfter(node.Name, i.Spec.NodeCleanupWaitInterval.Duration)
				return i, false, nil
			}

		}
	}

	return nil, false, nil
}

// manageNodes reconciles nodes
func (h *handler) manageNodes(_ string, i *equinix.Instance) (*equinix.Instance, error) {
	node, err := h.node.Get(i.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return i, nil
		} else {
			return i, err
		}
	}

	if node != nil {
		i.Status.Status = "managed"
		return h.instance.UpdateStatus(i)
	}

	return i, nil
}
