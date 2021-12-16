package instancepool

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/sirupsen/logrus"

	equinix "github.com/harvester/harvester-equinix-addon/pkg/apis/equinix.harvesterhci.io/v1"
	controller "github.com/harvester/harvester-equinix-addon/pkg/generated/controllers/equinix.harvesterhci.io/v1"
	"github.com/harvester/harvester-equinix-addon/pkg/harvester"
	"github.com/harvester/harvester-equinix-addon/pkg/util"
	"github.com/pkg/errors"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultCredentialSecret = "equinix-addon"
	DefaultNamespace        = "harvester-system"
	DefaultISOURL           = "https://releases.rancher.com/harvester/master/harvester-master-amd64.iso"
	DefaultIPXEScriptURL    = "https://raw.githubusercontent.com/ibrokethecloud/custom_pxe/master/master.ipxe"
	InitialIPXEScriptURL    = "https://raw.githubusercontent.com/ibrokethecloud/custom_pxe/master/shell.ipxe"
	DefaultInterface        = "eth0"
	DefaultIngressService   = "ingress-expose"
)

var instanceLock sync.Mutex

type handler struct {
	ctx          context.Context
	instancePool controller.InstancePoolController
	instance     controller.InstanceController
	secret       corecontrollers.SecretController
	node         corecontrollers.NodeController
	service      corecontrollers.ServiceController
}

func Register(ctx context.Context, instancePool controller.InstancePoolController,
	instance controller.InstanceController, secret corecontrollers.SecretController,
	node corecontrollers.NodeController, service corecontrollers.ServiceController) {
	ipHandler := &handler{
		ctx:          ctx,
		instancePool: instancePool,
		instance:     instance,
		secret:       secret,
		node:         node,
		service:      service,
	}
	relatedresource.WatchClusterScoped(ctx, "instancePool-instance-change", ipHandler.ReconcileNodePool, instancePool, instance)
	instancePool.OnChange(ctx, "instancePool-change", ipHandler.wrapper)
}

func (h *handler) wrapper(key string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {
	if ip == nil || ip.DeletionTimestamp != nil {
		return ip, nil
	}

	switch ip.Status.Status {
	case "":
		return h.prepareInstancePool(key, ip)
	case "tokenReady":
		return h.submitInstances(key, ip)
	case "submitted", "ready":
		return h.reconcileInstances(key, ip)
	case "cleanupNodes":
		return h.removeInstances(key, ip)
	}

	return ip, nil
}

func (h *handler) ReconcileNodePool(_ string, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if instance, ok := obj.(*equinix.Instance); ok {
		if instance.Status.Status == "managed" || instance.DeletionTimestamp != nil {
			instancePoolName := instance.Labels["instancePool"]
			logrus.Infof("instance %s got updated. Reconcilling instancePool %s", instance.Name, instancePoolName)
			return []relatedresource.Key{
				{
					Name: instancePoolName,
				},
			}, nil
		}
	}

	return nil, nil
}

// identifyToken assumes rancher/config.yaml is mounted into /etc/rancher/rancherd
func (h *handler) prepareInstancePool(key string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {

	logrus.Infof("preparing instancePool %s", key)
	token, ok := os.LookupEnv("TOKEN")

	if ok {
		ip.Status.Token = token
	} else {
		config, err := os.ReadFile("/etc/rancher/rancherd/config.yaml")
		if err != nil {
			return ip, errors.Wrap(err, "unable to read config.yaml")
		}

		configMap := make(map[string]interface{})
		err = yaml.Unmarshal(config, configMap)
		if err != nil {
			return ip, errors.Wrap(err, "unable to parse config.yaml")
		}

		token, ok := configMap["token"]
		if !ok {
			return ip, errors.Wrap(err, "no token found in config.yaml")
		}

		ip.Status.Token = token.(string)
	}
	ip.Status.Needed = ip.Spec.Count
	ip.Status.Status = "tokenReady"
	ip.Status.Requested = ip.Spec.Count

	return h.instancePool.UpdateStatus(ip)
}

// submitInstances will create instance objects
func (h *handler) submitInstances(key string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {
	instanceLock.Lock()
	logrus.Infof("submitting instances for instancePool %s", key)
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
		return ip, err
	}

	token, ok := secret.Data["METAL_AUTH_TOKEN"]
	if !ok {
		return ip, fmt.Errorf("operator secret doesnt contain a key METAL_AUTH_TOKEN")
	}

	projectID, ok := secret.Data["PROJECT_ID"]
	if !ok {
		return ip, fmt.Errorf("operator secret doesnt contain a key PROJECT_ID")
	}

	nodes, err := h.node.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("node-role.kubernetes.io/control-plane=true"),
	})
	if err != nil {
		return ip, err
	}

	if len(nodes.Items) == 0 {
		fmt.Errorf("no control-plane nodes found")
	}

	joinAddress, err := h.findJoinAddress()
	if err != nil {
		return ip, err
	}

	// lookup token and project id from secret
	// set up as annotation

	for i := 1; i <= ip.Status.Needed; i++ {
		suffix := util.LowerRandStringRunes(8)
		i := &equinix.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", ip.Name, suffix),
			},
			Spec: equinix.InstanceSpec{
				OS:             "custom_ipxe",
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

		i.Spec.IPXEScriptURL = InitialIPXEScriptURL

		if ip.Spec.ManagementBondingOptions != nil {
			i.Spec.ManagementBondingOptions = ip.Spec.ManagementBondingOptions
		}

		if ip.Spec.NodeCleanupWaitInterval != nil {
			i.Spec.NodeCleanupWaitInterval = ip.Spec.NodeCleanupWaitInterval
		}

		if len(ip.Spec.ManagementInterfaces) != 0 {
			i.Spec.ManagementInterfaces = ip.Spec.ManagementInterfaces
		} else {
			i.Spec.ManagementInterfaces = []string{DefaultInterface}
		}
		i.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: "equinix.harvesterhci.io/v1",
				Kind:       "InstancePool",
				Name:       ip.Name,
				UID:        ip.UID,
			},
		})
		labels := make(map[string]string)
		labels["instancePool"] = ip.Name
		i.SetLabels(labels)
		annotations := make(map[string]string)
		annotations["token"] = string(token)
		annotations["password"] = util.RandStringRunes(16)

		if ip.Spec.IPXEScriptURL != "" {
			annotations["reconfig_ipxe_url"] = ip.Spec.IPXEScriptURL
		} else {
			annotations["reconfig_ipxe_url"] = DefaultIPXEScriptURL
		}
		i.SetAnnotations(annotations)
		// generateCloudInit //
		userData, err := generateCloudInit(ip, i, joinAddress)
		if err != nil {
			return ip, err
		}
		i.Spec.UserData = userData
		_, err = h.instance.Create(i)
		if err != nil {
			return ip, err
		}
	}

	ip.Status.Status = "submitted"
	ip.Status.Needed = 0
	_, err = h.instancePool.UpdateStatus(ip)
	instanceLock.Unlock()
	if err != nil {
		return ip, err
	}

	return ip, nil
}

// identify instances will reconcile instance states
func (h *handler) reconcileInstances(_ string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {

	logrus.Infof("ready to fetch instances to reoncile instancePool state %s", ip.Name)
	instanceList, err := h.instance.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instancePool=%s", ip.Name),
	})

	if err != nil {
		return ip, err
	}

	readyCount := 0
	presentCount := 0
	for _, instance := range instanceList.Items {
		if instance.Status.Status == "managed" {
			readyCount++
		}
		presentCount++
	}

	modified := false
	if ip.Status.Requested == readyCount && ip.Status.Requested == ip.Spec.Count {
		ip.Status.Status = "ready"
		ip.Status.Needed = 0
		modified = true
	} else {
		ip.Status.Requested = ip.Spec.Count
		ip.Status.Needed = ip.Spec.Count - presentCount
		if ip.Status.Needed < 0 {
			ip.Status.Status = "cleanupNodes"
		}

		if ip.Status.Needed > 0 {
			ip.Status.Status = "tokenReady"
		}
		modified = true
	}

	if modified {
		ip.Status.Ready = readyCount
		return h.instancePool.UpdateStatus(ip)
	}

	return ip, nil

}

func generateCloudInit(ip *equinix.InstancePool, i *equinix.Instance, joinAddress string) (string, error) {

	hc := harvester.HarvesterConfig{
		ServerURL: fmt.Sprintf("https://%s:8443", joinAddress),
		Token:     ip.Status.Token,
		OS: harvester.OS{
			Hostname: i.Name,
			Password: i.Annotations["password"],
		},
		Install: harvester.Install{
			Automatic: true,
			Mode:      "join",
			TTY:       "ttyS1,115200n8",
			Device:    "/dev/sda",
		},
	}

	// set ISO URL //
	if ip.Spec.ISOURL != "" {
		hc.Install.ISOURL = ip.Spec.ISOURL
	} else {
		hc.Install.ISOURL = DefaultISOURL
	}
	config, err := yaml.Marshal(hc)
	if err != nil {
		return "", errors.Wrap(err, "error during marshalling harverster config to cloudInit")
	}

	return fmt.Sprintf("#cloud-config\n%s", string(config)), nil
}

func (h *handler) removeInstances(_ string, ip *equinix.InstancePool) (*equinix.InstancePool, error) {
	instanceList, err := h.instance.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instancePool=%s", ip.Name),
	})

	if err != nil {
		return ip, err
	}

	if len(instanceList.Items) != 0 {
		for i := ip.Status.Needed; i < 0; i++ {
			err = h.instance.Delete(instanceList.Items[len(instanceList.Items)-1].Name, &metav1.DeleteOptions{})
			if err != nil {
				return ip, err
			}
			instanceList.Items = instanceList.Items[:len(instanceList.Items)-1]
			ip.Status.Requested--
			ip.Status.Needed++
		}
	}

	ip.Status.Status = "submitted"
	return h.instancePool.UpdateStatus(ip)
}

func (h *handler) findJoinAddress() (string, error) {
	svc, err := h.service.Get("kube-system", DefaultIngressService, metav1.GetOptions{})

	if err != nil {
		return "", err
	}

	return svc.Status.LoadBalancer.Ingress[0].IP, nil
}
