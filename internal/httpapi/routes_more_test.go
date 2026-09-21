package httpapi

import "testing"

// randUUID is a well-formed UUIDv7-shaped id that never exists in a fresh store.
const randUUID = "018f0000-0000-7000-8000-0000000000aa"

// TestSubnetUpdateDelete covers subnet PUT, stats-by-id, overlap 422, DELETE
// with force and the not-found paths.
func TestSubnetUpdateDelete(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "net", "10.10.0.0/24")

	// Update (rename).
	if w := f.req(t, "PUT", p+"/subnets/"+sid, "admin",
		`{"name":"net-renamed","cidr":"10.10.0.0/24"}`); w.Code != 200 {
		t.Fatalf("subnet update: %d %s", w.Code, w.Body)
	}

	// Update a missing subnet -> 404.
	if w := f.req(t, "PUT", p+"/subnets/"+randUUID, "admin",
		`{"name":"x","cidr":"10.11.0.0/24"}`); w.Code != 404 {
		t.Fatalf("subnet update missing: want 404, got %d %s", w.Code, w.Body)
	}

	// Overlapping create without allow_overlap -> 422 validation.
	if w := f.req(t, "POST", p+"/subnets", "admin",
		`{"name":"dup","cidr":"10.10.0.0/25"}`); w.Code != 422 {
		t.Fatalf("overlap create: want 422, got %d %s", w.Code, w.Body)
	}
	// Overlapping create WITH allow_overlap=true -> 201.
	if w := f.req(t, "POST", p+"/subnets?allow_overlap=true", "admin",
		`{"name":"ok","cidr":"10.10.0.0/25"}`); w.Code != 201 {
		t.Fatalf("overlap allowed: want 201, got %d %s", w.Code, w.Body)
	}

	// Bad CIDR -> 422.
	if w := f.req(t, "POST", p+"/subnets", "admin",
		`{"name":"bad","cidr":"not-a-cidr"}`); w.Code != 422 {
		t.Fatalf("bad cidr: want 422, got %d %s", w.Code, w.Body)
	}

	// Delete with force.
	if w := f.req(t, "DELETE", p+"/subnets/"+sid+"?force=true", "admin", ""); w.Code != 204 {
		t.Fatalf("subnet delete force: want 204, got %d %s", w.Code, w.Body)
	}
	// Delete a missing subnet -> 404.
	if w := f.req(t, "DELETE", p+"/subnets/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("subnet delete missing: want 404, got %d %s", w.Code, w.Body)
	}
	// Stats for a missing subnet -> 404.
	if w := f.req(t, "GET", p+"/subnets/"+randUUID+"/stats", "admin", ""); w.Code != 404 {
		t.Fatalf("subnet stats missing: want 404, got %d %s", w.Code, w.Body)
	}
}

// TestAddressExtras covers create-in-subnet, address get/update/delete missing,
// find not-found and suggest.
func TestAddressExtras(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "addr-net", "10.20.0.0/24")

	// Explicit create.
	w := f.req(t, "POST", p+"/ip-addresses", "admin",
		`{"subnet_id":"`+sid+`","address":"10.20.0.5","hostname":"h1"}`)
	if w.Code != 201 {
		t.Fatalf("address create: %d %s", w.Code, w.Body)
	}
	aid, _ := decodeBody(t, w)["id"].(string)

	// Update.
	if w := f.req(t, "PUT", p+"/ip-addresses/"+aid, "admin",
		`{"subnet_id":"`+sid+`","address":"10.20.0.5","hostname":"h2"}`); w.Code != 200 {
		t.Fatalf("address update: %d %s", w.Code, w.Body)
	}
	// Get missing -> 404.
	if w := f.req(t, "GET", p+"/ip-addresses/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("address get missing: want 404, got %d", w.Code)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/ip-addresses/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("address delete missing: want 404, got %d", w.Code)
	}
	// Find an address that does not exist -> 404.
	if w := f.req(t, "GET", p+"/ip-addresses/find?address=10.20.0.222", "admin", ""); w.Code != 404 {
		t.Fatalf("find missing: want 404, got %d %s", w.Code, w.Body)
	}
	// Suggest with default count (no count param).
	if w := f.req(t, "GET", p+"/ip-addresses/suggest?subnet_id="+sid, "admin", ""); w.Code != 200 {
		t.Fatalf("suggest default: %d %s", w.Code, w.Body)
	}
}

// TestBulkAllocateExhaust covers bulk-allocate when count exceeds availability.
func TestBulkAllocateExhaust(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "tiny", "10.30.0.0/30") // 2 usable hosts
	w := f.req(t, "POST", p+"/ip-addresses/bulk-allocate", "admin",
		`{"subnet_id":"`+sid+`","count":10,"prefix":"host"}`)
	if w.Code != 507 {
		t.Fatalf("bulk over-allocate: want 507, got %d %s", w.Code, w.Body)
	}
}

// TestVlanUpdateDelete covers vlan PUT/DELETE, duplicate 409, invalid 422 and
// GET tree/subnets.
func TestVlanUpdateDelete(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":200,"name":"v200"}`)
	if w.Code != 201 {
		t.Fatalf("create vlan: %d %s", w.Code, w.Body)
	}
	vid, _ := decodeBody(t, w)["id"].(string)

	// Duplicate vlan_id -> 409 conflict.
	if w := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":200,"name":"dup"}`); w.Code != 409 {
		t.Fatalf("dup vlan: want 409, got %d %s", w.Code, w.Body)
	}
	// Out-of-range vlan_id -> 422.
	if w := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":0,"name":"bad"}`); w.Code != 422 {
		t.Fatalf("bad vlan: want 422, got %d %s", w.Code, w.Body)
	}
	// Update.
	if w := f.req(t, "PUT", p+"/vlans/"+vid, "admin", `{"vlan_id":200,"name":"v200-renamed"}`); w.Code != 200 {
		t.Fatalf("vlan update: %d %s", w.Code, w.Body)
	}
	// Update missing -> 404.
	if w := f.req(t, "PUT", p+"/vlans/"+randUUID, "admin", `{"vlan_id":201,"name":"x"}`); w.Code != 404 {
		t.Fatalf("vlan update missing: want 404, got %d", w.Code)
	}
	// Delete.
	if w := f.req(t, "DELETE", p+"/vlans/"+vid, "admin", ""); w.Code != 204 {
		t.Fatalf("vlan delete: %d %s", w.Code, w.Body)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/vlans/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("vlan delete missing: want 404, got %d", w.Code)
	}
}

// TestLocationUpdateDelete covers location PUT/DELETE, tree and 404s.
func TestLocationUpdateDelete(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/locations", "admin", `{"name":"dc","location_type":"datacenter"}`)
	if w.Code != 201 {
		t.Fatalf("create location: %d %s", w.Code, w.Body)
	}
	lid, _ := decodeBody(t, w)["id"].(string)

	// Missing name -> 422.
	if w := f.req(t, "POST", p+"/locations", "admin", `{"location_type":"rack"}`); w.Code != 422 {
		t.Fatalf("no-name location: want 422, got %d %s", w.Code, w.Body)
	}
	// Update.
	if w := f.req(t, "PUT", p+"/locations/"+lid, "admin",
		`{"name":"dc-renamed","location_type":"datacenter"}`); w.Code != 200 {
		t.Fatalf("location update: %d %s", w.Code, w.Body)
	}
	// Get missing -> 404.
	if w := f.req(t, "GET", p+"/locations/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("location get missing: want 404, got %d", w.Code)
	}
	// Update missing -> 404.
	if w := f.req(t, "PUT", p+"/locations/"+randUUID, "admin",
		`{"name":"x","location_type":"rack"}`); w.Code != 404 {
		t.Fatalf("location update missing: want 404, got %d", w.Code)
	}
	// Delete.
	if w := f.req(t, "DELETE", p+"/locations/"+lid, "admin", ""); w.Code != 204 {
		t.Fatalf("location delete: %d %s", w.Code, w.Body)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/locations/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("location delete missing: want 404, got %d", w.Code)
	}
}

// TestDeviceUpdateDelete covers device PUT/DELETE and 404s.
func TestDeviceUpdateDelete(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/devices", "admin", `{"name":"dev","device_type":"server"}`)
	if w.Code != 201 {
		t.Fatalf("create device: %d %s", w.Code, w.Body)
	}
	did, _ := decodeBody(t, w)["id"].(string)

	// Update.
	if w := f.req(t, "PUT", p+"/devices/"+did, "admin",
		`{"name":"dev-renamed","device_type":"server"}`); w.Code != 200 {
		t.Fatalf("device update: %d %s", w.Code, w.Body)
	}
	// Get missing -> 404.
	if w := f.req(t, "GET", p+"/devices/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("device get missing: want 404, got %d", w.Code)
	}
	// Update missing -> 404.
	if w := f.req(t, "PUT", p+"/devices/"+randUUID, "admin",
		`{"name":"x","device_type":"server"}`); w.Code != 404 {
		t.Fatalf("device update missing: want 404, got %d", w.Code)
	}
	// Delete with force.
	if w := f.req(t, "DELETE", p+"/devices/"+did+"?force=true", "admin", ""); w.Code != 204 {
		t.Fatalf("device delete: %d %s", w.Code, w.Body)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/devices/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("device delete missing: want 404, got %d", w.Code)
	}
}

// TestIPGroupCRUD covers ip-group update/delete, member update/remove, list,
// check-miss and validation.
func TestIPGroupCRUD(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/ip-groups", "admin", `{"name":"grp"}`)
	if w.Code != 201 {
		t.Fatalf("create ip-group: %d %s", w.Code, w.Body)
	}
	gid, _ := decodeBody(t, w)["id"].(string)

	// Missing name -> 422.
	if w := f.req(t, "POST", p+"/ip-groups", "admin", `{"description":"x"}`); w.Code != 422 {
		t.Fatalf("no-name group: want 422, got %d %s", w.Code, w.Body)
	}
	// Get group.
	if w := f.req(t, "GET", p+"/ip-groups/"+gid, "admin", ""); w.Code != 200 {
		t.Fatalf("ip-group get: %d %s", w.Code, w.Body)
	}
	// Update group.
	if w := f.req(t, "PUT", p+"/ip-groups/"+gid, "admin", `{"name":"grp-renamed"}`); w.Code != 200 {
		t.Fatalf("ip-group update: %d %s", w.Code, w.Body)
	}

	// Add a member.
	w = f.req(t, "POST", p+"/ip-groups/"+gid+"/members", "admin",
		`{"member_type":"address","value":"10.50.0.9"}`)
	if w.Code != 201 {
		t.Fatalf("add member: %d %s", w.Code, w.Body)
	}
	mid, _ := decodeBody(t, w)["id"].(string)

	// Invalid member value -> 422.
	if w := f.req(t, "POST", p+"/ip-groups/"+gid+"/members", "admin",
		`{"member_type":"address","value":"not-an-ip"}`); w.Code != 422 {
		t.Fatalf("bad member: want 422, got %d %s", w.Code, w.Body)
	}
	// List members.
	if w := f.req(t, "GET", p+"/ip-groups/"+gid+"/members", "admin", ""); w.Code != 200 {
		t.Fatalf("list members: %d %s", w.Code, w.Body)
	}
	// Update member.
	if w := f.req(t, "PUT", p+"/ip-groups/"+gid+"/members/"+mid, "admin",
		`{"member_type":"address","value":"10.50.0.10"}`); w.Code != 200 {
		t.Fatalf("update member: %d %s", w.Code, w.Body)
	}
	// Remove member.
	if w := f.req(t, "DELETE", p+"/ip-groups/"+gid+"/members/"+mid, "admin", ""); w.Code != 204 {
		t.Fatalf("remove member: %d %s", w.Code, w.Body)
	}

	// Check an ip that matches nothing -> 200 with empty list.
	w = f.req(t, "GET", p+"/ip-groups/check?ip=192.168.254.254", "admin", "")
	if w.Code != 200 {
		t.Fatalf("check miss: %d %s", w.Code, w.Body)
	}
	if m, _ := decodeBody(t, w)["matching_groups"].([]any); len(m) != 0 {
		t.Fatalf("check miss should be empty: %s", w.Body)
	}

	// Delete group.
	if w := f.req(t, "DELETE", p+"/ip-groups/"+gid, "admin", ""); w.Code != 204 {
		t.Fatalf("delete group: %d %s", w.Code, w.Body)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/ip-groups/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("delete group missing: want 404, got %d", w.Code)
	}
}

// TestHostGroupCRUD covers host-group create/get/update/delete, member
// add/list/remove, device host-groups and validation.
func TestHostGroupCRUD(t *testing.T) {
	f := newAPI(t)

	// Create a device to add as a member.
	dw := f.req(t, "POST", p+"/devices", "admin", `{"name":"hg-dev","device_type":"server"}`)
	did, _ := decodeBody(t, dw)["id"].(string)

	// List (empty) + create.
	if w := f.req(t, "GET", p+"/host-groups", "admin", ""); w.Code != 200 {
		t.Fatalf("host-groups list: %d %s", w.Code, w.Body)
	}
	w := f.req(t, "POST", p+"/host-groups", "admin", `{"name":"web"}`)
	if w.Code != 201 {
		t.Fatalf("create host-group: %d %s", w.Code, w.Body)
	}
	hid, _ := decodeBody(t, w)["id"].(string)

	// Missing name -> 422.
	if w := f.req(t, "POST", p+"/host-groups", "admin", `{"description":"x"}`); w.Code != 422 {
		t.Fatalf("no-name host-group: want 422, got %d %s", w.Code, w.Body)
	}
	// Get + update.
	if w := f.req(t, "GET", p+"/host-groups/"+hid, "admin", ""); w.Code != 200 {
		t.Fatalf("host-group get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/host-groups/"+hid, "admin", `{"name":"web-renamed"}`); w.Code != 200 {
		t.Fatalf("host-group update: %d %s", w.Code, w.Body)
	}

	// Add a device member.
	w = f.req(t, "POST", p+"/host-groups/"+hid+"/members", "admin", `{"device_id":"`+did+`"}`)
	if w.Code != 201 {
		t.Fatalf("add host member: %d %s", w.Code, w.Body)
	}
	mid, _ := decodeBody(t, w)["id"].(string)

	// Missing device_id -> 422.
	if w := f.req(t, "POST", p+"/host-groups/"+hid+"/members", "admin", `{}`); w.Code != 422 {
		t.Fatalf("no device_id: want 422, got %d %s", w.Code, w.Body)
	}
	// List members.
	if w := f.req(t, "GET", p+"/host-groups/"+hid+"/members", "admin", ""); w.Code != 200 {
		t.Fatalf("list host members: %d %s", w.Code, w.Body)
	}
	// The device now reports the host-group.
	if w := f.req(t, "GET", p+"/devices/"+did+"/host-groups", "admin", ""); w.Code != 200 {
		t.Fatalf("device host-groups: %d %s", w.Code, w.Body)
	}
	// Remove member.
	if w := f.req(t, "DELETE", p+"/host-groups/"+hid+"/members/"+mid, "admin", ""); w.Code != 204 {
		t.Fatalf("remove host member: %d %s", w.Code, w.Body)
	}
	// Delete host-group.
	if w := f.req(t, "DELETE", p+"/host-groups/"+hid, "admin", ""); w.Code != 204 {
		t.Fatalf("delete host-group: %d %s", w.Code, w.Body)
	}
	// Delete missing -> 404.
	if w := f.req(t, "DELETE", p+"/host-groups/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("delete host-group missing: want 404, got %d", w.Code)
	}
}

// TestScanMissing covers scan get/cancel on a missing job -> 404.
func TestScanMissing(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/ip-scans/"+randUUID, "admin", ""); w.Code != 404 {
		t.Fatalf("scan get missing: want 404, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/ip-scans/"+randUUID+"/cancel", "admin", ""); w.Code != 404 {
		t.Fatalf("scan cancel missing: want 404, got %d %s", w.Code, w.Body)
	}
	// Starting a scan on a missing subnet -> 404.
	if w := f.req(t, "POST", p+"/subnets/"+randUUID+"/scan", "admin", `{}`); w.Code != 404 {
		t.Fatalf("scan start missing subnet: want 404, got %d %s", w.Code, w.Body)
	}
}

// TestMalformedBody covers the DecodeJSON malformed-body path through a route.
func TestMalformedBody(t *testing.T) {
	f := newAPI(t)
	// Unknown field is rejected by DisallowUnknownFields -> 400 malformed_body.
	if w := f.req(t, "POST", p+"/subnets", "admin", `{"bogus_field":true}`); w.Code != 400 {
		t.Fatalf("unknown field: want 400, got %d %s", w.Code, w.Body)
	}
	// Truncated JSON -> 400.
	if w := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":`); w.Code != 400 {
		t.Fatalf("truncated: want 400, got %d %s", w.Code, w.Body)
	}
}

// TestForbiddenPagesForUser confirms a plain user is refused the admin-only
// warden-secrets and power routes but the shape is the documented 403.
func TestUserForbiddenOOB(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	// Sensors and SEL are platform-admin only.
	if w := f.req(t, "GET", p+"/devices/"+did+"/sensors", "user", ""); w.Code != 403 {
		t.Fatalf("sensors (user): want 403, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sel", "user", ""); w.Code != 403 {
		t.Fatalf("sel (user): want 403, got %d %s", w.Code, w.Body)
	}
}
