// Package ipamclient is a thin, typed Go client for the ipam.v1
// service-to-service gRPC API, for other Freya modules that need to read or
// administer subnets, addresses, devices, groups, run discovery scans, read
// per-tenant statistics, or drive out-of-band device power. It wraps the
// generated gRPC stubs so callers deal in ordinary Go values, not protobuf
// messages. The caller supplies a connected, SPIFFE-mTLS *grpc.ClientConn (e.g.
// from freya.App.Client(ctx, "ipam")); this package does not dial or manage the
// connection. No response carries SNMP/BMC/IPMI credentials or sealed
// owner/contact fields; devices/subnets expose only opaque warden references.
package ipamclient

import (
	"context"

	"google.golang.org/grpc"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
)

// Client calls the ipam.v1 API over a caller-provided gRPC connection.
type Client struct {
	subnets    ipamv1.SubnetServiceClient
	addresses  ipamv1.IpAddressServiceClient
	devices    ipamv1.DeviceServiceClient
	vlans      ipamv1.VlanServiceClient
	locations  ipamv1.LocationServiceClient
	ipGroups   ipamv1.IpGroupServiceClient
	hostGroups ipamv1.HostGroupServiceClient
	scans      ipamv1.IpScanServiceClient
	system     ipamv1.SystemServiceClient
}

// New builds a client from a connected (SPIFFE-mTLS) gRPC connection to the
// ipam service. It wraps all nine ipam.v1 services.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{
		subnets:    ipamv1.NewSubnetServiceClient(conn),
		addresses:  ipamv1.NewIpAddressServiceClient(conn),
		devices:    ipamv1.NewDeviceServiceClient(conn),
		vlans:      ipamv1.NewVlanServiceClient(conn),
		locations:  ipamv1.NewLocationServiceClient(conn),
		ipGroups:   ipamv1.NewIpGroupServiceClient(conn),
		hostGroups: ipamv1.NewHostGroupServiceClient(conn),
		scans:      ipamv1.NewIpScanServiceClient(conn),
		system:     ipamv1.NewSystemServiceClient(conn),
	}
}

// ---- subnets ----

// CreateSubnet creates a subnet in the tenant from the given fields. The server
// derives the network/broadcast/mask/prefix and refuses overlaps.
func (c *Client) CreateSubnet(ctx context.Context, tenantID string, in Subnet) (Subnet, error) {
	resp, err := c.subnets.Create(ctx, &ipamv1.CreateSubnetRequest{
		TenantId: tenantID,
		Subnet: &ipamv1.Subnet{
			Name: in.Name, Cidr: in.CIDR, Description: in.Description, Gateway: in.Gateway,
			DnsServers: in.DNSServers, VlanId: in.VlanID, ParentId: in.ParentID, LocationId: in.LocationID,
			Status: subnetStatusEnum(in.Status), SnmpSecretRef: in.SNMPSecretRef, SnmpVersion: int32(in.SNMPVersion),
			Tags: in.Tags,
		},
	})
	if err != nil {
		return Subnet{}, err
	}
	return toSubnet(resp), nil
}

// GetSubnet returns one subnet by id, with its computed utilization fields.
func (c *Client) GetSubnet(ctx context.Context, tenantID, id string) (Subnet, error) {
	resp, err := c.subnets.Get(ctx, &ipamv1.GetSubnetRequest{TenantId: tenantID, Id: id})
	if err != nil {
		return Subnet{}, err
	}
	return toSubnet(resp), nil
}

// ListSubnets lists a tenant's subnets matching f (empty filters match all).
func (c *Client) ListSubnets(ctx context.Context, tenantID string, f SubnetFilter) ([]Subnet, error) {
	resp, err := c.subnets.List(ctx, &ipamv1.ListSubnetsRequest{
		TenantId: tenantID, VlanId: f.VlanID, ParentId: f.ParentID, LocationId: f.LocationID,
		Status: subnetStatusEnum(f.Status), IpVersion: int32(f.IPVersion), Query: f.Query,
		Limit: int64(f.Limit), CursorId: f.CursorID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Subnet, 0, len(resp.GetSubnets()))
	for _, s := range resp.GetSubnets() {
		out = append(out, toSubnet(s))
	}
	return out, nil
}

// ---- addresses ----

// AllocateNextAddress hands out the next free host in a subnet, optionally
// stamping the request's hostname/device/description.
func (c *Client) AllocateNextAddress(ctx context.Context, tenantID string, req AllocateRequest) (IPAddress, error) {
	resp, err := c.addresses.AllocateNext(ctx, &ipamv1.AllocateNextRequest{
		TenantId: tenantID, SubnetId: req.SubnetID, Hostname: req.Hostname,
		DeviceId: req.DeviceID, Description: req.Description,
	})
	if err != nil {
		return IPAddress{}, err
	}
	return toAddress(resp), nil
}

// GetAddress returns one address by id. An unknown id (or one of another
// tenant) is a gRPC NotFound status, which callers may test with
// status.Code(err) == codes.NotFound.
func (c *Client) GetAddress(ctx context.Context, tenantID, id string) (IPAddress, error) {
	resp, err := c.addresses.Get(ctx, &ipamv1.GetIpAddressRequest{TenantId: tenantID, Id: id})
	if err != nil {
		return IPAddress{}, err
	}
	return toAddress(resp), nil
}

// FindAddress returns the address with the given dotted/colon string.
func (c *Client) FindAddress(ctx context.Context, tenantID, address string) (IPAddress, error) {
	resp, err := c.addresses.Find(ctx, &ipamv1.FindIpAddressRequest{TenantId: tenantID, Address: address})
	if err != nil {
		return IPAddress{}, err
	}
	return toAddress(resp), nil
}

// ---- devices ----

// CreateDevice creates a managed device in the tenant.
func (c *Client) CreateDevice(ctx context.Context, tenantID string, in Device) (Device, error) {
	resp, err := c.devices.Create(ctx, &ipamv1.CreateDeviceRequest{
		TenantId: tenantID,
		Device: &ipamv1.Device{
			Name: in.Name, DeviceType: deviceTypeEnum(in.DeviceType), Description: in.Description,
			Manufacturer: in.Manufacturer, Model: in.Model, SerialNumber: in.SerialNumber, AssetTag: in.AssetTag,
			LocationId: in.LocationID, RackId: in.RackID, RackPosition: int32(in.RackPosition),
			DeviceHeightU: int32(in.DeviceHeightU), Status: deviceStatusEnum(in.Status), PrimaryIp: in.PrimaryIP,
			PrimaryIpv6: in.PrimaryIPv6, ManagementIp: in.ManagementIP, OsType: in.OSType, OsVersion: in.OSVersion,
			FirmwareVersion: in.FirmwareVersion, IpmiSecretRef: in.IPMISecretRef, RebootRequired: in.RebootRequired,
			UnattendedUpgrades: in.UnattendedUpgrades, Tags: in.Tags,
		},
	})
	if err != nil {
		return Device{}, err
	}
	return toDevice(resp), nil
}

// GetDevice returns one device by id.
func (c *Client) GetDevice(ctx context.Context, tenantID, id string) (Device, error) {
	resp, err := c.devices.Get(ctx, &ipamv1.GetDeviceRequest{TenantId: tenantID, Id: id})
	if err != nil {
		return Device{}, err
	}
	return toDevice(resp), nil
}

// PowerStatus reads the out-of-band power state of a device's BMC. It returns
// "on", "off" or "unknown". Requires platform-admin on the server.
func (c *Client) PowerStatus(ctx context.Context, tenantID, deviceID string) (string, error) {
	resp, err := c.devices.PowerStatus(ctx, &ipamv1.PowerStatusRequest{TenantId: tenantID, Id: deviceID})
	if err != nil {
		return "", err
	}
	return resp.GetState(), nil
}

// Power applies an out-of-band power action ("on","off","cycle","reset",
// "soft","diag") to a device's BMC and returns the resulting state. Requires
// platform-admin on the server.
func (c *Client) Power(ctx context.Context, tenantID, deviceID, action string) (string, error) {
	resp, err := c.devices.Power(ctx, &ipamv1.PowerRequest{
		TenantId: tenantID, Id: deviceID, Action: powerActionEnum(action),
	})
	if err != nil {
		return "", err
	}
	return resp.GetState(), nil
}

// ---- groups ----

// CheckIpInGroup returns the IP groups the given ip falls in.
func (c *Client) CheckIpInGroup(ctx context.Context, tenantID, ip string) ([]IPGroup, error) {
	resp, err := c.ipGroups.CheckIpInGroup(ctx, &ipamv1.CheckIpInGroupRequest{TenantId: tenantID, Ip: ip})
	if err != nil {
		return nil, err
	}
	out := make([]IPGroup, 0, len(resp.GetGroups()))
	for _, g := range resp.GetGroups() {
		out = append(out, toIPGroup(g))
	}
	return out, nil
}

// ---- scans ----

// StartScan enqueues an async discovery scan for a subnet and returns the job.
func (c *Client) StartScan(ctx context.Context, tenantID string, opts ScanOptions) (ScanJob, error) {
	resp, err := c.scans.Start(ctx, &ipamv1.StartScanRequest{
		TenantId: tenantID, SubnetId: opts.SubnetID, EnableSnmp: opts.EnableSNMP,
		EnableDnsUpdate: opts.EnableDNSUpdate, SkipReverseDns: opts.SkipReverseDNS,
		TcpProbePorts: opts.TCPProbePorts, Concurrency: int32(opts.Concurrency), TimeoutMs: int32(opts.TimeoutMs),
	})
	if err != nil {
		return ScanJob{}, err
	}
	return toScanJob(resp), nil
}

// ---- statistics ----

// GetStatistics returns a tenant's IPAM statistics rollup.
func (c *Client) GetStatistics(ctx context.Context, tenantID string) (Statistics, error) {
	resp, err := c.system.GetStats(ctx, &ipamv1.GetStatsRequest{TenantId: tenantID})
	if err != nil {
		return Statistics{}, err
	}
	return Statistics{
		SubnetsTotal: resp.GetSubnetsTotal(), AddressesTotal: resp.GetAddressesTotal(),
		AddressesUsed: resp.GetAddressesUsed(), AddressesAvailable: resp.GetAddressesAvailable(),
		Utilization: resp.GetUtilization(), DevicesTotal: resp.GetDevicesTotal(), VlansTotal: resp.GetVlansTotal(),
		LocationsTotal: resp.GetLocationsTotal(), IPGroupsTotal: resp.GetIpGroupsTotal(),
		HostGroupsTotal: resp.GetHostGroupsTotal(), ScansRunning: resp.GetScansRunning(),
		DevicesByStatus: resp.GetDevicesByStatus(), DevicesByType: resp.GetDevicesByType(),
		AddressesByStatus: resp.GetAddressesByStatus(), SubnetsByStatus: resp.GetSubnetsByStatus(),
	}, nil
}
