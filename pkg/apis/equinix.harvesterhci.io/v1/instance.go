package v1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// InstanceSpec defines the desired state of Instance
type InstanceSpec struct {
	Plan                     string            `json:"plan"`
	Facility                 []string          `json:"facility,omitempty"`
	Metro                    string            `json:"metro,omitempty"`
	OS                       string            `json:"operating_system"`
	BillingCycle             string            `json:"billingCycle"`
	ProjectID                string            `json:"projectID,omitempty"`
	UserData                 string            `json:"userdata,omitempty"`
	Tags                     []string          `json:"tags,omitempty"`
	Description              string            `json:"description,omitempty"`
	IPXEScriptURL            string            `json:"ipxeScriptUrl,omitempty"`
	PublicIPv4SubnetSize     int               `json:"publicIPv4SubnetSize,omitempty"`
	AlwaysPXE                bool              `json:"alwaysPxe,omitempty"`
	HardwareReservationID    string            `json:"hardwareReservation_id,omitempty"`
	SpotInstance             bool              `json:"spotInstance,omitempty"`
	SpotPriceMax             resource.Quantity `json:"spotPriceMax,omitempty,string"`
	CustomData               string            `json:"customData,omitempty"`
	UserSSHKeys              []string          `json:"usersshKeys,omitempty"`
	ProjectSSHKeys           []string          `json:"projectsshKeys,omitempty"`
	Features                 map[string]string `json:"features,omitempty"`
	NoSSHKeys                bool              `json:"nosshKeys,omitempty"`
	NodeCleanupWaitInterval  *metav1.Duration  `json:"nodeCleanupWaitInterval,omitempty"`
	ManagementInterfaces     []string          `json:"managementInterfaces,omitempty"`
	ManagementBondingOptions map[string]string `json:"managementBondingOptions,omitempty"`
}

// InstanceStatus defines the observed state of Instance
type InstanceStatus struct {
	Status     string `json:"status"`
	InstanceID string `json:"instanceID"`
	PublicIP   string `json:"publicIP"`
	PrivateIP  string `json:"privateIP"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Instance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstanceSpec   `json:"spec,omitempty"`
	Status InstanceStatus `json:"status,omitempty"`
}
