package bridge

import (
	"fmt"
	"strings"
	"net"

	log "github.com/Sirupsen/logrus"
	"github.com/gopher-net/dknet"
	"github.com/samalba/dockerclient"
	"github.com/vishvananda/netlink"
)

const (
	defaultRoute     = "0.0.0.0/0"
	brPortPrefix    = "br-veth0-"
	bridgePrefix     = "br-"
	containerEthName = "eth"

	mtuOption           = "bridge.mtu"
	modeOption          = "bridge.mode"
	bridgeNameOption    = "bridge.name"
	bindInterfaceOption = "bridge.bind_interface"

	modeNAT  = "nat"
	modeFlat = "flat"

	defaultMTU  = 1500
	defaultMode = modeNAT
)

var (
	validModes = map[string]bool{
		modeNAT:  true,
		modeFlat: true,
	}
)

type Driver struct {
	dknet.Driver
	dockerer
	networks map[string]*NetworkState
	endpoints map[string]*EndpointState

}

type EndpointState struct {
	Fip string
	Lip string
	FipIfName string
}

// NetworkState is filled in at network creation time
// it contains state that we wish to keep for each network
type NetworkState struct {
	BridgeName        string
	MTU               int
	Mode              string
	Gateway           string
	GatewayMask       string
	FlatBindInterface string
}

func (d *Driver) CreateNetwork(r *dknet.CreateNetworkRequest) error {
	log.Debugf("Create network request: %+v", r)

	bridgeName, err := getBridgeName(r)
	if err != nil {
		return err
	}

	mtu, err := getBridgeMTU(r)
	if err != nil {
		return err
	}

	mode, err := getBridgeMode(r)
	if err != nil {
		return err
	}

	gateway, mask, err := getGatewayIP(r)
	if err != nil {
		return err
	}

	bindInterface, err := getBindInterface(r)
	if err != nil {
		return err
	}

	ns := &NetworkState{
		BridgeName:        bridgeName,
		MTU:               mtu,
		Mode:              mode,
		Gateway:           gateway,
		GatewayMask:       mask,
		FlatBindInterface: bindInterface,
	}
	d.networks[r.NetworkID] = ns

	log.Debugf("Initializing bridge for network %s", r.NetworkID)
	if err := d.initBridge(r.NetworkID); err != nil {
		delete(d.networks, r.NetworkID)
		return err
	}
	return nil
}

func (d *Driver) DeleteNetwork(r *dknet.DeleteNetworkRequest) error {
	log.Debugf("Delete network request: %+v", r)
	bridgeName := d.networks[r.NetworkID].BridgeName

	// Delete NAT rules for bridge
	gatewayIP := d.networks[r.NetworkID].Gateway + "/" + d.networks[r.NetworkID].GatewayMask
	if err := delNatOut(gatewayIP, bridgeName); err != nil {
		log.Fatalf("Could not del NAT rules for bridge %s", bridgeName)
		return err
	}

	log.Debugf("Deleting Bridge %s", bridgeName)
	if err := deleteBridge(bridgeName); err != nil {
		log.Errorf("Deleting bridge %s failed: %s", bridgeName, err)
		return err
	}
	delete(d.networks, r.NetworkID)
	return nil
}

func (d *Driver) CreateEndpoint(r *dknet.CreateEndpointRequest) error {
	log.Debugf("Create endpoint request: %+v", r)
	localVethPair := vethPair(truncateID(r.EndpointID))
	if err := netlink.LinkAdd(localVethPair); err != nil {
		log.Errorf("failed to create the veth pair named: [ %v ] error: [ %s ] ", localVethPair, err)
		return err
	}
	// Bring the veth pair up
	err := netlink.LinkSetUp(localVethPair)
	if err != nil {
		log.Warnf("Error enabling  Veth local iface: [ %v ]", localVethPair)
		return err
	}

	bridgeName := d.networks[r.NetworkID].BridgeName
	link, _ := netlink.LinkByName(bridgeName)

	bridge := netlink.Bridge{}
	bridge.LinkAttrs = *(link.Attrs())

	if err := netlink.LinkSetMaster(localVethPair, &bridge); err != nil {
		log.Errorf("error attaching veth [ %s ] to bridge [ %s ]", localVethPair.Name, bridgeName)
		return err
	}

	log.Infof("Attached veth [ %s ] to bridge [ %s ]", localVethPair.Name, bridgeName)

	// Add floating IP to GW interface
	fip := "10.0.2.200/32"
	fipNet, err := netlink.ParseIPNet(fip)
	if err != nil {
		return err
	}
	routeList, err := netlink.RouteGet(fipNet.IP)
	if err != nil {
		return err
	}
	route := routeList[0]
	intf, err := net.InterfaceByIndex(route.LinkIndex)
	if err != nil {
		return err
	}
	setInterfaceIP(intf.Name, fip)

	// Add DNAT rules for floating IP
	fipStr := strings.Split(fip, "/")[0]
	lipStr := strings.Split(r.Interface.Address, "/")[0]
	d.endpoints[r.EndpointID] = &EndpointState{
		Fip: fipStr,
		FipIfName: intf.Name,
		Lip: lipStr,
	}

	if err = addFipDnat(fipStr, lipStr, bridgeName); err != nil {
		log.Fatalf("Could not set NAT rules for floating ip %s", fip)
		log.Fatalf("%s", err)
		return err
	}
	return nil
}

func (d *Driver) DeleteEndpoint(r *dknet.DeleteEndpointRequest) error {
	log.Debugf("Delete endpoint request: %+v", r)
	delete(d.endpoints, r.EndpointID)
	return nil
}

func (d *Driver) EndpointInfo(r *dknet.InfoRequest) (*dknet.InfoResponse, error) {
	res := &dknet.InfoResponse{
		Value: make(map[string]string),
	}
	return res, nil
}

func (d *Driver) Join(r *dknet.JoinRequest) (*dknet.JoinResponse, error) {
	// create and attach local name to the bridge
	localVethPair := vethPair(truncateID(r.EndpointID))

	updateDefaultGW4Container("1f9974015e01", d.endpoints[r.EndpointID].Lip)
	// SrcName gets renamed to DstPrefix + ID on the container iface
	res := &dknet.JoinResponse{
		InterfaceName: dknet.InterfaceName{
			SrcName:   localVethPair.PeerName,
			DstPrefix: containerEthName,
		},
		Gateway: d.networks[r.NetworkID].Gateway,
	}
	log.Debugf("Join endpoint %s:%s to %s", r.NetworkID, r.EndpointID, r.SandboxKey)
	return res, nil
}

func (d *Driver) Leave(r *dknet.LeaveRequest) error {
	log.Debugf("Leave request: %+v", r)
	localVethPair := vethPair(truncateID(r.EndpointID))
	portID := fmt.Sprintf(brPortPrefix + truncateID(r.EndpointID))
	bridgeName := d.networks[r.NetworkID].BridgeName

	// Delete DNAT for floating ip
	fip := d.endpoints[r.EndpointID].Fip
	lip := d.endpoints[r.EndpointID].Lip
	if err:= delFipDnat(fip, lip, bridgeName); err != nil {
		log.Errorf("Delete DNAT rule failed!")
		return err
	}

	// Delete floating ip on interface
	fipIfName := d.endpoints[r.EndpointID].FipIfName
	delInterfaceIP(fipIfName, fip+"/32")

	//
	if err:= netlink.LinkSetNoMaster(localVethPair); err != nil {
		log.Errorf("Port [ %s ] delete transaction failed on bridge [ %s ] due to: %s", portID, bridgeName, err)
		return err
	}

	if err := netlink.LinkDel(localVethPair); err != nil {
		log.Errorf("unable to delete veth on leave: %s", err)
	}
	log.Infof("Deleted port [ %s ] from bridge [ %s ]", portID, bridgeName)
	log.Debugf("Leave %s:%s", r.NetworkID, r.EndpointID)
	return nil
}

func NewDriver() (*Driver, error) {
	docker, err := dockerclient.NewDockerClient("unix:///var/run/docker.sock", nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	d := &Driver{
		dockerer: dockerer{
			client: docker,
		},
		networks: make(map[string]*NetworkState),
		endpoints: make(map[string]*EndpointState),
	}

	return d, nil
}

// Create veth pair. Peername is renamed to eth0 in the container
func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: brPortPrefix + suffix},
		PeerName:  "br-veth1-" + suffix,
	}
}

// Enable a netlink interface
func interfaceUp(name string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		log.Debugf("Error retrieving a link named [ %s ]", iface.Attrs().Name)
		return err
	}
	return netlink.LinkSetUp(iface)
}

func truncateID(id string) string {
	return id[:5]
}

func getBridgeMTU(r *dknet.CreateNetworkRequest) (int, error) {
	bridgeMTU := defaultMTU
	if r.Options != nil {
		if mtu, ok := r.Options[mtuOption].(int); ok {
			bridgeMTU = mtu
		}
	}
	return bridgeMTU, nil
}

func getBridgeName(r *dknet.CreateNetworkRequest) (string, error) {
	bridgeName := bridgePrefix + truncateID(r.NetworkID)
	if r.Options != nil {
		if name, ok := r.Options[bridgeNameOption].(string); ok {
			bridgeName = name
		}
	}
	return bridgeName, nil
}

func getBridgeMode(r *dknet.CreateNetworkRequest) (string, error) {
	bridgeMode := defaultMode
	if r.Options != nil {
		if mode, ok := r.Options[modeOption].(string); ok {
			if _, isValid := validModes[mode]; !isValid {
				return "", fmt.Errorf("%s is not a valid mode", mode)
			}
			bridgeMode = mode
		}
	}
	return bridgeMode, nil
}

func getGatewayIP(r *dknet.CreateNetworkRequest) (string, string, error) {
	// FIXME: Dear future self, I'm sorry for leaving you with this mess, but I want to get this working ASAP
	// This should be an array
	// We need to handle case where we have
	// a. v6 and v4 - dual stack
	// auxilliary address
	// multiple subnets on one network
	// also in that case, we'll need a function to determine the correct default gateway based on it's IP/Mask
	var gatewayIP string

	if len(r.IPv6Data) > 0 {
		if r.IPv6Data[0] != nil {
			if r.IPv6Data[0].Gateway != "" {
				gatewayIP = r.IPv6Data[0].Gateway
			}
		}
	}
	// Assumption: IPAM will provide either IPv4 OR IPv6 but not both
	// We may want to modify this in future to support dual stack
	if len(r.IPv4Data) > 0 {
		if r.IPv4Data[0] != nil {
			if r.IPv4Data[0].Gateway != "" {
				gatewayIP = r.IPv4Data[0].Gateway
			}
		}
	}

	if gatewayIP == "" {
		return "", "", fmt.Errorf("No gateway IP found")
	}
	parts := strings.Split(gatewayIP, "/")
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Cannot split gateway IP address")
	}
	return parts[0], parts[1], nil
}

func getBindInterface(r *dknet.CreateNetworkRequest) (string, error) {
	if r.Options != nil {
		if mode, ok := r.Options[bindInterfaceOption].(string); ok {
			return mode, nil
		}
	}
	// As bind interface is optional and has no default, don't return an error
	return "", nil
}
