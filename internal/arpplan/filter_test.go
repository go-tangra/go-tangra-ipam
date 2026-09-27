package arpplan

import (
	"fmt"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestClassifyMAC(t *testing.T) {
	network := map[string]bool{"4c:5e:0c:00:00:01": true}
	cases := map[string]string{
		"0A-5C-D2-F1-00-05":       "",
		"":                        store.ARPIgnoredInvalid,
		"zz":                      store.ARPIgnoredInvalid,
		"00:00:00:00:00:00":       store.ARPIgnoredInvalid,
		"00:11:22:33:44:55:66:77": store.ARPIgnoredInvalid,       // EUI-64
		"ff:ff:ff:ff:ff:ff":       store.ARPIgnoredMulticast,     // broadcast
		"01:00:5e:00:00:fb":       store.ARPIgnoredMulticast,     // IPv4 multicast
		"33:33:00:00:00:01":       store.ARPIgnoredMulticast,     // IPv6 multicast
		"00:00:5e:00:01:0a":       store.ARPIgnoredVirtualRouter, // VRRP IPv4
		"00:00:5e:00:02:0a":       store.ARPIgnoredVirtualRouter, // VRRP IPv6
		"00:00:0c:07:ac:01":       store.ARPIgnoredVirtualRouter, // HSRP v1
		"00:00:0c:9f:f0:01":       store.ARPIgnoredVirtualRouter, // HSRP v2 low
		"00:00:0c:9f:ff:ff":       store.ARPIgnoredVirtualRouter, // HSRP v2 high
		"00:00:0c:9f:e0:01":       "",                            // below the HSRP v2 range
		"00:00:0c:07:ab:01":       "",                            // not HSRP v1
		"00:00:5e:00:03:01":       "",                            // IANA, not VRRP
		"00:00:5e:01:01:01":       "",
		"4C:5E:0C:00:00:01":       store.ARPIgnoredNetworkDevice,
	}
	for in, want := range cases {
		mac, reason := classifyMAC(in, network)
		if reason != want {
			t.Errorf("%q: reason %q want %q", in, reason, want)
		}
		if want == "" && mac == "" {
			t.Errorf("%q: usable MAC not returned", in)
		}
		if want != "" && mac != "" {
			t.Errorf("%q: ignored MAC returned %q", in, mac)
		}
	}
}

// proxyInput: one MAC answering for n IPs of one subnet.
func proxyInput(n, threshold int) Input {
	in := baseInput()
	in.ProxyThreshold = threshold
	var es []snmp.ARPEntry
	for i := 1; i <= n; i++ {
		es = append(es, entry(fmt.Sprintf("10.0.0.%d", i), "0a:00:00:00:00:99"))
	}
	es = append(es, entry("10.0.0.200", "0a:00:00:00:00:01"))
	in.Observations = []Observation{{DeviceID: "fw", Entries: es}}
	return in
}

// SC-004: proxy-ARP and virtual-router MACs are applied to no address.
func TestProxyThresholdBoundary(t *testing.T) {
	p := Build(proxyInput(8, 8)) // exactly the threshold: applied
	if len(p.Ops) != 9 || p.Ignored[store.ARPIgnoredProxy] != 0 {
		t.Fatalf("at threshold: %d ops %v", len(p.Ops), p.Ignored)
	}
	p = Build(proxyInput(9, 8)) // threshold+1: none of its entries
	if len(p.Ops) != 1 || p.Ops[0].Address != "10.0.0.200" || p.Ignored[store.ARPIgnoredProxy] != 9 {
		t.Fatalf("above threshold: %+v %v", p.Ops, p.Ignored)
	}
	p = Build(proxyInput(20, 0)) // default threshold 8
	if len(p.Ops) != 1 || p.Ignored[store.ARPIgnoredProxy] != 20 {
		t.Fatalf("default threshold: %d ops %v", len(p.Ops), p.Ignored)
	}
	// The same IP repeated by several devices counts once.
	in := baseInput()
	in.ProxyThreshold = 2
	in.Observations = []Observation{
		{DeviceID: "r1", Entries: []snmp.ARPEntry{entry("10.0.0.1", "0a:00:00:00:00:01"), entry("10.0.0.2", "0a:00:00:00:00:01")}},
		{DeviceID: "r2", Entries: []snmp.ARPEntry{entry("10.0.0.1", "0a:00:00:00:00:01"), entry("10.0.0.2", "0a:00:00:00:00:01")}},
	}
	if p := Build(in); len(p.Ops) != 2 || p.Ignored[store.ARPIgnoredProxy] != 0 {
		t.Fatalf("repeated IPs: %+v %v", p.Ops, p.Ignored)
	}
}

func TestFiltersCountedByReason(t *testing.T) {
	in := baseInput()
	in.NetworkMACs = map[string]bool{"4c:5e:0c:00:00:01": true}
	in.ExcludedDevices = []string{"fw"}
	in.Addresses = []store.IPAddress{{ID: "a1", TenantID: "t1", Address: "10.0.0.1"}}
	in.Observations = []Observation{
		{DeviceID: "r1", Entries: []snmp.ARPEntry{
			entry("10.0.0.1", "00:00:5e:00:01:01"),
			entry("10.0.0.2", "00:00:0c:07:ac:02"),
			entry("10.0.0.3", "ff:ff:ff:ff:ff:ff"),
			entry("10.0.0.4", "01:00:5e:00:00:01"),
			entry("10.0.0.5", "00:00:00:00:00:00"),
			entry("10.0.0.6", "4c:5e:0c:00:00:01"),
			entry("10.0.0.7", "0a:00:00:00:00:07"),
		}},
		{DeviceID: "fw", Entries: []snmp.ARPEntry{entry("10.0.0.1", "0a:00:00:00:00:01"), entry("10.0.0.8", "0a:00:00:00:00:08")}},
	}
	p := Build(in)
	want := map[string]int{store.ARPIgnoredVirtualRouter: 2, store.ARPIgnoredMulticast: 2, store.ARPIgnoredInvalid: 1,
		store.ARPIgnoredNetworkDevice: 1, store.ARPIgnoredExcluded: 2}
	for k, v := range want {
		if p.Ignored[k] != v {
			t.Errorf("%s: %d want %d (%v)", k, p.Ignored[k], v, p.Ignored)
		}
	}
	if len(p.Ops) != 1 || p.Ops[0].Address != "10.0.0.7" {
		t.Fatalf("only the clean entry is applied: %+v", p.Ops)
	}
	for _, op := range p.Ops {
		if op.AddressID == "a1" {
			t.Fatal("virtual-router/excluded MAC applied to an address")
		}
	}
}
