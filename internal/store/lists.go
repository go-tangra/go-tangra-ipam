package store

import "github.com/go-tangra/go-tangra/v4/listquery"

// inetOf orders a text column holding an IP address or prefix in inet order
// (IPv4 before IPv6, numeric within a family, shorter prefix first on equal
// networks). A value that is not a valid inet maps to NULL and sorts last
// instead of failing the query: addresses and CIDRs are stored as text and
// older rows are not guaranteed to parse. ipam_try_inet (migration 0011) is
// IMMUTABLE so the expression matches the addresses_tenant_inet /
// subnets_tenant_inet indexes; the expression must stay textually identical
// to the indexed one. It is nullable, so these fields are not NotNull.
func inetOf(col string) string {
	return "ipam_try_inet(" + col + ")"
}

// List definitions of the IPAM tables (go-tangra specs/032-server-side-tables,
// contracts/sortable-fields.md "ipam"). Sort fields map to constant SQL
// expressions on real columns only; the memstore sorts the same public names
// in Go. VLAN, location and subnet sort on the referenced id (they group the
// rows; ordering by the referenced name would need a join per row).
//
// NotNull marks fields whose expression is a NOT NULL column (checked against
// the migrations): their ORDER BY carries no NULLS clause, so a plain
// (tenant_id, expr, id) btree serves both directions. Nullable columns (the
// inet expressions, last_seen, the referenced ids, the LEFT JOINed host-member
// device name) keep NULLS LAST.
var (
	// AddressList pages ipam_ip_addresses (unaliased: addrCols references the
	// table by name), also a device's addresses (GET /devices/{id}/addresses).
	// Default: address ascending in inet order.
	AddressList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"address":      {Expr: inetOf("address")},
			"hostname":     {Expr: "hostname", Text: true, NotNull: true},
			"mac":          {Expr: "mac_address", Text: true, NotNull: true},
			"status":       {Expr: "status", NotNull: true},
			"address_type": {Expr: "address_type", NotNull: true},
			"last_seen":    {Expr: "last_seen", DefaultDir: listquery.Desc},
			"created_at":   {Expr: "created_at", DefaultDir: listquery.Desc, NotNull: true},
		},
		Default: "address", TieBreak: "id",
	}
	// DeviceList pages ipam_devices.
	DeviceList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"name":         {Expr: "name", Text: true, NotNull: true},
			"device_type":  {Expr: "device_type", NotNull: true},
			"status":       {Expr: "status", NotNull: true},
			"manufacturer": {Expr: "manufacturer", Text: true, NotNull: true},
			"location":     {Expr: "location_id"},
			"created_at":   {Expr: "created_at", DefaultDir: listquery.Desc, NotNull: true},
		},
		Default: "name", TieBreak: "id",
	}
	// SubnetList pages ipam_subnets: cidr ascending in inet order by default.
	// Utilization is computed per row (not stored) and is not sortable.
	SubnetList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"cidr":     {Expr: inetOf("cidr")},
			"name":     {Expr: "name", Text: true, NotNull: true},
			"vlan":     {Expr: "vlan_id"},
			"location": {Expr: "location_id"},
			"status":   {Expr: "status", NotNull: true},
		},
		Default: "cidr", TieBreak: "id",
	}
	// VlanList pages ipam_vlans: VLAN number ascending by default.
	VlanList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"vlan_id": {Expr: "vlan_id", NotNull: true},
			"name":    {Expr: "name", Text: true, NotNull: true},
			"domain":  {Expr: "domain", Text: true, NotNull: true},
			"status":  {Expr: "status", NotNull: true},
		},
		Default: "vlan_id", TieBreak: "id",
	}
	// ScanList pages ipam_ip_scan_jobs: newest first by default.
	ScanList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"created_at": {Expr: "created_at", DefaultDir: listquery.Desc, NotNull: true},
			"status":     {Expr: "status", NotNull: true},
			"subnet":     {Expr: "subnet_id", NotNull: true},
		},
		Default: "created_at", TieBreak: "id",
	}
	// IPMemberList pages one IP group's members; "name" is the member value.
	IPMemberList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"sequence": {Expr: "sequence", NotNull: true},
			"name":     {Expr: "value", Text: true, NotNull: true},
		},
		Default: "sequence", TieBreak: "id",
	}
	// HostMemberList pages one host group's members (m: members, d: devices);
	// "name" is the member device's name.
	HostMemberList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"sequence": {Expr: "m.sequence", NotNull: true},
			"name":     {Expr: "d.name", Text: true},
		},
		Default: "sequence", TieBreak: "m.id",
	}
	// InterfaceList pages one device's interfaces.
	InterfaceList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"name": {Expr: "name", Text: true, NotNull: true},
		},
		Default: "name", TieBreak: "id",
	}
	// PackageList pages one device's packages.
	PackageList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"name":    {Expr: "name", Text: true, NotNull: true},
			"version": {Expr: "current_version", Text: true, NotNull: true},
		},
		Default: "name", TieBreak: "id",
	}
	// GuestList pages the guests one hypervisor reports (g: guests).
	GuestList = listquery.Spec{
		Fields: map[string]listquery.Field{
			"name": {Expr: "g.name", Text: true, NotNull: true},
		},
		Default: "name", TieBreak: "g.id",
	}
)

// ListRequest completes r with the Spec's defaults (a zero Request from an
// internal caller pages with the defaults); an invalid hand-built Request
// falls back to the defaults entirely.
func ListRequest(r listquery.Request, s listquery.Spec) listquery.Request {
	out, err := listquery.New(r.Page, r.PageSize, r.Sort, r.Order, s)
	if err != nil {
		out, _ = listquery.New(0, 0, "", "", s)
	}
	return out
}
