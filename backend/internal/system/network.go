package system

import (
	"fmt"
	"net"
)

// listNetworkInterfaces enumerates interfaces and their addresses using
// only net.Interfaces/net.Interface.Addrs - both fully cross-platform in
// the standard library. Every platform provider's NetworkInterfaces
// method delegates to this rather than reimplementing interface
// enumeration three times; only throughput counters (bytes sent/received,
// which need OS-specific sources - /proc/net/dev on Linux) require
// per-platform code, and are left for the metrics engine in Phase 4 since
// a rate needs two time-separated samples to mean anything.
func listNetworkInterfaces() ([]NetworkInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}

	out := make([]NetworkInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			// A single interface failing to report addresses (permission
			// issues on some virtual interfaces are the usual cause)
			// should not fail the whole listing.
			addrs = nil
		}

		addrStrings := make([]string, 0, len(addrs))
		for _, a := range addrs {
			addrStrings = append(addrStrings, a.String())
		}

		out = append(out, NetworkInterface{
			Name:         iface.Name,
			HardwareAddr: iface.HardwareAddr.String(),
			Addresses:    addrStrings,
			IsUp:         iface.Flags&net.FlagUp != 0,
			IsLoopback:   iface.Flags&net.FlagLoopback != 0,
		})
	}

	return out, nil
}
