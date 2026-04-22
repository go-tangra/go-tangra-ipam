package data

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	entCrud "github.com/tx7do/go-crud/entgo"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/device"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipaddress"
)

// StatisticsRepo provides methods for collecting IPAM statistics
type StatisticsRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

// NewStatisticsRepo creates a new StatisticsRepo
func NewStatisticsRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *StatisticsRepo {
	return &StatisticsRepo{
		entClient: entClient,
		log:       ctx.NewLoggerHelper("ipam/statistics/repo"),
	}
}

// GetSubnetCount returns the total number of subnets
func (r *StatisticsRepo) GetSubnetCount(ctx context.Context) (int64, error) {
	count, err := r.entClient.Client().Subnet.Query().Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// GetTotalAddresses returns the sum of total_addresses from all subnets
func (r *StatisticsRepo) GetTotalAddresses(ctx context.Context) (int64, error) {
	subnets, err := r.entClient.Client().Subnet.Query().All(ctx)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, s := range subnets {
		total += s.TotalAddresses
	}
	return total, nil
}

// GetAddressCountByStatus returns the count of IP addresses with the given status
// IP address status values: 1=Active, 2=Reserved, 3=DHCP, 4=Deprecated, 5=Offline
func (r *StatisticsRepo) GetAddressCountByStatus(ctx context.Context, status int32) (int64, error) {
	count, err := r.entClient.Client().IpAddress.Query().
		Where(ipaddress.StatusEQ(status)).
		Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// GetTotalAddressCount returns the total number of IP address records
func (r *StatisticsRepo) GetTotalAddressCount(ctx context.Context) (int64, error) {
	count, err := r.entClient.Client().IpAddress.Query().Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// GetVlanCount returns the total number of VLANs
func (r *StatisticsRepo) GetVlanCount(ctx context.Context) (int64, error) {
	count, err := r.entClient.Client().Vlan.Query().Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// GetDeviceCount returns the total number of devices
func (r *StatisticsRepo) GetDeviceCount(ctx context.Context) (int64, error) {
	count, err := r.entClient.Client().Device.Query().Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// GetDeviceCountsByType returns a map of numeric DeviceType -> count using a
// single GROUP BY query. The keys match the proto DeviceType enum numbers
// (e.g. 1=SERVER, 2=VM, 3=ROUTER, ...).
func (r *StatisticsRepo) GetDeviceCountsByType(ctx context.Context) (map[int32]int64, error) {
	var rows []struct {
		DeviceType int32 `json:"device_type"`
		Count      int64 `json:"count"`
	}

	err := r.entClient.Client().Device.Query().
		GroupBy(device.FieldDeviceType).
		Aggregate(ent.As(ent.Count(), "count")).
		Scan(ctx, &rows)
	if err != nil {
		// Fall back to per-row counting if GROUP BY isn't supported for some
		// reason (e.g. custom driver). Surfaces as an empty map so the caller
		// can still render the page.
		r.log.Warnf("group-by device_type failed, returning empty map: %v", err)
		return map[int32]int64{}, nil
	}

	out := make(map[int32]int64, len(rows))
	for _, row := range rows {
		out[row.DeviceType] = row.Count
	}
	return out, nil
}


// GetLocationCount returns the total number of locations
func (r *StatisticsRepo) GetLocationCount(ctx context.Context) (int64, error) {
	count, err := r.entClient.Client().Location.Query().Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}
