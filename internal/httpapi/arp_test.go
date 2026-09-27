package httpapi

import (
	"testing"
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
