package v1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type InstancePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstancePoolSpec   `json:"spec,omitempty"`
	Status InstancePoolStatus `json:"status,omitempty"`
}

type InstancePoolSpec struct {
	Count                    int               `json:"count"`
	BillingCycle             string            `json:"billingCycle,omitempty"`
	SpotInstance             bool              `json:"spotInstance,omitempty"`
	SpotPriceMax             resource.Quantity `json:"spotPriceMax,omitempty,string"`
	CustomData               string            `json:"customData,omitempty"`
	UserSSHKeys              []string          `json:"usersshKeys,omitempty"`
	ProjectSSHKeys           []string          `json:"projectsshKeys,omitempty"`
	Features                 map[string]string `json:"features,omitempty"`
	NoSSHKeys                bool              `json:"nosshKeys,omitempty"`
	ManagementInterfaces     []string          `json:"managementInterface"`
	ManagementBondingOptions map[string]string `json:"managementBondingOptions,omitempty"`
	IPXEScriptURL            string            `json:"ipxeScriptUrl,omitempty"`
	ISOURL                   string            `json:"isoUrl,omitempty"`
	Plan                     string            `json:"plan"`
	Metro                    string            `json:"metro,omitempty"`
	Facility                 []string          `json:"facility,omitempty"`
	NodeCleanupWaitInterval  *metav1.Duration  `json:"nodeCleanupWaitInterval,omitempty"`
	NetworkingConfiguration  `json:"networkingConfiguration,omitempty"`
}

type InstancePoolStatus struct {
	Status    string `json:"status"`
	Ready     int    `json:"ready"`
	Requested int    `json:"requested"`
	Needed    int    `json:"needed"`
	Token     string `json:"token"`
}

type NetworkingConfiguration struct {
	Type       string                   `json:"type"`
	Interfaces []InterfaceConfiguration `json:"interfaceConfiguration"`
}

type InterfaceConfiguration struct {
	Name    string   `json:"name"`
	VlanIDS []string `json:"vlanIDS"`
}

func (n *NetworkingConfiguration) IsEmpty() bool {
	return n.Type == ""
}

func (n *NetworkingConfiguration) IsValidType() bool {
	switch n.Type {
	case "layer2-bonded", "layer2-individual", "layer3", "hybrid", "hybrid-bonded":
		return true
	}

	return false
}
