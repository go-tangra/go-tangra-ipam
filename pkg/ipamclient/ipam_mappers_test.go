package ipamclient

import (
	"testing"
	"time"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
)

func TestSubnetStatusRoundTrip(t *testing.T) {
	cases := []struct {
		s string
		e ipamv1.SubnetStatus
	}{
		{"active", ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE},
		{"reserved", ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED},
		{"deprecated", ipamv1.SubnetStatus_SUBNET_STATUS_DEPRECATED},
		{"deleted", ipamv1.SubnetStatus_SUBNET_STATUS_DELETED},
	}
	for _, c := range cases {
		if got := subnetStatusEnum(c.s); got != c.e {
			t.Errorf("subnetStatusEnum(%q)=%v want %v", c.s, got, c.e)
		}
		if got := subnetStatusString(c.e); got != c.s {
			t.Errorf("subnetStatusString(%v)=%q want %q", c.e, got, c.s)
		}
	}
	if got := subnetStatusEnum("bogus"); got != ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED {
		t.Errorf("subnetStatusEnum default=%v", got)
	}
	if got := subnetStatusString(ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED); got != "" {
		t.Errorf("subnetStatusString default=%q", got)
	}
}

func TestIpStatusRoundTrip(t *testing.T) {
	cases := []struct {
		s string
		e ipamv1.IpStatus
	}{
		{"active", ipamv1.IpStatus_IP_STATUS_ACTIVE},
		{"reserved", ipamv1.IpStatus_IP_STATUS_RESERVED},
		{"dhcp", ipamv1.IpStatus_IP_STATUS_DHCP},
		{"deprecated", ipamv1.IpStatus_IP_STATUS_DEPRECATED},
		{"offline", ipamv1.IpStatus_IP_STATUS_OFFLINE},
	}
	for _, c := range cases {
		if got := ipStatusEnum(c.s); got != c.e {
			t.Errorf("ipStatusEnum(%q)=%v want %v", c.s, got, c.e)
		}
		if got := ipStatusString(c.e); got != c.s {
			t.Errorf("ipStatusString(%v)=%q want %q", c.e, got, c.s)
		}
	}
	if got := ipStatusEnum("bogus"); got != ipamv1.IpStatus_IP_STATUS_UNSPECIFIED {
		t.Errorf("ipStatusEnum default=%v", got)
	}
	if got := ipStatusString(ipamv1.IpStatus_IP_STATUS_UNSPECIFIED); got != "" {
		t.Errorf("ipStatusString default=%q", got)
	}
}

func TestAddressTypeString(t *testing.T) {
	cases := []struct {
		e ipamv1.AddressType
		s string
	}{
		{ipamv1.AddressType_ADDRESS_TYPE_HOST, "host"},
		{ipamv1.AddressType_ADDRESS_TYPE_GATEWAY, "gateway"},
		{ipamv1.AddressType_ADDRESS_TYPE_BROADCAST, "broadcast"},
		{ipamv1.AddressType_ADDRESS_TYPE_NETWORK, "network"},
		{ipamv1.AddressType_ADDRESS_TYPE_VIRTUAL, "virtual"},
		{ipamv1.AddressType_ADDRESS_TYPE_ANYCAST, "anycast"},
		{ipamv1.AddressType_ADDRESS_TYPE_UNSPECIFIED, ""},
	}
	for _, c := range cases {
		if got := addressTypeString(c.e); got != c.s {
			t.Errorf("addressTypeString(%v)=%q want %q", c.e, got, c.s)
		}
	}
}

func TestDeviceTypeRoundTrip(t *testing.T) {
	cases := []struct {
		s string
		e ipamv1.DeviceType
	}{
		{"server", ipamv1.DeviceType_DEVICE_TYPE_SERVER},
		{"vm", ipamv1.DeviceType_DEVICE_TYPE_VM},
		{"router", ipamv1.DeviceType_DEVICE_TYPE_ROUTER},
		{"switch", ipamv1.DeviceType_DEVICE_TYPE_SWITCH},
		{"firewall", ipamv1.DeviceType_DEVICE_TYPE_FIREWALL},
		{"load_balancer", ipamv1.DeviceType_DEVICE_TYPE_LOAD_BALANCER},
		{"access_point", ipamv1.DeviceType_DEVICE_TYPE_ACCESS_POINT},
		{"storage", ipamv1.DeviceType_DEVICE_TYPE_STORAGE},
		{"printer", ipamv1.DeviceType_DEVICE_TYPE_PRINTER},
		{"phone", ipamv1.DeviceType_DEVICE_TYPE_PHONE},
		{"workstation", ipamv1.DeviceType_DEVICE_TYPE_WORKSTATION},
		{"container", ipamv1.DeviceType_DEVICE_TYPE_CONTAINER},
		{"other", ipamv1.DeviceType_DEVICE_TYPE_OTHER},
	}
	for _, c := range cases {
		if got := deviceTypeEnum(c.s); got != c.e {
			t.Errorf("deviceTypeEnum(%q)=%v want %v", c.s, got, c.e)
		}
		if got := deviceTypeString(c.e); got != c.s {
			t.Errorf("deviceTypeString(%v)=%q want %q", c.e, got, c.s)
		}
	}
	if got := deviceTypeEnum("bogus"); got != ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED {
		t.Errorf("deviceTypeEnum default=%v", got)
	}
	if got := deviceTypeString(ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED); got != "" {
		t.Errorf("deviceTypeString default=%q", got)
	}
}

func TestDeviceStatusRoundTrip(t *testing.T) {
	cases := []struct {
		s string
		e ipamv1.DeviceStatus
	}{
		{"active", ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE},
		{"planned", ipamv1.DeviceStatus_DEVICE_STATUS_PLANNED},
		{"staged", ipamv1.DeviceStatus_DEVICE_STATUS_STAGED},
		{"decommissioned", ipamv1.DeviceStatus_DEVICE_STATUS_DECOMMISSIONED},
		{"offline", ipamv1.DeviceStatus_DEVICE_STATUS_OFFLINE},
		{"failed", ipamv1.DeviceStatus_DEVICE_STATUS_FAILED},
		{"available", ipamv1.DeviceStatus_DEVICE_STATUS_AVAILABLE},
	}
	for _, c := range cases {
		if got := deviceStatusEnum(c.s); got != c.e {
			t.Errorf("deviceStatusEnum(%q)=%v want %v", c.s, got, c.e)
		}
		if got := deviceStatusString(c.e); got != c.s {
			t.Errorf("deviceStatusString(%v)=%q want %q", c.e, got, c.s)
		}
	}
	if got := deviceStatusEnum("bogus"); got != ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED {
		t.Errorf("deviceStatusEnum default=%v", got)
	}
	if got := deviceStatusString(ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED); got != "" {
		t.Errorf("deviceStatusString default=%q", got)
	}
}

func TestGroupStatusString(t *testing.T) {
	cases := []struct {
		e ipamv1.GroupStatus
		s string
	}{
		{ipamv1.GroupStatus_GROUP_STATUS_ACTIVE, "active"},
		{ipamv1.GroupStatus_GROUP_STATUS_INACTIVE, "inactive"},
		{ipamv1.GroupStatus_GROUP_STATUS_UNSPECIFIED, ""},
	}
	for _, c := range cases {
		if got := groupStatusString(c.e); got != c.s {
			t.Errorf("groupStatusString(%v)=%q want %q", c.e, got, c.s)
		}
	}
}

func TestScanStatusString(t *testing.T) {
	cases := []struct {
		e ipamv1.ScanStatus
		s string
	}{
		{ipamv1.ScanStatus_SCAN_STATUS_PENDING, "pending"},
		{ipamv1.ScanStatus_SCAN_STATUS_SCANNING, "scanning"},
		{ipamv1.ScanStatus_SCAN_STATUS_COMPLETED, "completed"},
		{ipamv1.ScanStatus_SCAN_STATUS_FAILED, "failed"},
		{ipamv1.ScanStatus_SCAN_STATUS_CANCELLED, "cancelled"},
		{ipamv1.ScanStatus_SCAN_STATUS_UNSPECIFIED, ""},
	}
	for _, c := range cases {
		if got := scanStatusString(c.e); got != c.s {
			t.Errorf("scanStatusString(%v)=%q want %q", c.e, got, c.s)
		}
	}
}

func TestScanTriggerString(t *testing.T) {
	cases := []struct {
		e ipamv1.ScanTrigger
		s string
	}{
		{ipamv1.ScanTrigger_SCAN_TRIGGER_AUTO, "auto"},
		{ipamv1.ScanTrigger_SCAN_TRIGGER_MANUAL, "manual"},
		{ipamv1.ScanTrigger_SCAN_TRIGGER_UNSPECIFIED, ""},
	}
	for _, c := range cases {
		if got := scanTriggerString(c.e); got != c.s {
			t.Errorf("scanTriggerString(%v)=%q want %q", c.e, got, c.s)
		}
	}
}

func TestPowerActionEnum(t *testing.T) {
	cases := []struct {
		s string
		e ipamv1.PowerAction
	}{
		{"on", ipamv1.PowerAction_POWER_ACTION_ON},
		{"off", ipamv1.PowerAction_POWER_ACTION_OFF},
		{"cycle", ipamv1.PowerAction_POWER_ACTION_CYCLE},
		{"reset", ipamv1.PowerAction_POWER_ACTION_RESET},
		{"soft", ipamv1.PowerAction_POWER_ACTION_SOFT},
		{"diag", ipamv1.PowerAction_POWER_ACTION_DIAG},
		{"bogus", ipamv1.PowerAction_POWER_ACTION_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := powerActionEnum(c.s); got != c.e {
			t.Errorf("powerActionEnum(%q)=%v want %v", c.s, got, c.e)
		}
	}
}

func TestUnixTime(t *testing.T) {
	if got := unixTime(0); !got.IsZero() {
		t.Errorf("unixTime(0) not zero: %v", got)
	}
	want := time.Unix(1700000000, 0).UTC()
	if got := unixTime(1700000000); !got.Equal(want) {
		t.Errorf("unixTime(1700000000)=%v want %v", got, want)
	}
}

func TestConvertersNil(t *testing.T) {
	if got := toSubnet(nil); got.ID != "" || got.Tags != nil {
		t.Errorf("toSubnet(nil) not zero: %+v", got)
	}
	if got := toAddress(nil); got.ID != "" || got.Tags != nil {
		t.Errorf("toAddress(nil) not zero: %+v", got)
	}
	if got := toDevice(nil); got.ID != "" || got.Tags != nil {
		t.Errorf("toDevice(nil) not zero: %+v", got)
	}
	if got := toIPGroup(nil); got.ID != "" || got.Tags != nil {
		t.Errorf("toIPGroup(nil) not zero: %+v", got)
	}
	if got := toScanJob(nil); got.ID != "" {
		t.Errorf("toScanJob(nil) not zero: %+v", got)
	}
}

func TestConvertersPopulated(t *testing.T) {
	sub := toSubnet(&ipamv1.Subnet{
		Id: "sn-1", TenantId: "t1", Name: "n", Cidr: "10.0.0.0/24",
		Status: ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE, IpVersion: 4,
		PrefixLength: 24, SnmpVersion: 2, CreatedAt: 1700000000, UpdatedAt: 1700000001,
		TotalAddresses: 256, UsedAddresses: 10, AvailableAddresses: 246, Utilization: 0.04,
		Tags: map[string]string{"env": "prod"},
	})
	if sub.ID != "sn-1" || sub.Status != "active" || sub.IPVersion != 4 || sub.PrefixLength != 24 || sub.Tags["env"] != "prod" {
		t.Errorf("toSubnet populated: %+v", sub)
	}
	if sub.CreatedAt.IsZero() || sub.UpdatedAt.IsZero() {
		t.Errorf("toSubnet times zero: %+v", sub)
	}

	addr := toAddress(&ipamv1.IPAddress{
		Id: "ip-1", TenantId: "t1", Address: "10.0.0.5", SubnetId: "sn-1",
		Status: ipamv1.IpStatus_IP_STATUS_ACTIVE, AddressType: ipamv1.AddressType_ADDRESS_TYPE_HOST,
		IsPrimary: true, HasReverseDns: true, LastSeen: 1700000000, LeaseExpiry: 1700000002,
	})
	if addr.ID != "ip-1" || addr.Status != "active" || addr.AddressType != "host" || !addr.IsPrimary || !addr.HasReverseDNS {
		t.Errorf("toAddress populated: %+v", addr)
	}

	dev := toDevice(&ipamv1.Device{
		Id: "dev-1", TenantId: "t1", Name: "d", DeviceType: ipamv1.DeviceType_DEVICE_TYPE_SERVER,
		Status: ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE, RackPosition: 3, DeviceHeightU: 2,
		RebootRequired: true, UnattendedUpgrades: true, InterfaceCount: 2, AddressCount: 4,
		PackageUpdateCount: 5, SecurityUpdateCount: 1,
	})
	if dev.ID != "dev-1" || dev.DeviceType != "server" || dev.Status != "active" || dev.RackPosition != 3 || !dev.RebootRequired {
		t.Errorf("toDevice populated: %+v", dev)
	}

	grp := toIPGroup(&ipamv1.IPGroup{
		Id: "g-1", TenantId: "t1", Name: "grp", Status: ipamv1.GroupStatus_GROUP_STATUS_ACTIVE, MemberCount: 7,
	})
	if grp.ID != "g-1" || grp.Status != "active" || grp.MemberCount != 7 {
		t.Errorf("toIPGroup populated: %+v", grp)
	}

	job := toScanJob(&ipamv1.IPScanJob{
		Id: "job-1", TenantId: "t1", SubnetId: "sn-1", Status: ipamv1.ScanStatus_SCAN_STATUS_SCANNING,
		Progress: 50, TriggeredBy: ipamv1.ScanTrigger_SCAN_TRIGGER_AUTO, TotalAddresses: 14,
		ScannedCount: 7, AliveCount: 3, NewCount: 1, UpdatedCount: 2,
	})
	if job.ID != "job-1" || job.Status != "scanning" || job.Progress != 50 || job.TriggeredBy != "auto" {
		t.Errorf("toScanJob populated: %+v", job)
	}
}
