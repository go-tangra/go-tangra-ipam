package grpcapi

import (
	"context"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/ipmi"
	"github.com/go-freya/freya/services/ipam/internal/kvm"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

// ================= SubnetService =================

// SubnetServer implements ipam.v1.SubnetService.
type SubnetServer struct {
	ipamv1.UnimplementedSubnetServiceServer
	subnets *subnets.Service
	scan    *scan.Service
}

func (s *SubnetServer) Create(ctx context.Context, req *ipamv1.CreateSubnetRequest) (*ipamv1.Subnet, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.subnets.Create(ctx, subj, subnetFromPB(req.GetSubnet()), false)
	if err != nil {
		return nil, grpcError(err)
	}
	return subnetToPB(v), nil
}

func (s *SubnetServer) Get(ctx context.Context, req *ipamv1.GetSubnetRequest) (*ipamv1.Subnet, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.subnets.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return subnetToPB(v), nil
}

func (s *SubnetServer) List(ctx context.Context, req *ipamv1.ListSubnetsRequest) (*ipamv1.ListSubnetsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.SubnetFilter{
		VlanID: req.GetVlanId(), ParentID: req.GetParentId(), LocationID: req.GetLocationId(),
		Status: subnetStatusFromPB(req.GetStatus()), IPVersion: int(req.GetIpVersion()),
		Query: req.GetQuery(), Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.subnets.List(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListSubnetsResponse{Subnets: make([]*ipamv1.Subnet, 0, len(items))}
	for _, v := range items {
		out.Subnets = append(out.Subnets, subnetToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *SubnetServer) Update(ctx context.Context, req *ipamv1.UpdateSubnetRequest) (*ipamv1.Subnet, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := subnetFromPB(req.GetSubnet())
	in.ID = req.GetId()
	v, err := s.subnets.Update(ctx, subj, in, false)
	if err != nil {
		return nil, grpcError(err)
	}
	return subnetToPB(v), nil
}

func (s *SubnetServer) Delete(ctx context.Context, req *ipamv1.DeleteSubnetRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.subnets.Delete(ctx, subj, req.GetId(), req.GetForce()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *SubnetServer) GetTree(ctx context.Context, req *ipamv1.GetSubnetTreeRequest) (*ipamv1.SubnetTree, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	roots, err := s.subnets.GetTree(ctx, subj)
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.SubnetTree{Roots: subnetTreeToPB(roots)}, nil
}

// GetStats returns a single subnet with its computed utilization fields.
func (s *SubnetServer) GetStats(ctx context.Context, req *ipamv1.GetSubnetStatsRequest) (*ipamv1.Subnet, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.subnets.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return subnetToPB(v), nil
}

func (s *SubnetServer) Scan(ctx context.Context, req *ipamv1.ScanSubnetRequest) (*ipamv1.IPScanJob, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if s.scan == nil {
		return nil, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	job, err := s.scan.StartScan(ctx, subj, req.GetId(), scan.Options{
		EnableSNMP: req.GetEnableSnmp(), SkipReverseDNS: req.GetSkipReverseDns(),
	})
	if err != nil {
		return nil, grpcError(err)
	}
	return scanJobToPB(job), nil
}

// ================= IpAddressService =================

// IpAddressServer implements ipam.v1.IpAddressService.
type IpAddressServer struct {
	ipamv1.UnimplementedIpAddressServiceServer
	addresses *addresses.Service
}

func (s *IpAddressServer) Create(ctx context.Context, req *ipamv1.CreateIpAddressRequest) (*ipamv1.IPAddress, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.addresses.Create(ctx, subj, addressFromPB(req.GetAddress()))
	if err != nil {
		return nil, grpcError(err)
	}
	return addressToPB(v), nil
}

func (s *IpAddressServer) Get(ctx context.Context, req *ipamv1.GetIpAddressRequest) (*ipamv1.IPAddress, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.addresses.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return addressToPB(v), nil
}

func (s *IpAddressServer) List(ctx context.Context, req *ipamv1.ListIpAddressesRequest) (*ipamv1.ListIpAddressesResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.AddressFilter{
		SubnetID: req.GetSubnetId(), DeviceID: req.GetDeviceId(), Status: ipStatusFromPB(req.GetStatus()),
		AddressType: addressTypeFromPB(req.GetAddressType()), AddressPrefix: req.GetAddressPrefix(),
		HostnamePattern: req.GetHostnamePattern(), Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.addresses.List(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListIpAddressesResponse{Addresses: make([]*ipamv1.IPAddress, 0, len(items))}
	for _, v := range items {
		out.Addresses = append(out.Addresses, addressToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *IpAddressServer) Update(ctx context.Context, req *ipamv1.UpdateIpAddressRequest) (*ipamv1.IPAddress, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := addressFromPB(req.GetAddress())
	in.ID = req.GetId()
	v, err := s.addresses.Update(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return addressToPB(v), nil
}

func (s *IpAddressServer) Delete(ctx context.Context, req *ipamv1.DeleteIpAddressRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.addresses.Delete(ctx, subj, req.GetId()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

// AllocateNext hands out the lowest free host and, when the request carries
// hostname/device/description, stamps them onto the allocated address.
func (s *IpAddressServer) AllocateNext(ctx context.Context, req *ipamv1.AllocateNextRequest) (*ipamv1.IPAddress, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.addresses.AllocateNext(ctx, subj, req.GetSubnetId(), "", nil)
	if err != nil {
		return nil, grpcError(err)
	}
	if req.GetHostname() != "" || req.GetDeviceId() != "" || req.GetDescription() != "" {
		v.Hostname = req.GetHostname()
		v.DeviceID = req.GetDeviceId()
		v.Description = req.GetDescription()
		v, err = s.addresses.Update(ctx, subj, v)
		if err != nil {
			return nil, grpcError(err)
		}
	}
	return addressToPB(v), nil
}

func (s *IpAddressServer) BulkAllocate(ctx context.Context, req *ipamv1.BulkAllocateRequest) (*ipamv1.BulkAllocateResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.addresses.BulkAllocate(ctx, subj, req.GetSubnetId(), int(req.GetCount()), req.GetHostnamePrefix())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.BulkAllocateResponse{Addresses: make([]*ipamv1.IPAddress, 0, len(items))}
	for _, v := range items {
		out.Addresses = append(out.Addresses, addressToPB(v))
	}
	return out, nil
}

func (s *IpAddressServer) Find(ctx context.Context, req *ipamv1.FindIpAddressRequest) (*ipamv1.IPAddress, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.addresses.Find(ctx, subj, req.GetAddress())
	if err != nil {
		return nil, grpcError(err)
	}
	return addressToPB(v), nil
}

func (s *IpAddressServer) Suggest(ctx context.Context, req *ipamv1.SuggestIpAddressRequest) (*ipamv1.SuggestIpAddressResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	ips, err := s.addresses.SuggestAvailableAddresses(ctx, subj, req.GetSubnetId(), int(req.GetCount()), nil)
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.SuggestIpAddressResponse{Addresses: ips}, nil
}

func (s *IpAddressServer) Ping(ctx context.Context, req *ipamv1.PingIpAddressRequest) (*ipamv1.PingResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	res, err := s.addresses.PingAddress(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.PingResponse{Alive: res.Alive, RttMs: int32(res.RTTMs)}, nil
}

// ================= DeviceService =================

// DeviceServer implements ipam.v1.DeviceService. The power/KVM RPCs require the
// platform-admin role and fetch BMC credentials from warden at use time.
type DeviceServer struct {
	ipamv1.UnimplementedDeviceServiceServer
	devices *devices.Service
	bmc     ipmi.BMC
	kvm     *kvm.Manager
	warden  warden.Client
}

func (s *DeviceServer) Create(ctx context.Context, req *ipamv1.CreateDeviceRequest) (*ipamv1.Device, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.devices.Create(ctx, subj, deviceFromPB(req.GetDevice()))
	if err != nil {
		return nil, grpcError(err)
	}
	return deviceToPB(v), nil
}

func (s *DeviceServer) Get(ctx context.Context, req *ipamv1.GetDeviceRequest) (*ipamv1.Device, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.devices.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return deviceToPB(v), nil
}

func (s *DeviceServer) List(ctx context.Context, req *ipamv1.ListDevicesRequest) (*ipamv1.ListDevicesResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.DeviceFilter{
		DeviceType: deviceTypeFromPB(req.GetDeviceType()), Status: deviceStatusFromPB(req.GetStatus()),
		LocationID: req.GetLocationId(), Manufacturer: req.GetManufacturer(), RackID: req.GetRackId(),
		Query: req.GetQuery(), Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.devices.List(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListDevicesResponse{Devices: make([]*ipamv1.Device, 0, len(items))}
	for _, v := range items {
		out.Devices = append(out.Devices, deviceToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *DeviceServer) Update(ctx context.Context, req *ipamv1.UpdateDeviceRequest) (*ipamv1.Device, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := deviceFromPB(req.GetDevice())
	in.ID = req.GetId()
	v, err := s.devices.Update(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return deviceToPB(v), nil
}

func (s *DeviceServer) Delete(ctx context.Context, req *ipamv1.DeleteDeviceRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.devices.Delete(ctx, subj, req.GetId(), req.GetForce()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *DeviceServer) GetAddresses(ctx context.Context, req *ipamv1.GetDeviceAddressesRequest) (*ipamv1.ListIpAddressesResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.devices.GetAddresses(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListIpAddressesResponse{Addresses: make([]*ipamv1.IPAddress, 0, len(items))}
	for _, v := range items {
		out.Addresses = append(out.Addresses, addressToPB(v))
	}
	return out, nil
}

func (s *DeviceServer) GetInterfaces(ctx context.Context, req *ipamv1.GetDeviceInterfacesRequest) (*ipamv1.ListInterfacesResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.devices.ListInterfaces(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListInterfacesResponse{Interfaces: make([]*ipamv1.DeviceInterface, 0, len(items))}
	for _, v := range items {
		out.Interfaces = append(out.Interfaces, deviceInterfaceToPB(v))
	}
	return out, nil
}

func (s *DeviceServer) CreateInterface(ctx context.Context, req *ipamv1.CreateInterfaceRequest) (*ipamv1.DeviceInterface, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.devices.CreateInterface(ctx, subj, req.GetDeviceId(), deviceInterfaceFromPB(req.GetIface()))
	if err != nil {
		return nil, grpcError(err)
	}
	return deviceInterfaceToPB(v), nil
}

func (s *DeviceServer) DeleteInterface(ctx context.Context, req *ipamv1.DeleteInterfaceRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.devices.DeleteInterface(ctx, subj, req.GetInterfaceId()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *DeviceServer) ListPackages(ctx context.Context, req *ipamv1.ListPackagesRequest) (*ipamv1.ListPackagesResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.devices.ListPackages(ctx, subj, req.GetDeviceId(), nil, nil, "")
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListPackagesResponse{Packages: make([]*ipamv1.DevicePackage, 0, len(items))}
	for _, v := range items {
		out.Packages = append(out.Packages, devicePackageToPB(v))
	}
	return out, nil
}

// SyncPackages carries no package payload on the wire, so it is not offered on
// the mesh; agents sync packages through the HTTP ingest path.
func (s *DeviceServer) SyncPackages(ctx context.Context, req *ipamv1.SyncPackagesRequest) (*ipamv1.ListPackagesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "SyncPackages is not offered on the mesh API")
}

func (s *DeviceServer) PowerStatus(ctx context.Context, req *ipamv1.PowerStatusRequest) (*ipamv1.PowerStatusResponse, error) {
	subj, host, creds, err := s.bmcContext(ctx, req.GetTenantId(), req.GetId())
	if err != nil {
		return nil, err
	}
	_ = subj
	st, err := s.bmc.PowerStatus(ctx, host, creds)
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.PowerStatusResponse{State: powerStateString(st.On)}, nil
}

func (s *DeviceServer) Power(ctx context.Context, req *ipamv1.PowerRequest) (*ipamv1.PowerStatusResponse, error) {
	subj, host, creds, err := s.bmcContext(ctx, req.GetTenantId(), req.GetId())
	if err != nil {
		return nil, err
	}
	_ = subj
	action := powerActionToStore(req.GetAction())
	if action == "" {
		return nil, status.Error(codes.InvalidArgument, "power action required")
	}
	if err := s.bmc.Power(ctx, host, creds, action); err != nil {
		return nil, grpcError(err)
	}
	st, err := s.bmc.PowerStatus(ctx, host, creds)
	if err != nil {
		return &ipamv1.PowerStatusResponse{State: "unknown"}, nil
	}
	return &ipamv1.PowerStatusResponse{State: powerStateString(st.On)}, nil
}

func (s *DeviceServer) StartKvmSession(ctx context.Context, req *ipamv1.StartKvmSessionRequest) (*ipamv1.KvmSession, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		return nil, grpcError(err)
	}
	if s.kvm == nil || s.warden == nil {
		return nil, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	dev, host, secret, err := s.deviceSecret(ctx, subj, req.GetId())
	if err != nil {
		return nil, err
	}
	token, consoleURL, err := s.kvm.StartSession(ctx, dev.ID, host, kvm.Creds{
		Username: secret["username"], Password: secret["password"],
	})
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.KvmSession{Token: token, ConsoleUrl: consoleURL}, nil
}

// bmcContext performs the platform-admin check and resolves the device's BMC
// host and IPMI credentials (fetched from warden at use time).
func (s *DeviceServer) bmcContext(ctx context.Context, tenantID, deviceID string) (authz.Subjects, string, ipmi.Creds, error) {
	subj, err := caller(ctx, tenantID)
	if err != nil {
		return authz.Subjects{}, "", ipmi.Creds{}, err
	}
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		return authz.Subjects{}, "", ipmi.Creds{}, grpcError(err)
	}
	if s.bmc == nil || s.warden == nil {
		return authz.Subjects{}, "", ipmi.Creds{}, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	_, host, secret, err := s.deviceSecret(ctx, subj, deviceID)
	if err != nil {
		return authz.Subjects{}, "", ipmi.Creds{}, err
	}
	port := 0
	if p := secret["port"]; p != "" {
		port, _ = strconv.Atoi(p)
	}
	creds := ipmi.Creds{
		Username: secret["username"], Password: secret["password"],
		Protocol: secret["protocol"], Port: port,
	}
	return subj, host, creds, nil
}

// deviceSecret loads a device, requires an IPMI reference, and fetches the
// credential material from warden. The map is used immediately and never stored.
func (s *DeviceServer) deviceSecret(ctx context.Context, subj authz.Subjects, deviceID string) (store.Device, string, map[string]string, error) {
	dev, err := s.devices.Get(ctx, subj, deviceID)
	if err != nil {
		return store.Device{}, "", nil, grpcError(err)
	}
	if dev.IPMISecretRef == "" {
		return store.Device{}, "", nil, status.Error(codes.FailedPrecondition, "device has no BMC/IPMI credential reference")
	}
	host := dev.ManagementIP
	if host == "" {
		host = dev.PrimaryIP
	}
	if host == "" {
		return store.Device{}, "", nil, status.Error(codes.FailedPrecondition, "device has no management or primary IP")
	}
	secret, err := s.warden.GetSecret(ctx, dev.IPMISecretRef)
	if err != nil {
		return store.Device{}, "", nil, grpcError(err)
	}
	return dev, host, secret, nil
}

func powerStateString(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// ================= VlanService =================

// VlanServer implements ipam.v1.VlanService.
type VlanServer struct {
	ipamv1.UnimplementedVlanServiceServer
	vlans *vlans.Service
}

func (s *VlanServer) Create(ctx context.Context, req *ipamv1.CreateVlanRequest) (*ipamv1.Vlan, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.vlans.Create(ctx, subj, vlanFromPB(req.GetVlan()))
	if err != nil {
		return nil, grpcError(err)
	}
	return vlanToPB(v), nil
}

func (s *VlanServer) Get(ctx context.Context, req *ipamv1.GetVlanRequest) (*ipamv1.Vlan, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.vlans.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return vlanToPB(v), nil
}

func (s *VlanServer) List(ctx context.Context, req *ipamv1.ListVlansRequest) (*ipamv1.ListVlansResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.VlanFilter{
		LocationID: req.GetLocationId(), Domain: req.GetDomain(), Status: vlanStatusFromPB(req.GetStatus()),
		VlanIDMin: int(req.GetVlanIdMin()), VlanIDMax: int(req.GetVlanIdMax()),
		Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.vlans.List(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListVlansResponse{Vlans: make([]*ipamv1.Vlan, 0, len(items))}
	for _, v := range items {
		out.Vlans = append(out.Vlans, vlanToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *VlanServer) Update(ctx context.Context, req *ipamv1.UpdateVlanRequest) (*ipamv1.Vlan, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := vlanFromPB(req.GetVlan())
	in.ID = req.GetId()
	v, err := s.vlans.Update(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return vlanToPB(v), nil
}

func (s *VlanServer) Delete(ctx context.Context, req *ipamv1.DeleteVlanRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.vlans.Delete(ctx, subj, req.GetId(), false); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *VlanServer) GetSubnets(ctx context.Context, req *ipamv1.GetVlanSubnetsRequest) (*ipamv1.ListSubnetsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.vlans.GetSubnets(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListSubnetsResponse{Subnets: make([]*ipamv1.Subnet, 0, len(items))}
	for _, v := range items {
		out.Subnets = append(out.Subnets, subnetToPB(v))
	}
	return out, nil
}

// ================= LocationService =================

// LocationServer implements ipam.v1.LocationService.
type LocationServer struct {
	ipamv1.UnimplementedLocationServiceServer
	locations *locations.Service
}

func (s *LocationServer) Create(ctx context.Context, req *ipamv1.CreateLocationRequest) (*ipamv1.Location, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.locations.Create(ctx, subj, locationFromPB(req.GetLocation()))
	if err != nil {
		return nil, grpcError(err)
	}
	return locationToPB(v), nil
}

func (s *LocationServer) Get(ctx context.Context, req *ipamv1.GetLocationRequest) (*ipamv1.Location, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.locations.Get(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return locationToPB(v), nil
}

func (s *LocationServer) List(ctx context.Context, req *ipamv1.ListLocationsRequest) (*ipamv1.ListLocationsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.LocationFilter{
		ParentID: req.GetParentId(), LocationType: locationTypeFromPB(req.GetLocationType()),
		Country: req.GetCountry(), Status: locationStatusFromPB(req.GetStatus()),
		Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.locations.List(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListLocationsResponse{Locations: make([]*ipamv1.Location, 0, len(items))}
	for _, v := range items {
		out.Locations = append(out.Locations, locationToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *LocationServer) Update(ctx context.Context, req *ipamv1.UpdateLocationRequest) (*ipamv1.Location, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := locationFromPB(req.GetLocation())
	in.ID = req.GetId()
	v, err := s.locations.Update(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return locationToPB(v), nil
}

func (s *LocationServer) Delete(ctx context.Context, req *ipamv1.DeleteLocationRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.locations.Delete(ctx, subj, req.GetId(), req.GetForce()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *LocationServer) GetTree(ctx context.Context, req *ipamv1.GetLocationTreeRequest) (*ipamv1.LocationTree, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	roots, err := s.locations.GetTree(ctx, subj)
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.LocationTree{Roots: locationTreeToPB(roots)}, nil
}

// ================= IpGroupService =================

// IpGroupServer implements ipam.v1.IpGroupService.
type IpGroupServer struct {
	ipamv1.UnimplementedIpGroupServiceServer
	groups *groups.Service
}

func (s *IpGroupServer) Create(ctx context.Context, req *ipamv1.CreateIpGroupRequest) (*ipamv1.IPGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.groups.CreateIPGroup(ctx, subj, ipGroupFromPB(req.GetGroup()))
	if err != nil {
		return nil, grpcError(err)
	}
	return ipGroupToPB(v), nil
}

func (s *IpGroupServer) Get(ctx context.Context, req *ipamv1.GetIpGroupRequest) (*ipamv1.IPGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.groups.GetIPGroup(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	pb := ipGroupToPB(v)
	if req.GetIncludeMembers() {
		members, err := s.groups.ListIPGroupMembers(ctx, subj, v.ID)
		if err != nil {
			return nil, grpcError(err)
		}
		pb.Members = ipGroupMembersToPB(members)
	}
	return pb, nil
}

func (s *IpGroupServer) List(ctx context.Context, req *ipamv1.ListIpGroupsRequest) (*ipamv1.ListIpGroupsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.groups.ListIPGroups(ctx, subj, int(req.GetLimit()), req.GetCursorId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListIpGroupsResponse{Groups: make([]*ipamv1.IPGroup, 0, len(items))}
	for _, v := range items {
		pb := ipGroupToPB(v)
		if req.GetIncludeMembers() {
			members, merr := s.groups.ListIPGroupMembers(ctx, subj, v.ID)
			if merr != nil {
				return nil, grpcError(merr)
			}
			pb.Members = ipGroupMembersToPB(members)
		}
		out.Groups = append(out.Groups, pb)
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *IpGroupServer) Update(ctx context.Context, req *ipamv1.UpdateIpGroupRequest) (*ipamv1.IPGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := ipGroupFromPB(req.GetGroup())
	in.ID = req.GetId()
	v, err := s.groups.UpdateIPGroup(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return ipGroupToPB(v), nil
}

func (s *IpGroupServer) Delete(ctx context.Context, req *ipamv1.DeleteIpGroupRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.groups.DeleteIPGroup(ctx, subj, req.GetId()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *IpGroupServer) AddMember(ctx context.Context, req *ipamv1.AddIpGroupMemberRequest) (*ipamv1.IPGroupMember, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	m := ipGroupMemberFromPB(req.GetMember())
	m.IPGroupID = req.GetIpGroupId()
	v, err := s.groups.AddIPGroupMember(ctx, subj, m)
	if err != nil {
		return nil, grpcError(err)
	}
	return ipGroupMemberToPB(v), nil
}

func (s *IpGroupServer) UpdateMember(ctx context.Context, req *ipamv1.UpdateIpGroupMemberRequest) (*ipamv1.IPGroupMember, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	m := ipGroupMemberFromPB(req.GetMember())
	m.ID = req.GetMemberId()
	m.IPGroupID = req.GetIpGroupId()
	v, err := s.groups.UpdateIPGroupMember(ctx, subj, m)
	if err != nil {
		return nil, grpcError(err)
	}
	return ipGroupMemberToPB(v), nil
}

func (s *IpGroupServer) RemoveMember(ctx context.Context, req *ipamv1.RemoveIpGroupMemberRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.groups.RemoveIPGroupMember(ctx, subj, req.GetMemberId()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *IpGroupServer) ListMembers(ctx context.Context, req *ipamv1.ListIpGroupMembersRequest) (*ipamv1.ListIpGroupMembersResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.groups.ListIPGroupMembers(ctx, subj, req.GetIpGroupId())
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.ListIpGroupMembersResponse{Members: ipGroupMembersToPB(items)}, nil
}

func (s *IpGroupServer) CheckIpInGroup(ctx context.Context, req *ipamv1.CheckIpInGroupRequest) (*ipamv1.CheckIpInGroupResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	found, err := s.groups.CheckIpInGroup(ctx, subj, req.GetIp(), nil)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.CheckIpInGroupResponse{Groups: make([]*ipamv1.IPGroup, 0, len(found))}
	for _, v := range found {
		out.Groups = append(out.Groups, ipGroupToPB(v))
	}
	return out, nil
}

func ipGroupMembersToPB(members []store.IPGroupMember) []*ipamv1.IPGroupMember {
	out := make([]*ipamv1.IPGroupMember, 0, len(members))
	for _, m := range members {
		out = append(out, ipGroupMemberToPB(m))
	}
	return out
}

// ================= HostGroupService =================

// HostGroupServer implements ipam.v1.HostGroupService.
type HostGroupServer struct {
	ipamv1.UnimplementedHostGroupServiceServer
	groups *groups.Service
}

func (s *HostGroupServer) Create(ctx context.Context, req *ipamv1.CreateHostGroupRequest) (*ipamv1.HostGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.groups.CreateHostGroup(ctx, subj, hostGroupFromPB(req.GetGroup()))
	if err != nil {
		return nil, grpcError(err)
	}
	return hostGroupToPB(v), nil
}

func (s *HostGroupServer) Get(ctx context.Context, req *ipamv1.GetHostGroupRequest) (*ipamv1.HostGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	v, err := s.groups.GetHostGroup(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	pb := hostGroupToPB(v)
	if req.GetIncludeMembers() {
		members, merr := s.groups.ListHostGroupMembers(ctx, subj, v.ID)
		if merr != nil {
			return nil, grpcError(merr)
		}
		pb.Members = hostGroupMembersToPB(members)
	}
	return pb, nil
}

func (s *HostGroupServer) List(ctx context.Context, req *ipamv1.ListHostGroupsRequest) (*ipamv1.ListHostGroupsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.groups.ListHostGroups(ctx, subj, int(req.GetLimit()), req.GetCursorId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListHostGroupsResponse{Groups: make([]*ipamv1.HostGroup, 0, len(items))}
	for _, v := range items {
		pb := hostGroupToPB(v)
		if req.GetIncludeMembers() {
			members, merr := s.groups.ListHostGroupMembers(ctx, subj, v.ID)
			if merr != nil {
				return nil, grpcError(merr)
			}
			pb.Members = hostGroupMembersToPB(members)
		}
		out.Groups = append(out.Groups, pb)
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *HostGroupServer) Update(ctx context.Context, req *ipamv1.UpdateHostGroupRequest) (*ipamv1.HostGroup, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	in := hostGroupFromPB(req.GetGroup())
	in.ID = req.GetId()
	v, err := s.groups.UpdateHostGroup(ctx, subj, in)
	if err != nil {
		return nil, grpcError(err)
	}
	return hostGroupToPB(v), nil
}

func (s *HostGroupServer) Delete(ctx context.Context, req *ipamv1.DeleteHostGroupRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err := s.groups.DeleteHostGroup(ctx, subj, req.GetId()); err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.DeleteResponse{}, nil
}

func (s *HostGroupServer) AddMember(ctx context.Context, req *ipamv1.AddHostGroupMemberRequest) (*ipamv1.HostGroupMember, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	m := store.HostGroupMember{
		HostGroupID: req.GetHostGroupId(), DeviceID: req.GetDeviceId(), Sequence: int(req.GetSequence()),
	}
	v, err := s.groups.AddHostGroupMember(ctx, subj, m)
	if err != nil {
		return nil, grpcError(err)
	}
	return hostGroupMemberToPB(v), nil
}

// RemoveMember removes a device from a host group. The wire request identifies
// the member by device id, so the member row is resolved first.
func (s *HostGroupServer) RemoveMember(ctx context.Context, req *ipamv1.RemoveHostGroupMemberRequest) (*ipamv1.DeleteResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	members, err := s.groups.ListHostGroupMembers(ctx, subj, req.GetHostGroupId())
	if err != nil {
		return nil, grpcError(err)
	}
	for _, m := range members {
		if m.DeviceID == req.GetDeviceId() {
			if err := s.groups.RemoveHostGroupMember(ctx, subj, m.ID); err != nil {
				return nil, grpcError(err)
			}
			return &ipamv1.DeleteResponse{}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "not_found")
}

func (s *HostGroupServer) ListMembers(ctx context.Context, req *ipamv1.ListHostGroupMembersRequest) (*ipamv1.ListHostGroupMembersResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.groups.ListHostGroupMembers(ctx, subj, req.GetHostGroupId())
	if err != nil {
		return nil, grpcError(err)
	}
	return &ipamv1.ListHostGroupMembersResponse{Members: hostGroupMembersToPB(items)}, nil
}

func (s *HostGroupServer) ListDeviceHostGroups(ctx context.Context, req *ipamv1.ListDeviceHostGroupsRequest) (*ipamv1.ListHostGroupsResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	items, err := s.groups.ListDeviceHostGroups(ctx, subj, req.GetDeviceId())
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListHostGroupsResponse{Groups: make([]*ipamv1.HostGroup, 0, len(items))}
	for _, v := range items {
		out.Groups = append(out.Groups, hostGroupToPB(v))
	}
	return out, nil
}

func hostGroupMembersToPB(members []store.HostGroupMember) []*ipamv1.HostGroupMember {
	out := make([]*ipamv1.HostGroupMember, 0, len(members))
	for _, m := range members {
		out = append(out, hostGroupMemberToPB(m))
	}
	return out
}

// ================= IpScanService =================

// IpScanServer implements ipam.v1.IpScanService.
type IpScanServer struct {
	ipamv1.UnimplementedIpScanServiceServer
	scan *scan.Service
}

func (s *IpScanServer) Start(ctx context.Context, req *ipamv1.StartScanRequest) (*ipamv1.IPScanJob, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	job, err := s.scan.StartScan(ctx, subj, req.GetSubnetId(), scan.Options{
		EnableSNMP: req.GetEnableSnmp(), EnableDNSUpdate: req.GetEnableDnsUpdate(), SkipReverseDNS: req.GetSkipReverseDns(),
	})
	if err != nil {
		return nil, grpcError(err)
	}
	return scanJobToPB(job), nil
}

func (s *IpScanServer) Get(ctx context.Context, req *ipamv1.GetScanRequest) (*ipamv1.IPScanJob, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	job, err := s.scan.GetScanJob(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return scanJobToPB(job), nil
}

func (s *IpScanServer) List(ctx context.Context, req *ipamv1.ListScansRequest) (*ipamv1.ListScansResponse, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	f := store.ScanFilter{
		SubnetID: req.GetSubnetId(), Status: scanStatusFromPB(req.GetStatus()),
		Limit: int(req.GetLimit()), CursorID: req.GetCursorId(),
	}
	items, err := s.scan.ListScanJobs(ctx, subj, f)
	if err != nil {
		return nil, grpcError(err)
	}
	out := &ipamv1.ListScansResponse{Jobs: make([]*ipamv1.IPScanJob, 0, len(items))}
	for _, v := range items {
		out.Jobs = append(out.Jobs, scanJobToPB(v))
	}
	if n := len(items); n > 0 {
		out.NextCursorId = items[n-1].ID
	}
	return out, nil
}

func (s *IpScanServer) Cancel(ctx context.Context, req *ipamv1.CancelScanRequest) (*ipamv1.IPScanJob, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	job, err := s.scan.CancelScan(ctx, subj, req.GetId())
	if err != nil {
		return nil, grpcError(err)
	}
	return scanJobToPB(job), nil
}

// ================= SystemService =================

// SystemServer implements ipam.v1.SystemService.
type SystemServer struct {
	ipamv1.UnimplementedSystemServiceServer
	stats *stats.Service
	dns   *dnscfg.Service
}

func (s *SystemServer) Health(ctx context.Context, req *ipamv1.HealthRequest) (*ipamv1.HealthResponse, error) {
	return &ipamv1.HealthResponse{Status: "ok"}, nil
}

func (s *SystemServer) GetStats(ctx context.Context, req *ipamv1.GetStatsRequest) (*ipamv1.Stats, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if s.stats == nil {
		return nil, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	v, err := s.stats.Tenant(ctx, subj)
	if err != nil {
		return nil, grpcError(err)
	}
	return statsToPB(v), nil
}

func (s *SystemServer) GetDnsConfig(ctx context.Context, req *ipamv1.GetDnsConfigRequest) (*ipamv1.DNSConfig, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if s.dns == nil {
		return nil, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	v, err := s.dns.Get(ctx, subj)
	if err != nil {
		return nil, grpcError(err)
	}
	return dnsConfigToPB(v), nil
}

func (s *SystemServer) UpdateDnsConfig(ctx context.Context, req *ipamv1.UpdateDnsConfigRequest) (*ipamv1.DNSConfig, error) {
	subj, err := caller(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	if s.dns == nil {
		return nil, status.Error(codes.Unavailable, "temporarily_unavailable")
	}
	v, err := s.dns.Update(ctx, subj, dnsConfigFromPB(req.GetConfig()))
	if err != nil {
		return nil, grpcError(err)
	}
	return dnsConfigToPB(v), nil
}
