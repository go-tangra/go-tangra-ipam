package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestAddressMACProvenanceRoutes (022 T017): a MAC set through the API is
// manual; provenance, origin and link in the body are ignored.
func TestAddressMACProvenanceRoutes(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "srv", "10.20.0.0/24")
	w := f.req(t, "POST", p+"/ip-addresses", "admin", `{"subnet_id":"`+sid+`","address":"10.20.0.5","mac_address":"0A:5C:D2:F1:00:05",
		"mac_source":"arp","mac_source_device_id":"x","mac_conflict":"y","origin":"arp","link":{"switch_id":"s","port_id":"p","source":"lldp"}}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, w)
	if b["mac_source"] != "manual" || b["mac_address"] != "0a:5c:d2:f1:00:05" || b["origin"] != nil || b["link"] != nil ||
		b["mac_conflict"] != nil || b["mac_source_device_id"] != nil {
		t.Fatalf("create body %v", b)
	}
	id := b["id"].(string)
	w = f.req(t, "PUT", p+"/ip-addresses/"+id, "admin", `{"subnet_id":"`+sid+`","address":"10.20.0.5","mac_address":""}`)
	if b := decodeBody(t, w); w.Code != 200 || b["mac_source"] != nil || b["mac_address"] != nil {
		t.Fatalf("clear %d %v", w.Code, b)
	}
}

// TestAddressLinkRoutes (022 T024): the address link object, the addresses
// behind a switch port and the links of a device's bound addresses.
func TestAddressLinkRoutes(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	sid := f.createSubnet(t, "srv", "10.21.0.0/24")
	sw := store.Device{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa01", TenantID: apiTenant, Name: "MSW-RACK2", DeviceType: store.DevSwitch}
	host := store.Device{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa02", TenantID: apiTenant, Name: "ipmi-host"}
	for _, d := range []store.Device{sw, host} {
		if err := f.mem.CreateDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	port := store.DeviceInterface{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa03", TenantID: apiTenant, DeviceID: sw.ID, Name: "14"}
	if err := f.mem.CreateInterface(ctx, port); err != nil {
		t.Fatal(err)
	}
	a := store.IPAddress{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa04", TenantID: apiTenant, SubnetID: sid, Address: "10.21.0.7",
		Hostname: "ipmi-7", DeviceID: host.ID, MACAddress: "0a:5c:d2:f1:00:07", MACSource: store.MACSourceARP}
	if err := f.mem.CreateAddress(ctx, a); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	if err := f.mem.SetAddressLinks(ctx, apiTenant, []store.IPAddress{{ID: a.ID, Link: &store.AddressLink{SwitchID: sw.ID, PortID: port.ID,
		PortName: "14", VLAN: 30, Source: store.LinkSNMPFDB, LastSeen: &seen}}}, nil); err != nil {
		t.Fatal(err)
	}
	w := f.req(t, "GET", p+"/ip-addresses/"+a.ID, "user", "")
	link, _ := decodeBody(t, w)["link"].(map[string]any)
	if w.Code != 200 || link["switch_name"] != "MSW-RACK2" || link["port_name"] != "14" || link["vlan"] != float64(30) ||
		link["source"] != "snmp_fdb" || link["last_seen"] == nil {
		t.Fatalf("address link %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+sw.ID+"/interfaces", "user", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"behind_addresses":[{"address_id":"`+a.ID+`","address":"10.21.0.7","hostname":"ipmi-7"}]`) {
		t.Fatalf("behind addresses %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+host.ID+"/addresses", "user", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"switch_name":"MSW-RACK2"`) {
		t.Fatalf("device addresses %d %s", w.Code, w.Body)
	}
}
