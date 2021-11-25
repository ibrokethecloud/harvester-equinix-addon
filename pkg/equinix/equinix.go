package equinix

import (
	"fmt"

	api "github.com/harvester/harvester-equinix-addon/pkg/apis/equinix.harvesterhci.io/v1"
	"github.com/packethost/packngo"
	"github.com/pkg/errors"
)

type MetalClient struct {
	*packngo.Client
	ProjectID string
}

func NewClient(token, projectID string) *MetalClient {
	m := &MetalClient{
		Client:    packngo.NewClientWithAuth("packngo lib", token, nil),
		ProjectID: projectID,
	}

	return m
}

func (m *MetalClient) CreateNewDevice(instance *api.Instance) (status *api.InstanceStatus, err error) {
	status = instance.Status.DeepCopy()
	dsr := m.generateDeviceCreationRequest(instance)
	device, _, err := m.Devices.Create(dsr)
	if err != nil {
		return status, errors.Wrap(err, "error during device creation")
	}

	status.InstanceID = device.ID
	status.Status = device.State
	return status, err
}

func (m *MetalClient) generateDeviceCreationRequest(instance *api.Instance) (dsr *packngo.DeviceCreateRequest) {
	dsr = &packngo.DeviceCreateRequest{
		Hostname:              fmt.Sprintf("%s-%s", instance.Name, instance.Namespace),
		Plan:                  instance.Spec.Plan,
		Facility:              instance.Spec.Facility,
		Metro:                 instance.Spec.Metro,
		ProjectID:             instance.Spec.ProjectID,
		AlwaysPXE:             instance.Spec.AlwaysPXE,
		Tags:                  instance.Spec.Tags,
		Description:           instance.Spec.Description,
		PublicIPv4SubnetSize:  instance.Spec.PublicIPv4SubnetSize,
		HardwareReservationID: instance.Spec.HardwareReservationID,
		SpotInstance:          instance.Spec.SpotInstance,
		SpotPriceMax:          instance.Spec.SpotPriceMax.AsApproximateFloat64(),
		CustomData:            instance.Spec.CustomData,
		UserSSHKeys:           instance.Spec.UserSSHKeys,
		ProjectSSHKeys:        instance.Spec.ProjectSSHKeys,
		Features:              instance.Spec.Features,
		NoSSHKeys:             instance.Spec.NoSSHKeys,
		OS:                    instance.Spec.OS,
		BillingCycle:          instance.Spec.BillingCycle,
		IPXEScriptURL:         instance.Spec.IPXEScriptURL,
	}

	if dsr.ProjectID == "" {
		dsr.ProjectID = m.ProjectID
	}
	return dsr
}

func (m *MetalClient) CheckDeviceStatus(instance *api.Instance) (status *api.InstanceStatus, err error) {
	status = instance.Status.DeepCopy()
	deviceStatus, _, err := m.Devices.Get(instance.Status.InstanceID, nil)
	if err != nil {
		return status, err
	}

	if deviceStatus.State == "active" {
		status.Status = "running"
		status.PrivateIP = deviceStatus.GetNetworkInfo().PrivateIPv4
		status.PublicIP = deviceStatus.GetNetworkInfo().PublicIPv4
	}

	return status, nil
}

func (m *MetalClient) DeleteDevice(instance *api.Instance) (err error) {

	ok, err := m.deviceExists(instance.Status.InstanceID)
	if err != nil {
		return err
	}

	// device exists. terminate the same.
	if ok {
		_, err = m.Devices.Delete(instance.Status.InstanceID, true)
		return err
	}

	// device doesnt exist.. ignore object
	return nil
}

func (m *MetalClient) deviceExists(instanceID string) (ok bool, err error) {
	devices, _, err := m.Devices.List(m.ProjectID, nil)
	if err != nil {
		return ok, err
	}

	for _, device := range devices {
		if device.ID == instanceID {
			ok = true
			return ok, nil
		}
	}

	return ok, nil

}
