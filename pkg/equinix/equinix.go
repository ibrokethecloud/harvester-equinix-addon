package equinix

import (
	"fmt"

	api "github.com/harvester/harvester-equinix-addon/pkg/apis/equinix.harvesterhci.io/v1"
	"github.com/harvester/harvester-equinix-addon/pkg/harvester"
	"github.com/packethost/packngo"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
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
		Hostname:              instance.Name,
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
		status.Status = "ready"
		status.PrivateIP = deviceStatus.GetNetworkInfo().PrivateIPv4
		status.PublicIP = deviceStatus.GetNetworkInfo().PublicIPv4
	} else {
		status.Status = deviceStatus.State
	}
	return status, nil
}

func (m *MetalClient) DeleteDevice(instance *api.Instance) (err error) {
	if instance.Status.InstanceID == "" {
		return nil
	}
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

func (m *MetalClient) ReInstallDevice(instance *api.Instance) (status *api.InstanceStatus, err error) {
	status = instance.Status.DeepCopy()

	device, _, err := m.Devices.Get(instance.Status.InstanceID, nil)
	if err != nil {
		return status, err
	}

	err = m.UpdateNetworkConfig(device, instance.Spec.NetworkingConfiguration)
	if err != nil {
		return status, err
	}
	// find mac addresses //
	macAddresses := []string{}

	for _, ifaceName := range instance.Spec.ManagementInterfaces {
		port, err := device.GetPortByName(ifaceName)
		if err != nil {
			return status, err
		}

		macAddresses = append(macAddresses, port.Data.MAC)
	}

	cloudInit, err := updateCloudInit(instance.Spec.UserData, macAddresses, instance.Spec.ManagementBondingOptions)
	if err != nil {
		return status, err
	}

	ipxeURL := instance.Annotations["reconfig_ipxe_url"]
	deviceUpdateRequest := &packngo.DeviceUpdateRequest{
		IPXEScriptURL: &ipxeURL,
		UserData:      &cloudInit,
	}

	_, _, err = m.Devices.Update(instance.Status.InstanceID, deviceUpdateRequest)
	if err != nil {
		return status, err
	}

	_, err = m.Devices.Reinstall(instance.Status.InstanceID, &packngo.DeviceReinstallFields{PreserveData: true, DeprovisionFast: true})

	if err != nil {
		return status, err
	}

	status.Status = "reinstalling"
	return status, nil
}

func updateCloudInit(baseCloudInit string, macAddresses []string, bondOptions map[string]string) (string, error) {

	hc := &harvester.HarvesterConfig{}
	err := yaml.Unmarshal([]byte(baseCloudInit), hc)
	if err != nil {
		return "", err
	}

	networkInterfaces := []harvester.NetworkInterface{}
	for _, macAddress := range macAddresses {
		networkInterfaces = append(networkInterfaces, harvester.NetworkInterface{HwAddr: macAddress})
	}

	if hc.Networks == nil {
		hc.Networks = make(map[string]harvester.Network)
	}

	mgmtNetwork := harvester.Network{
		Interfaces:   networkInterfaces,
		Method:       "dhcp",
		DefaultRoute: true,
	}

	if bondOptions != nil {
		mgmtNetwork.BondOptions = bondOptions
	}

	hc.Networks["harvester-mgmt"] = mgmtNetwork
	updatedCloudInit, err := yaml.Marshal(hc)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("#cloud-config\n%s", string(updatedCloudInit)), nil
}

func (m *MetalClient) UpdateNetworkConfig(device *packngo.Device, network api.NetworkingConfiguration) error {

	if network.Type == "" {
		// no actual network reconfig is needed
		return nil
	}

	err := m.ConvertDevice(device, network.Type)
	if err != nil {
		return err
	}

	// apply VLANS
	for _, netInterface := range network.Interfaces {
		port, err := device.GetPortByName(netInterface.Name)
		if err != nil {
			return err
		}

		for _, vlan := range netInterface.VlanIDS {
			_, _, err = m.Client.Ports.Assign(port.ID, vlan)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// ConvertDevice is fork from Packngo ConvertDevice. Changed to use non deprecated port service
// methods
func (m *MetalClient) ConvertDevice(d *packngo.Device, targetType string) error {
	bondPorts := d.GetBondPorts()
	allEthPorts := d.GetPhysicalPorts()

	bond0Port := bondPorts["bond0"]
	var oddEthPorts []packngo.Port
	for _, portName := range []string{"eth1", "eth3", "eth5", "eth7", "eth9"} {
		if ethPort, ok := allEthPorts[portName]; ok {
			oddEthPorts = append(oddEthPorts, *ethPort)
		}

	}

	if targetType == "layer3" {
		// TODO: remove vlans from all the ports
		for _, p := range bondPorts {
			_, _, err := m.Client.Ports.Bond(p.ID, false)
			if err != nil {
				return err
			}
		}

		_, _, err := m.Client.Ports.ConvertToLayerThree(bond0Port.ID, []packngo.AddressRequest{
			{AddressFamily: 4, Public: true},
			{AddressFamily: 4, Public: false},
			{AddressFamily: 6, Public: true},
		})

		if err != nil {
			return err
		}

		for _, p := range allEthPorts {
			_, _, err := m.Client.Ports.Bond(p.ID, false)
			if err != nil {
				return err
			}
		}
		return nil
	}

	if targetType == "hybrid" {
		// ports need to be refreshed before bonding/disbonding
		for _, p := range oddEthPorts {
			if p.DisbondOperationSupported {
				_, _, err := m.Client.Ports.Disbond(p.ID, false)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	if targetType == "layer2-individual" {
		_, _, err := m.Client.Ports.ConvertToLayerTwo(bond0Port.ID)
		if err != nil {
			return err
		}
		for _, p := range allEthPorts {
			if p.DisbondOperationSupported {
				_, _, err = m.Client.Ports.Disbond(p.ID, true)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	if targetType == "layer2-bonded" {

		for _, p := range bondPorts {
			_, _, err := m.Client.Ports.ConvertToLayerTwo(p.ID)
			if err != nil {
				return err
			}
		}
		for _, p := range allEthPorts {
			_, _, err := m.Client.Ports.Bond(p.ID, false)
			if err != nil {
				return err
			}
		}

		return nil
	}

	if targetType == "hybrid-bonded" {
		// nothing needs to be done. VLANS are just applied to bond0 interface
		return nil
	}

	return fmt.Errorf("invalid network type %s in instance", targetType)
}
