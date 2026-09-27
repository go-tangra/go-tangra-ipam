package arpplan

import (
	"net/netip"
	"sort"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Build plans one tenant's ARP changes (research D2). Observations are
// processed in device-id order and, for an IP several devices report, the
// last one wins; ops come out in address order.
func Build(in Input) Plan {
	p := Plan{Ignored: map[string]int{}}
	threshold := in.ProxyThreshold
	if threshold < 1 {
		threshold = store.ARPDefaultProxyThreshold
	}
	newID := in.NewID
	if newID == nil {
		newID = store.NewID
	}
	excluded := map[string]bool{}
	for _, d := range in.ExcludedDevices {
		excluded[d] = true
	}

	obs := append([]Observation(nil), in.Observations...)
	sort.SliceStable(obs, func(i, j int) bool { return obs[i].DeviceID < obs[j].DeviceID })
	var cands []candidate
	for _, o := range obs {
		for _, e := range o.Entries {
			p.Entries++
			if excluded[o.DeviceID] {
				p.Ignored[store.ARPIgnoredExcluded]++
				continue
			}
			ip, err := netip.ParseAddr(e.IP)
			if err != nil {
				p.Ignored[store.ARPIgnoredInvalid]++
				continue
			}
			mac, reason := classifyMAC(e.MAC, in.NetworkMACs)
			if reason != "" {
				p.Ignored[reason]++
				continue
			}
			cands = append(cands, candidate{ip: ip.Unmap().WithZone(""), mac: mac, device: o.DeviceID})
		}
	}
	cands = filterProxy(cands, threshold, p.Ignored)

	var subnets []ipnet.Candidate
	for _, s := range in.Subnets {
		if s.TenantID == in.TenantID {
			subnets = append(subnets, ipnet.Candidate{ID: s.ID, CIDR: s.CIDR})
		}
	}
	winner := map[netip.Addr]candidate{}
	placed := map[netip.Addr]string{}
	disagree := map[netip.Addr]bool{}
	for _, c := range cands {
		sid, ok := subnetOf(subnets, c.ip)
		if !ok {
			p.Ignored[store.ARPIgnoredOutside]++
			continue
		}
		if prev, seen := winner[c.ip]; seen && prev.mac != c.mac {
			disagree[c.ip] = true
		}
		winner[c.ip], placed[c.ip] = c, sid
	}
	p.Conflicts += len(disagree)

	addrs := map[netip.Addr]store.IPAddress{}
	for _, a := range in.Addresses {
		if ip, err := netip.ParseAddr(a.Address); err == nil && a.TenantID == in.TenantID {
			addrs[ip.Unmap().WithZone("")] = a
		}
	}
	ips := make([]netip.Addr, 0, len(winner))
	for ip := range winner {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
	pl := planner{in: in, p: &p}
	for _, ip := range ips {
		c := winner[ip]
		a, ok := addrs[ip]
		if !ok {
			pl.create(c, placed[ip], newID())
			continue
		}
		pl.address(a, c)
	}
	return p
}

type planner struct {
	in Input
	p  *Plan
}

func (pl planner) row(t audit.EventType, subjectID string, detail map[string]any) store.AuditRow {
	r, _ := audit.Row(audit.Event{TenantID: pl.in.TenantID, EventType: t, ActorKind: audit.ActorSystem,
		ActorID: audit.ScanActor, SubjectKind: audit.SubjectAddress, SubjectID: subjectID, Outcome: audit.OutcomeOK,
		Details: detail}, pl.in.Now)
	return r
}

func (pl planner) op(kind string, a store.IPAddress, c candidate, rows ...store.AuditRow) {
	pl.p.Ops = append(pl.p.Ops, store.ARPOp{Kind: kind, AddressID: a.ID, Address: c.ip.String(), SubnetID: a.SubnetID,
		MAC: c.mac, SourceDeviceID: c.device, At: pl.in.Now, Audit: rows})
}

func (pl planner) create(c candidate, subnetID, id string) {
	addr := c.ip.String()
	pl.op(store.ARPCreate, store.IPAddress{ID: id, SubnetID: subnetID}, c, pl.row(audit.AddressCreated, id, map[string]any{
		"address": addr, "origin": store.OriginARP, "mac": c.mac, "subnet_id": subnetID, "source_device_id": c.device}))
	pl.p.Created++
}

// address applies the provenance table (data-model §5) to an existing row. A
// stored MAC without a source predates the feature and counts as manual.
func (pl planner) address(a store.IPAddress, c candidate) {
	addr := c.ip.String()
	stored, _ := hostreport.NormalizeMAC(a.MACAddress)
	switch {
	case a.MACAddress == "":
		pl.op(store.ARPFill, a, c, pl.row(audit.MACLearned, a.ID, map[string]any{
			"address": addr, "mac": c.mac, "source_device_id": c.device, "job_id": pl.in.JobID}))
		pl.p.Applied++
	case stored == c.mac && a.MACConflict != "":
		pl.op(store.ARPClearConflict, a, c)
	case stored == c.mac:
		pl.op(store.ARPTouch, a, c)
	case a.MACSource == store.MACSourceARP:
		pl.op(store.ARPUpdate, a, c, pl.row(audit.MACChanged, a.ID, map[string]any{
			"address": addr, "mac": c.mac, "previous_mac": a.MACAddress, "source_device_id": c.device, "job_id": pl.in.JobID}))
		pl.p.Applied++
	default: // agent, manual or pre-022: never overwritten
		pl.p.Conflicts++
		if a.MACConflict != c.mac {
			pl.op(store.ARPConflict, a, c, pl.row(audit.MACConflict, a.ID, map[string]any{
				"address": addr, "mac": a.MACAddress, "observed_mac": c.mac, "source_device_id": c.device}))
		}
	}
}
