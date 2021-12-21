# harvester-equinix-addon

A simple K8S operator to provision and manage node pools of metal nodes for Harvester in your Equinix Metal account.

The operator needs to be deployed on the first Harvester node, and references the Equinix account credentials via a secret `equinix-addon` in `harvester-system` namespace.

The secret needs to contain two keys:
* METAL_AUTH_TOKEN
* PROJECT_ID

Once deployed the user can configure a NodePool using the sample manifest:

```yaml
apiVersion: equinix.harvesterhci.io/v1
kind: InstancePool
metadata:
  name: harvester-pxe-worker
spec:
  count: 1
  billingCycle: hourly
  managementInterface:
    - eth0
  plan: c3.small.x86
  metro: SG
  nodeCleanupWaitInterval: 5m
  managementBondingOptions:
    mode: balance-tlb
    miimon: "100"
  networkingConfiguration:
    type: "hybrid"
    interfaceConfiguration:
      - name: eth1
        vlanIDS:
          - "1000"
          - "1001"
```

The operator will provision and manage Equinix Metal instances

```cassandraql
▶ kubectl get instancepool
NAME                   STATUS   READY   REQUESTED
harvester-pxe-worker   ready    1       1
~
▶ kubectl get instance
NAME                            STATUS    INSTANCEID                             PUBLICIP        PRIVATEIP
harvester-pxe-worker-zaoolitj   managed   1c6106a0-13e6-44fa-af2c-bec67d4b6c65   145.40.73.137   10.8.23.5
```

The provisioning flow is as follows:

* InstancePool operator generates and manages associated Instance objects which have a custom ipxe script to boot nodes into shell prompt and also generates an intermediate cloudInit.
* Once the instance has booted, the operator queries the macAddresses for the management interfaces and generates the appropriate cloudInit by using the intermediate cloudInit and merging the macAddress of the instance into the HarvesterConfig.
* The instance operator also updates the ipxe script to actually install harvester.
* After merging the cloudInit, the operator triggers re-install of the Equinix metal instance and waits for this instance to join the Harvester Cluster Nodes


** NOTE** The re-install is needed as we need to query the MacAddress of the nodes before actually trying to install Harvester with the appropriate Join configuration.


### Network Configuration
The operator allows the network to be configured on the nodes in the instance pool based on the networkConfiguration

A sample configuration looks as follows:



By default the newly configured nodes are launched in layer3 mode, with all physical interfaces bonded to a bond0 interface.

This can be changed by the various network types:

**type: hybird**
In this mode all odd numbered physical ports are unbonded from bond0, and made available for assignment to a vlan. In the example above eth1 is assigned to two layer2 vlans named 1000 and 1001. Corresponds to https://metal.equinix.com/developers/docs/layer2-networking/hybrid-unbonded-mode/

```yaml
  networkingConfiguration:
    type: "hybrid"
    interfaceConfiguration:
      - name: eth1
        vlanIDS:
          - "1000"
          - "1001"
```

**type: hybrid-bonded**
In this mode the bond0 interface is assigned to the vlans. Corresponds to https://metal.equinix.com/developers/docs/layer2-networking/hybrid-bonded-mode/

```yaml
  networkingConfiguration:
    type: "hybrid-bonded"
    interfaceConfiguration:
      - name: bond0
        vlanIDS:
          - "1000"
          - "1001"
```


**type: layer2-bonded**
In this mode, all interfaces are converted to layer2 networking and bonded. The bond0 interface can now be assigned to layer2 vlans.

```yaml
  networkingConfiguration:
    type: "layer2-bonded"
    interfaceConfiguration:
      - name: bond0
        vlanIDS:
          - "1000"
          - "1001"
```


**type: layer2-individual**
In this mode, all interfaces are converted to layer2 networking.Now each individual interface can be assigned to layer2 vlans.

```yaml
  networkingConfiguration:
    type: "layer2-individual"
    interfaceConfiguration:
      - name: eth0
        vlanIDS:
          - "1000"
      - name: eth1
        vlanIDS:
          - "1000"
          - "1001"
```

### InstancePool Management
The operator watches the node events and can replace nodes by replacing unhealthy nodes.

If an InstancePool Spec contains a value for `nodeCleanupWaitInterval: 5m` then nodes managed by the operator which are unhealthy for more than the specified duration are replaced by the operator