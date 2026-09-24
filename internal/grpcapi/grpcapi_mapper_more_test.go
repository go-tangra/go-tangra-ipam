package grpcapi

import (
	"testing"
	"time"

	ipamv1 "github.com/go-tangra/go-tangra-ipam/sdk/v4/api/proto/ipam/v1"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestTimeHelpers exercises unix/unixPtr/unixTime including the zero/nil branches.
func TestTimeHelpers(t *testing.T) {
	if got := unix(time.Time{}); got != 0 {
		t.Fatalf("unix(zero) = %d, want 0", got)
	}
	ts := time.Unix(1700000000, 0).UTC()
	if got := unix(ts); got != 1700000000 {
		t.Fatalf("unix(ts) = %d", got)
	}
	if got := unixPtr(nil); got != 0 {
		t.Fatalf("unixPtr(nil) = %d, want 0", got)
	}
	zero := time.Time{}
	if got := unixPtr(&zero); got != 0 {
		t.Fatalf("unixPtr(&zero) = %d, want 0", got)
	}
	if got := unixPtr(&ts); got != 1700000000 {
		t.Fatalf("unixPtr(&ts) = %d", got)
	}
	if got := unixTime(0); !got.IsZero() {
		t.Fatalf("unixTime(0) not zero: %v", got)
	}
	if got := unixTime(1700000000); got.Unix() != 1700000000 {
		t.Fatalf("unixTime round trip: %v", got)
	}
}

// TestSubnetStatusEnum round-trips every subnet status in both directions.
func TestSubnetStatusEnum(t *testing.T) {
	strs := []string{store.SubnetActive, store.SubnetReserved, store.SubnetDeprecated, store.SubnetDeleted}
	for _, s := range strs {
		pb := subnetStatusToPB(s)
		if pb == ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED {
			t.Fatalf("subnetStatusToPB(%q) unspecified", s)
		}
		if back := subnetStatusFromPB(pb); back != s {
			t.Fatalf("subnet round trip %q -> %v -> %q", s, pb, back)
		}
	}
	if subnetStatusToPB("bogus") != ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED {
		t.Fatal("unknown subnet status not unspecified")
	}
	if subnetStatusFromPB(ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified subnet status not empty")
	}
}

func TestIPStatusEnum(t *testing.T) {
	strs := []string{store.IPActive, store.IPReserved, store.IPDHCP, store.IPDeprecated, store.IPOffline}
	for _, s := range strs {
		pb := ipStatusToPB(s)
		if pb == ipamv1.IpStatus_IP_STATUS_UNSPECIFIED {
			t.Fatalf("ipStatusToPB(%q) unspecified", s)
		}
		if back := ipStatusFromPB(pb); back != s {
			t.Fatalf("ip status round trip %q -> %q", s, back)
		}
	}
	if ipStatusToPB("bogus") != ipamv1.IpStatus_IP_STATUS_UNSPECIFIED {
		t.Fatal("unknown ip status")
	}
	if ipStatusFromPB(ipamv1.IpStatus_IP_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified ip status")
	}
}

func TestAddressTypeEnum(t *testing.T) {
	strs := []string{store.AddrHost, store.AddrGateway, store.AddrBroadcast, store.AddrNetwork, store.AddrVirtual, store.AddrAnycast}
	for _, s := range strs {
		pb := addressTypeToPB(s)
		if pb == ipamv1.AddressType_ADDRESS_TYPE_UNSPECIFIED {
			t.Fatalf("addressTypeToPB(%q) unspecified", s)
		}
		if back := addressTypeFromPB(pb); back != s {
			t.Fatalf("address type round trip %q -> %q", s, back)
		}
	}
	if addressTypeToPB("bogus") != ipamv1.AddressType_ADDRESS_TYPE_UNSPECIFIED {
		t.Fatal("unknown address type")
	}
	if addressTypeFromPB(ipamv1.AddressType_ADDRESS_TYPE_UNSPECIFIED) != "" {
		t.Fatal("unspecified address type")
	}
}

func TestDeviceTypeEnum(t *testing.T) {
	strs := []string{
		store.DevServer, store.DevVM, store.DevRouter, store.DevSwitch, store.DevFirewall,
		store.DevLoadBalancer, store.DevAccessPoint, store.DevStorage, store.DevPrinter,
		store.DevPhone, store.DevWorkstation, store.DevContainer, store.DevOther,
	}
	for _, s := range strs {
		pb := deviceTypeToPB(s)
		if pb == ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED {
			t.Fatalf("deviceTypeToPB(%q) unspecified", s)
		}
		if back := deviceTypeFromPB(pb); back != s {
			t.Fatalf("device type round trip %q -> %q", s, back)
		}
	}
	if deviceTypeToPB("bogus") != ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED {
		t.Fatal("unknown device type")
	}
	if deviceTypeFromPB(ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED) != "" {
		t.Fatal("unspecified device type")
	}
}

func TestDeviceStatusEnum(t *testing.T) {
	strs := []string{
		store.DevStActive, store.DevStPlanned, store.DevStStaged, store.DevStDecommissioned,
		store.DevStOffline, store.DevStFailed, store.DevStAvailable,
	}
	for _, s := range strs {
		pb := deviceStatusToPB(s)
		if pb == ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED {
			t.Fatalf("deviceStatusToPB(%q) unspecified", s)
		}
		if back := deviceStatusFromPB(pb); back != s {
			t.Fatalf("device status round trip %q -> %q", s, back)
		}
	}
	if deviceStatusToPB("bogus") != ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED {
		t.Fatal("unknown device status")
	}
	if deviceStatusFromPB(ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified device status")
	}
}

func TestVlanStatusEnum(t *testing.T) {
	strs := []string{store.VlanActive, store.VlanReserved, store.VlanDeprecated}
	for _, s := range strs {
		pb := vlanStatusToPB(s)
		if pb == ipamv1.VlanStatus_VLAN_STATUS_UNSPECIFIED {
			t.Fatalf("vlanStatusToPB(%q) unspecified", s)
		}
		if back := vlanStatusFromPB(pb); back != s {
			t.Fatalf("vlan status round trip %q -> %q", s, back)
		}
	}
	if vlanStatusToPB("bogus") != ipamv1.VlanStatus_VLAN_STATUS_UNSPECIFIED {
		t.Fatal("unknown vlan status")
	}
	if vlanStatusFromPB(ipamv1.VlanStatus_VLAN_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified vlan status")
	}
}

func TestLocationTypeEnum(t *testing.T) {
	strs := []string{
		store.LocRegion, store.LocCountry, store.LocCity, store.LocDatacenter, store.LocBuilding,
		store.LocFloor, store.LocRoom, store.LocRack, store.LocSite, store.LocBranch,
	}
	for _, s := range strs {
		pb := locationTypeToPB(s)
		if pb == ipamv1.LocationType_LOCATION_TYPE_UNSPECIFIED {
			t.Fatalf("locationTypeToPB(%q) unspecified", s)
		}
		if back := locationTypeFromPB(pb); back != s {
			t.Fatalf("location type round trip %q -> %q", s, back)
		}
	}
	if locationTypeToPB("bogus") != ipamv1.LocationType_LOCATION_TYPE_UNSPECIFIED {
		t.Fatal("unknown location type")
	}
	if locationTypeFromPB(ipamv1.LocationType_LOCATION_TYPE_UNSPECIFIED) != "" {
		t.Fatal("unspecified location type")
	}
}

func TestLocationStatusEnum(t *testing.T) {
	strs := []string{store.LocStActive, store.LocStPlanned, store.LocStDecommissioned}
	for _, s := range strs {
		pb := locationStatusToPB(s)
		if pb == ipamv1.LocationStatus_LOCATION_STATUS_UNSPECIFIED {
			t.Fatalf("locationStatusToPB(%q) unspecified", s)
		}
		if back := locationStatusFromPB(pb); back != s {
			t.Fatalf("location status round trip %q -> %q", s, back)
		}
	}
	if locationStatusToPB("bogus") != ipamv1.LocationStatus_LOCATION_STATUS_UNSPECIFIED {
		t.Fatal("unknown location status")
	}
	if locationStatusFromPB(ipamv1.LocationStatus_LOCATION_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified location status")
	}
}

func TestMemberTypeEnum(t *testing.T) {
	strs := []string{store.MemberAddress, store.MemberRange, store.MemberSubnet}
	for _, s := range strs {
		pb := memberTypeToPB(s)
		if pb == ipamv1.MemberType_MEMBER_TYPE_UNSPECIFIED {
			t.Fatalf("memberTypeToPB(%q) unspecified", s)
		}
		if back := memberTypeFromPB(pb); back != s {
			t.Fatalf("member type round trip %q -> %q", s, back)
		}
	}
	if memberTypeToPB("bogus") != ipamv1.MemberType_MEMBER_TYPE_UNSPECIFIED {
		t.Fatal("unknown member type")
	}
	if memberTypeFromPB(ipamv1.MemberType_MEMBER_TYPE_UNSPECIFIED) != "" {
		t.Fatal("unspecified member type")
	}
}

func TestGroupStatusEnum(t *testing.T) {
	strs := []string{store.GroupActive, store.GroupInactive}
	for _, s := range strs {
		pb := groupStatusToPB(s)
		if pb == ipamv1.GroupStatus_GROUP_STATUS_UNSPECIFIED {
			t.Fatalf("groupStatusToPB(%q) unspecified", s)
		}
		if back := groupStatusFromPB(pb); back != s {
			t.Fatalf("group status round trip %q -> %q", s, back)
		}
	}
	if groupStatusToPB("bogus") != ipamv1.GroupStatus_GROUP_STATUS_UNSPECIFIED {
		t.Fatal("unknown group status")
	}
	if groupStatusFromPB(ipamv1.GroupStatus_GROUP_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified group status")
	}
}

func TestScanStatusEnum(t *testing.T) {
	strs := []string{store.ScanPending, store.ScanScanning, store.ScanCompleted, store.ScanFailed, store.ScanCancelled}
	for _, s := range strs {
		pb := scanStatusToPB(s)
		if pb == ipamv1.ScanStatus_SCAN_STATUS_UNSPECIFIED {
			t.Fatalf("scanStatusToPB(%q) unspecified", s)
		}
		if back := scanStatusFromPB(pb); back != s {
			t.Fatalf("scan status round trip %q -> %q", s, back)
		}
	}
	if scanStatusToPB("bogus") != ipamv1.ScanStatus_SCAN_STATUS_UNSPECIFIED {
		t.Fatal("unknown scan status")
	}
	if scanStatusFromPB(ipamv1.ScanStatus_SCAN_STATUS_UNSPECIFIED) != "" {
		t.Fatal("unspecified scan status")
	}
}

func TestScanTriggerEnum(t *testing.T) {
	if scanTriggerToPB(store.TriggerAuto) != ipamv1.ScanTrigger_SCAN_TRIGGER_AUTO {
		t.Fatal("trigger auto")
	}
	if scanTriggerToPB(store.TriggerManual) != ipamv1.ScanTrigger_SCAN_TRIGGER_MANUAL {
		t.Fatal("trigger manual")
	}
	if scanTriggerToPB("bogus") != ipamv1.ScanTrigger_SCAN_TRIGGER_UNSPECIFIED {
		t.Fatal("unknown trigger")
	}
}

func TestPowerActionToStore(t *testing.T) {
	cases := map[ipamv1.PowerAction]string{
		ipamv1.PowerAction_POWER_ACTION_ON:    store.PowerOn,
		ipamv1.PowerAction_POWER_ACTION_OFF:   store.PowerOff,
		ipamv1.PowerAction_POWER_ACTION_CYCLE: store.PowerCycle,
		ipamv1.PowerAction_POWER_ACTION_RESET: store.PowerReset,
		ipamv1.PowerAction_POWER_ACTION_SOFT:  store.PowerSoft,
		ipamv1.PowerAction_POWER_ACTION_DIAG:  store.PowerDiag,
	}
	for pb, want := range cases {
		if got := powerActionToStore(pb); got != want {
			t.Fatalf("powerActionToStore(%v) = %q, want %q", pb, got, want)
		}
	}
	if powerActionToStore(ipamv1.PowerAction_POWER_ACTION_UNSPECIFIED) != "" {
		t.Fatal("unspecified power action not empty")
	}
}

// TestEntityFromPBNil covers the nil-guard branches of the *FromPB mappers.
func TestEntityFromPBNil(t *testing.T) {
	if got := subnetFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("subnetFromPB(nil)")
	}
	if got := addressFromPB(nil); got.ID != "" || got.Address != "" {
		t.Fatal("addressFromPB(nil)")
	}
	if got := deviceFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("deviceFromPB(nil)")
	}
	if got := deviceInterfaceFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("deviceInterfaceFromPB(nil)")
	}
	if got := vlanFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("vlanFromPB(nil)")
	}
	if got := locationFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("locationFromPB(nil)")
	}
	if got := ipGroupFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("ipGroupFromPB(nil)")
	}
	if got := ipGroupMemberFromPB(nil); got.ID != "" || got.Value != "" {
		t.Fatal("ipGroupMemberFromPB(nil)")
	}
	if got := hostGroupFromPB(nil); got.ID != "" || got.Name != "" {
		t.Fatal("hostGroupFromPB(nil)")
	}
	if got := dnsConfigFromPB(nil); got.ID != "" || len(got.DNSServers) != 0 {
		t.Fatal("dnsConfigFromPB(nil)")
	}
}

// TestEntityRoundTrips exercises the populated entity mappers in both directions,
// including nested/repeated fields and the *ToPB helpers that stay 0% otherwise.
func TestEntityRoundTrips(t *testing.T) {
	last := time.Unix(1700000123, 0).UTC()

	// address with LastSeen/LeaseExpiry set to exercise the pointer branches.
	addr := store.IPAddress{
		ID: "a1", TenantID: tenant, Address: "10.0.0.5", SubnetID: "s1", Hostname: "h",
		MACAddress: "aa:bb", Description: "d", DeviceID: "dev", InterfaceName: "eth0",
		Status: store.IPActive, AddressType: store.AddrHost, IsPrimary: true, PTRRecord: "ptr",
		DNSName: "dns", HasReverseDNS: true, Note: "n", Tags: map[string]string{"env": "prod"},
		LastSeen: &last, LeaseExpiry: &last,
	}
	pbAddr := addressToPB(addr)
	back := addressFromPB(pbAddr)
	if back.Address != addr.Address || back.Status != store.IPActive || back.LastSeen == nil || back.LeaseExpiry == nil {
		t.Fatalf("address round trip: %+v", back)
	}
	if back.LastSeen.Unix() != last.Unix() {
		t.Fatalf("address LastSeen: %v", back.LastSeen)
	}

	iface := store.DeviceInterface{
		ID: "i1", TenantID: tenant, DeviceID: "dev", Name: "eth0", MACAddress: "aa:bb",
		InterfaceType: "ethernet", Enabled: true, SpeedMbps: 1000, Description: "nic",
		IfIndex: 2, RemoteDeviceID: "rd", RemoteInterfaceID: "ri", RemotePortName: "Gi0/1",
		LinkSource: "lldp", LinkVlan: 100, LinkLastSeen: &last,
	}
	pbIface := deviceInterfaceToPB(iface)
	ifBack := deviceInterfaceFromPB(pbIface)
	if ifBack.Name != "eth0" || ifBack.SpeedMbps != 1000 || ifBack.LinkVlan != 100 {
		t.Fatalf("iface round trip: %+v", ifBack)
	}

	pkg := store.DevicePackage{
		ID: "p1", TenantID: tenant, DeviceID: "dev", Name: "openssl", CurrentVersion: "1.0",
		AvailableVersion: "1.1", NeedsUpdate: true, IsSecurityUpdate: true, PackageManager: "apt",
		Description: "crypto",
	}
	if pb := devicePackageToPB(pkg); pb.GetName() != "openssl" || !pb.GetNeedsUpdate() || !pb.GetIsSecurityUpdate() {
		t.Fatalf("devicePackageToPB: %+v", pb)
	}

	hg := store.HostGroup{
		ID: "hg1", TenantID: tenant, Name: "web", Description: "web tier",
		Status: store.GroupActive, Tags: map[string]string{"env": "prod"}, MemberCount: 3,
	}
	pbHG := hostGroupToPB(hg)
	hgBack := hostGroupFromPB(pbHG)
	if hgBack.Name != "web" || hgBack.Status != store.GroupActive {
		t.Fatalf("hostgroup round trip: %+v", hgBack)
	}

	hgm := store.HostGroupMember{
		ID: "m1", TenantID: tenant, HostGroupID: "hg1", DeviceID: "dev", Sequence: 1,
		DeviceName: "srv", DeviceType: store.DevServer, DeviceStatus: store.DevStActive,
		DevicePrimaryIP: "10.0.0.1",
	}
	if pb := hostGroupMemberToPB(hgm); pb.GetDeviceName() != "srv" || pb.GetDeviceType() != ipamv1.DeviceType_DEVICE_TYPE_SERVER {
		t.Fatalf("hostGroupMemberToPB: %+v", pb)
	}
	if got := hostGroupMembersToPB([]store.HostGroupMember{hgm}); len(got) != 1 {
		t.Fatalf("hostGroupMembersToPB len=%d", len(got))
	}

	gm := store.IPGroupMember{
		ID: "gm1", TenantID: tenant, IPGroupID: "g1", MemberType: store.MemberSubnet,
		Value: "10.0.0.0/24", Description: "net", Sequence: 2,
	}
	if got := ipGroupMembersToPB([]store.IPGroupMember{gm}); len(got) != 1 || got[0].GetValue() != "10.0.0.0/24" {
		t.Fatalf("ipGroupMembersToPB: %+v", got)
	}

	dns := store.DNSConfig{
		ID: "d1", TenantID: tenant, DNSServers: []string{"8.8.8.8"}, TimeoutMs: 500,
		UseSystemDNSFallback: true, ReverseDNSEnabled: true,
	}
	pbDNS := dnsConfigToPB(dns)
	dnsBack := dnsConfigFromPB(pbDNS)
	if len(dnsBack.DNSServers) != 1 || dnsBack.TimeoutMs != 500 || !dnsBack.ReverseDNSEnabled {
		t.Fatalf("dns round trip: %+v", dnsBack)
	}

	st := statsToPB(repo.Stats{TotalSubnets: 2, TotalAddresses: 10, UsedAddresses: 4})
	if st.GetSubnetsTotal() != 2 || st.GetAddressesTotal() != 10 {
		t.Fatalf("statsToPB: %+v", st)
	}
}
