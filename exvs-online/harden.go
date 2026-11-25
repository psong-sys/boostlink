package main

// Limits that keep a stranger on the internet from tying up or crashing the
// match server, and from using the relay to reach this machine's own network.

import (
	"net"
	"net/http"
	"time"

	"exvsonline/pb/mms"
)

const (
	maxMMSBody    = 1 << 20  // a matchmaking request is a few hundred bytes
	maxProxyBody  = 32 << 20 // card-server uploads from the game, with lots of headroom
	maxEntries    = 2048     // queued or matched players kept at once (matched ones linger ~2 min)
	maxGroups     = 3        // partners in one queue entry (2v2 needs 1)
	maxProps      = 32
	maxStrLen     = 128  // serials, location ids, IP strings
	maxPropValLen = 1024 // property values
)

// newServer is http.ListenAndServe with timeouts, so slow or half-open
// connections can't pile up.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // the proxy waits up to 30s on the card server
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

func clipStr(s *string, n int) *string {
	if s == nil || len(*s) <= n {
		return s
	}
	c := (*s)[:n]
	return &c
}

// sanitizeClient trims a queued player's details before they are sent on to
// other players' games, so one entry can't hand them oversized data.
func sanitizeClient(c *mms.Client) {
	if c == nil {
		return
	}
	c.PcbSerial = clipStr(c.PcbSerial, maxStrLen)
	c.LocId = clipStr(c.LocId, maxStrLen)
	c.SubnetId = clipStr(c.SubnetId, maxStrLen)
	c.GlobalIpaddress = clipStr(c.GlobalIpaddress, maxStrLen)
	c.LocalIpaddress = clipStr(c.LocalIpaddress, maxStrLen)
	if len(c.Property) > maxProps {
		c.Property = c.Property[:maxProps]
	}
	for _, p := range c.Property {
		if p != nil {
			p.PropertyValue = clipStr(p.PropertyValue, maxPropValLen)
		}
	}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isInternalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsPrivate() || cgnat.Contains(ip)
}

// turnPeerAllowed decides which addresses relay users may send to. Players
// only ever relay to each other's public addresses, so loopback, link-local
// (cloud metadata) and multicast are always refused. Private ranges are
// refused too unless the relay itself sits on a private network (a LAN or
// ZeroTier host). A relay on 127.0.0.1 is a local test and allows anything.
func turnPeerAllowed(relayIP net.IP) func(net.Addr, net.IP) bool {
	if relayIP.IsLoopback() {
		return func(net.Addr, net.IP) bool { return true }
	}
	privateRelay := relayIP.IsPrivate() || cgnat.Contains(relayIP)
	return func(_ net.Addr, peer net.IP) bool {
		if peer.IsLoopback() || peer.IsUnspecified() || peer.IsLinkLocalUnicast() || peer.IsLinkLocalMulticast() ||
			peer.IsMulticast() || peer.IsInterfaceLocalMulticast() {
			return false
		}
		if privateRelay {
			return true
		}
		return !isInternalIP(peer)
	}
}
