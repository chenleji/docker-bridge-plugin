package bridge

import (
	"fmt"
	"net"
	"time"
	"os/exec"

	log "github.com/Sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"strings"
)

// Generate a mac addr
func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}

// Return the IPv4 address of a network interface
func getIfaceAddr(name string) (*net.IPNet, error) {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := netlink.AddrList(iface, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("Interface %s has no IP addresses", name)
	}
	if len(addrs) > 1 {
		log.Infof("Interface [ %v ] has more than 1 IPv4 address. Defaulting to using [ %v ]\n", name, addrs[0].IP)
	}
	return addrs[0].IPNet, nil
}

// Set the IP addr of a netlink interface
func setInterfaceIP(name string, rawIP string) error {
	retries := 2
	var iface netlink.Link
	var err error
	for i := 0; i < retries; i++ {
		iface, err = netlink.LinkByName(name)
		if err == nil {
			break
		}
		log.Debugf("error retrieving new bridge netlink link [ %s ]... retrying", name)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("Abandoning retrieving the new bridge link from netlink, Run [ ip link ] to troubleshoot the error: %s", err)
		return err
	}
	ipNet, err := netlink.ParseIPNet(rawIP)
	if err != nil {
		return err
	}
	addr := &netlink.Addr{ipNet, "", 0, 0}
	return netlink.AddrAdd(iface, addr)
}

// Delete the IP addr of a netlink interface
func delInterfaceIP(name string, rawIP string) error {
	retries := 2
	var iface netlink.Link
	var err error
	for i := 0; i < retries; i++ {
		iface, err = netlink.LinkByName(name)
		if err == nil {
			break
		}
		log.Debugf("error retrieving netlink link [ %s ]... retrying", name)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("Abandoning retrieving the link from netlink, Run [ ip link ] to troubleshoot the error: %s", err)
		return err
	}
	ipNet, err := netlink.ParseIPNet(rawIP)
	if err != nil {
		return err
	}
	addr := &netlink.Addr{ipNet, "", 0, 0}
	if err := netlink.AddrDel(iface, addr); err != nil {
		log.Debugf("error delete addr [%s] for interface [%s]", rawIP, name)
	}
	return nil
}

// Increment an IP in a subnet
func ipIncrement(networkAddr net.IP) net.IP {
	for i := 15; i >= 0; i-- {
		b := networkAddr[i]
		if b < 255 {
			networkAddr[i] = b + 1
			for xi := i + 1; xi <= 15; xi++ {
				networkAddr[xi] = 0
			}
			break
		}
	}
	return networkAddr
}

// Check if a netlink interface exists in the default namespace
func validateIface(ifaceStr string) bool {
	_, err := net.InterfaceByName(ifaceStr)
	if err != nil {
		log.Debugf("The requested interface [ %s ] was not found on the host: %s", ifaceStr, err)
		return false
	}
	return true
}

func updateDefaultGW4Container(container string, ip string) (string, error) {
	log.Infoln("updateDefaultGW4Container")

	exec.Command("mkdir", "-p", "/var/run/netns").Run()
	pid, err := exec.Command("docker", "inspect", "-f", "'{{.State.Pid}}'", container).Output()
	if err != nil {
		log.Infoln("docker inspect error!", err)
		return "", err
	}

	srcFile := "/host/proc/"+strings.TrimSuffix(strings.TrimPrefix(string(pid), "'"), "'")+"/ns/net"
	dstFile := "/var/run/netns/"+container
	if err := exec.Command("ln", "-s", srcFile, dstFile).Run(); err != nil {
		log.Infoln("ln -s error!", err)
	}

	gateway, err := exec.Command("ip", "route", "|", "grep", "default", "|",
		"cut", "-d", "' '", "-f", "3").Output()
	if err != nil {
		log.Infoln("ip route | grep default | cut -d ' ' -f 3 error!", err)
		return "", err
	}
	log.Infof("=========gwteway:%s", string(gateway))

	if err := exec.Command("ip", "netns", "exec", container,
					"ip", "route", "del", "default").Run(); err != nil {
		log.Infoln("ip route del default error!", err)
	}

	if err := exec.Command("ip", "netns", "exec", container,"ip",
					"route", "add", "default", "via", ip).Run(); err != nil {
		log.Infoln("ip route add default via ip error!", err)
	}

	if err := exec.Command("rm", dstFile).Run(); err != nil {
		log.Infoln("rm link error!", err)
	}

	return string(gateway), nil
}