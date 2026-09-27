package arpplan

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// largeInput: 10 devices each reporting 5,000 entries over a /16 with 5,000
// existing addresses (SC-006 scale, research D10).
func largeInput() Input {
	in := Input{TenantID: "t1", JobID: "j1", Now: time.Now(), Subnets: []store.Subnet{{ID: "s", TenantID: "t1", CIDR: "10.0.0.0/16"}}}
	for d := 0; d < 10; d++ {
		o := Observation{DeviceID: fmt.Sprintf("r%02d", d)}
		for i := 0; i < 5000; i++ {
			o.Entries = append(o.Entries, snmp.ARPEntry{IP: fmt.Sprintf("10.0.%d.%d", i/250, i%250+1), MAC: fmt.Sprintf("0a:00:00:00:%02x:%02x", i/256, i%256)})
		}
		in.Observations = append(in.Observations, o)
	}
	for i := 0; i < 5000; i++ {
		a := store.IPAddress{ID: fmt.Sprintf("a%d", i), TenantID: "t1", Address: fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)}
		if i%2 == 0 {
			a.MACAddress, a.MACSource = fmt.Sprintf("0a:00:00:00:%02x:%02x", i/256, i%256), store.MACSourceARP
		}
		in.Addresses = append(in.Addresses, a)
	}
	return in
}

func TestPlanScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	in := largeInput()
	start := time.Now()
	p := Build(in)
	took := time.Since(start)
	if p.Entries != 50000 || len(p.Ops) != 5000 {
		t.Fatalf("entries %d ops %d", p.Entries, len(p.Ops))
	}
	if limit := 2 * time.Second; took > limit { // ~70 ms measured; margin for -race under load
		t.Fatalf("planning 50,000 entries took %v (limit %v)", took, limit)
	}
	t.Logf("planned 50,000 entries against 5,000 addresses in %v", took)
}

func BenchmarkPlan(b *testing.B) {
	in := largeInput()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Build(in)
	}
}
