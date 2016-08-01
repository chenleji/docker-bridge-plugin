package bridge

import (
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/iptables"
	"github.com/docker/libcontainer/netlink"
)

//  setupBridge If bridge does not exist create it.
func (d *Driver) initBridge(id string) error {
	bridgeName := d.networks[id].BridgeName
	// Add bridge
	if err := netlink.NetworkLinkAdd(bridgeName, "bridge"); err != nil {
		log.Errorf("error creating linux bridge [ %s ] : [ %s ]", bridgeName, err)
		return err
	}

	retries := 3
	found := false
	for i := 0; i < retries; i++ {
		if found = validateIface(bridgeName); found {
			break
		}
		log.Debugf("A link for the linux bridge named [ %s ] not found, retrying in 2 seconds", bridgeName)
		time.Sleep(2 * time.Second)
	}
	if found == false {
		return fmt.Errorf("Could not find a link for the linux bridge named %s", bridgeName)

	}

	bridgeMode := d.networks[id].Mode
	switch bridgeMode {
	case modeNAT:
		{
			gatewayIP := d.networks[id].Gateway + "/" + d.networks[id].GatewayMask
			if err := setInterfaceIP(bridgeName, gatewayIP); err != nil {
				log.Debugf("Error assigning address: %s on bridge: %s with an error of: %s", gatewayIP, bridgeName, err)
			}

			// Validate that the IPAddress is there!
			_, err := getIfaceAddr(bridgeName)
			if err != nil {
				log.Fatalf("No IP address found on bridge %s", bridgeName)
				return err
			}

			// Add NAT rules for iptables
			if err = addNatOut(gatewayIP, bridgeName); err != nil {
				log.Fatalf("Could not set NAT rules for bridge %s", bridgeName)
				return err
			}
		}

	case modeFlat:
		{
			//ToDo: Add NIC to the bridge
		}
	}

	// Bring the bridge up
	err := interfaceUp(bridgeName)
	if err != nil {
		log.Warnf("Error enabling bridge: [ %s ]", err)
		return err
	}

	return nil
}

// deleteBridge deletes the linux bridge
func deleteBridge(bridgeName string) error {
	if err := netlink.NetworkLinkDel(bridgeName); err != nil {
		log.Errorf("error delete linux bridge [ %s ] : [ %s ]", bridgeName, err)
		return err
	}

	log.Debugf("delete bridge succesful")
	return nil
}

// todo: reconcile with what libnetwork does and port mappings
func addNatOut(cidr string, intfName string) error {
	masquerade := []string{
		"POSTROUTING", "-t", "nat",
		"!", "-o", intfName,
		"-s", cidr,
		"-j", "MASQUERADE",
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
		incl := append([]string{"-I"}, masquerade...)
		if output, err := iptables.Raw(incl...); err != nil {
			return err
		} else if len(output) > 0 {
			return &iptables.ChainError{
				Chain:  "POSTROUTING",
				Output: output,
			}
		}
	}
	return nil
}

func delNatOut(cidr string, intfName string) error {
	masquerade := []string{
		"POSTROUTING", "-t", "nat",
		"!", "-o", intfName,
		"-s", cidr,
		"-j", "MASQUERADE",
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
		log.Errorln("Can't find NAT rule in POSTROUTING chain!")
	}

	incl := append([]string{"-D"}, masquerade...)
	if output, err := iptables.Raw(incl...); err != nil {
		return err
	} else if len(output) > 0 {
		return &iptables.ChainError{
			Chain:  "POSTROUTING",
			Output: output,
		}
	}

	return nil
}

func addFipDnat(fipStr string, lipStr string, intfName string) error {
	masquerade := []string{
		"DOCKER", "-t", "nat",
		"-d", fipStr,
		"!", "-i", intfName,
		"-j", "DNAT", "--to-destination", lipStr,
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
		incl := append([]string{"-I"}, masquerade...)
		if output, err := iptables.Raw(incl...); err != nil {
			return err
		} else if len(output) > 0 {
			return &iptables.ChainError{
				Chain:  "DOCKER",
				Output: output,
			}
		}
	}
	return nil
}

func delFipDnat(fipStr string, lipStr string, intfName string) error {
	masquerade := []string{
		"DOCKER", "-t", "nat",
		"-d", fipStr,
		"!", "-i", intfName,
		"-j", "DNAT", "--to-destination", lipStr,
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
		log.Errorln("Can't find NAT rule in POSTROUTING chain!")
	}

	incl := append([]string{"-D"}, masquerade...)
	if output, err := iptables.Raw(incl...); err != nil {
		return err
	} else if len(output) > 0 {
		return &iptables.ChainError{
			Chain:  "DOCKER",
			Output: output,
		}
	}
	return nil
}

