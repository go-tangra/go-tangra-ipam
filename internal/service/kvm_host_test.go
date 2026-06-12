package service

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
)

func TestBmcHostFromDevice(t *testing.T) {
	cases := []struct {
		name     string
		metadata string
		mgmtIP   string
		want     string
	}{
		{"ipmi metadata wins", `{"ipmi":{"ip":"10.1.112.18"}}`, "10.9.9.9", "10.1.112.18"},
		{"falls back to management_ip", `{"machine_id":"x"}`, "10.1.112.20", "10.1.112.20"},
		{"empty metadata uses mgmt", "", "10.1.112.21", "10.1.112.21"},
		{"nothing", `{}`, "", ""},
		{"bad json uses mgmt", `{not json`, "10.1.112.22", "10.1.112.22"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &ent.Device{Metadata: c.metadata, ManagementIP: c.mgmtIP}
			if got := bmcHostFromDevice(d); got != c.want {
				t.Fatalf("bmcHostFromDevice = %q, want %q", got, c.want)
			}
		})
	}
}
