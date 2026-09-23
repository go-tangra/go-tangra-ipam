package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-freya/freya/services/ipam/internal/backup"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

// ipamBase is the gateway-proxied prefix every route shares.
const ipamBase = "/api/ipam/v1"

// MaxImportBytes bounds a backup import body (matches the route's declared
// x-freya-max-body-bytes).
const MaxImportBytes = 256 << 20

// Register mounts every IPAM HTTP route declared in the OpenAPI document. Each
// route resolves the caller from the gateway-forwarded platform token, derives
// a tenant-scoped subject, and delegates to a domain service. SNMP/BMC/IPMI
// credentials, sealed owner/contact fields and secret material are never
// returned; only opaque warden refs and non-sensitive metadata are exposed.
// The out-of-band power/KVM routes are mounted by registerPower (power.go); the
// realtime SSE stream is mounted only when Deps.Hub is set.
func (s *Server) Register(d Deps) {
	p := ipamBase

	// ---------------------------------------------------------------- Subnets
	s.MustHandle("GET", p+"/subnets", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Subnets.List(r.Context(), subj, store.SubnetFilter{
			VlanID:     q.Get("vlan_id"),
			ParentID:   q.Get("parent_id"),
			LocationID: q.Get("location_id"),
			Status:     q.Get("status"),
			Query:      q.Get("query"),
			Limit:      atoiDefault(q.Get("limit"), 0),
			CursorID:   q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/subnets", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Subnet
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Subnets.Create(r.Context(), subj, in, boolParam(r, "allow_overlap"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	// Split a subnet into the /prefix_length children it contains (they inherit
	// its VLAN and location); blocks colliding with an existing subnet come back
	// as skipped. dry_run previews the result without writing anything.
	s.MustHandle("POST", p+"/subnets/{id}/split", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			PrefixLength int  `json:"prefix_length"`
			DryRun       bool `json:"dry_run"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Subnets.Split(r.Context(), subj, r.PathValue("id"), in.PrefixLength, in.DryRun)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("GET", p+"/subnets/tree", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		tree, err := d.Subnets.GetTree(r.Context(), subj)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"tree": tree})
	})
	s.MustHandle("GET", p+"/subnets/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Subnets.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/subnets/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Subnet
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Subnets.Update(r.Context(), subj, in, boolParam(r, "allow_overlap"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/subnets/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Subnets.Delete(r.Context(), subj, r.PathValue("id"), boolParam(r, "force")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/subnets/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Subnets.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"subnet_id":           v.ID,
			"cidr":                v.CIDR,
			"total_addresses":     v.TotalAddresses,
			"used_addresses":      v.UsedAddresses,
			"available_addresses": v.AvailableAddresses,
			"utilization":         v.Utilization,
		})
	})
	s.MustHandle("POST", p+"/subnets/{id}/scan", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		opts, err := decodeScanOptions(r)
		if err != nil {
			Fail(w, r, nil, err)
			return
		}
		job, err := d.Scan.StartScan(r.Context(), subj, r.PathValue("id"), opts)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	})

	// ----------------------------------------------------------- IP addresses
	s.MustHandle("GET", p+"/ip-addresses", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Addresses.List(r.Context(), subj, store.AddressFilter{
			SubnetID:    q.Get("subnet_id"),
			DeviceID:    q.Get("device_id"),
			Status:      q.Get("status"),
			AddressType: q.Get("address_type"),
			Limit:       atoiDefault(q.Get("limit"), 0),
			CursorID:    q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/ip-addresses", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPAddress
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Addresses.Create(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("POST", p+"/ip-addresses/allocate", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			SubnetID  string   `json:"subnet_id"`
			StartFrom string   `json:"start_from"`
			Skip      []string `json:"skip"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Addresses.AllocateNext(r.Context(), subj, in.SubnetID, in.StartFrom, in.Skip)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("POST", p+"/ip-addresses/bulk-allocate", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			SubnetID string `json:"subnet_id"`
			Count    int    `json:"count"`
			Prefix   string `json:"prefix"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		items, err := d.Addresses.BulkAllocate(r.Context(), subj, in.SubnetID, in.Count, in.Prefix)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"items": items})
	})
	s.MustHandle("GET", p+"/ip-addresses/find", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Addresses.Find(r.Context(), subj, r.URL.Query().Get("address"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("GET", p+"/ip-addresses/suggest", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		sugg, err := d.Addresses.SuggestAvailableAddresses(r.Context(), subj, q.Get("subnet_id"), atoiDefault(q.Get("count"), 1), nil)
		if err != nil {
			failSvc(w, err)
			return
		}
		if sugg == nil {
			sugg = []string{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"suggestions": sugg})
	})
	s.MustHandle("GET", p+"/ip-addresses/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Addresses.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/ip-addresses/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPAddress
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Addresses.Update(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/ip-addresses/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Addresses.Delete(r.Context(), subj, r.PathValue("id")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("POST", p+"/ip-addresses/{id}/ping", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		res, err := d.Addresses.PingAddress(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	// ---------------------------------------------------------------- Devices
	s.MustHandle("GET", p+"/devices", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Devices.List(r.Context(), subj, store.DeviceFilter{
			DeviceType:   q.Get("device_type"),
			Status:       q.Get("status"),
			LocationID:   q.Get("location_id"),
			Manufacturer: q.Get("manufacturer"),
			RackID:       q.Get("rack_id"),
			Query:        q.Get("query"),
			Limit:        atoiDefault(q.Get("limit"), 0),
			CursorID:     q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/devices", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Device
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Devices.Create(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("GET", p+"/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Devices.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Device
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Devices.Update(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Devices.Delete(r.Context(), subj, r.PathValue("id"), boolParam(r, "force")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/devices/{id}/addresses", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Devices.GetAddresses(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("GET", p+"/devices/{id}/interfaces", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Devices.ListInterfaces(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/devices/{id}/interfaces", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.DeviceInterface
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Devices.CreateInterface(r.Context(), subj, r.PathValue("id"), in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("DELETE", p+"/devices/{id}/interfaces/{ifid}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Devices.DeleteInterface(r.Context(), subj, r.PathValue("ifid")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/devices/{id}/packages", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Devices.ListPackages(r.Context(), subj, r.PathValue("id"),
			optBool(q, "needs_update"), optBool(q, "security_only"), q.Get("manager"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/devices/{id}/packages/sync", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			Packages []store.DevicePackage `json:"packages"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		res, err := d.Devices.SyncPackages(r.Context(), subj, r.PathValue("id"), in.Packages)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
	s.MustHandle("GET", p+"/devices/{id}/host-groups", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Groups.ListDeviceHostGroups(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})

	// --------------------------------------------------- Warden secrets (meta)
	s.MustHandle("GET", p+"/warden-secrets", func(w http.ResponseWriter, r *http.Request) {
		if _, err := subjects(r); err != nil {
			failSvc(w, err)
			return
		}
		metas, err := d.Warden.ListSecrets(r.Context(), r.URL.Query().Get("query"))
		if err != nil {
			failSvc(w, err)
			return
		}
		if metas == nil {
			metas = []warden.SecretMeta{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": metas})
	})
	s.MustHandle("GET", p+"/warden-secrets/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := subjects(r); err != nil {
			failSvc(w, err)
			return
		}
		id := r.PathValue("id")
		metas, err := d.Warden.ListSecrets(r.Context(), "")
		if err != nil {
			failSvc(w, err)
			return
		}
		for _, m := range metas {
			if m.ID == id {
				WriteJSON(w, http.StatusOK, m)
				return
			}
		}
		WriteError(w, http.StatusNotFound, "not_found")
	})

	s.registerNetworking(d, p)
	s.registerGroups(d, p)
	s.registerScans(d, p)
	s.registerSystem(d, p)
	s.registerPower(d, p)

	// Realtime SSE stream (optional: only when a hub is wired).
	if d.Hub != nil {
		s.RegisterStream(d.Hub)
	}
}

// registerNetworking mounts VLAN and location routes.
func (s *Server) registerNetworking(d Deps, p string) {
	// ------------------------------------------------------------------ VLANs
	s.MustHandle("GET", p+"/vlans", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Vlans.List(r.Context(), subj, store.VlanFilter{
			LocationID: q.Get("location_id"),
			Domain:     q.Get("domain"),
			Status:     q.Get("status"),
			Limit:      atoiDefault(q.Get("limit"), 0),
			CursorID:   q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/vlans", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Vlan
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Vlans.Create(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("GET", p+"/vlans/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Vlans.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/vlans/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Vlan
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Vlans.Update(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/vlans/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Vlans.Delete(r.Context(), subj, r.PathValue("id"), boolParam(r, "force")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/vlans/{id}/subnets", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Vlans.GetSubnets(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})

	// -------------------------------------------------------------- Locations
	s.MustHandle("GET", p+"/locations", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Locations.List(r.Context(), subj, store.LocationFilter{
			ParentID:     q.Get("parent_id"),
			LocationType: q.Get("location_type"),
			Country:      q.Get("country"),
			Status:       q.Get("status"),
			Limit:        atoiDefault(q.Get("limit"), 0),
			CursorID:     q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/locations", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Location
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Locations.Create(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("GET", p+"/locations/tree", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		tree, err := d.Locations.GetTree(r.Context(), subj)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"tree": tree})
	})
	s.MustHandle("GET", p+"/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Locations.Get(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.Location
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Locations.Update(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/locations/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Locations.Delete(r.Context(), subj, r.PathValue("id"), boolParam(r, "force")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// registerGroups mounts IP-group and host-group routes.
func (s *Server) registerGroups(d Deps, p string) {
	// --------------------------------------------------------------- IP groups
	s.MustHandle("GET", p+"/ip-groups", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Groups.ListIPGroups(r.Context(), subj, atoiDefault(q.Get("limit"), 0), q.Get("cursor"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/ip-groups", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPGroup
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Groups.CreateIPGroup(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("GET", p+"/ip-groups/check", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		match, err := d.Groups.CheckIpInGroup(r.Context(), subj, r.URL.Query().Get("ip"), nil)
		if err != nil {
			failSvc(w, err)
			return
		}
		if match == nil {
			match = []store.IPGroup{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"matching_groups": match})
	})
	s.MustHandle("GET", p+"/ip-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Groups.GetIPGroup(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/ip-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPGroup
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Groups.UpdateIPGroup(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/ip-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Groups.DeleteIPGroup(r.Context(), subj, r.PathValue("id")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/ip-groups/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Groups.ListIPGroupMembers(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/ip-groups/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPGroupMember
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.IPGroupID = r.PathValue("id")
		v, err := d.Groups.AddIPGroupMember(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("PUT", p+"/ip-groups/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.IPGroupMember
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("mid")
		in.IPGroupID = r.PathValue("id")
		v, err := d.Groups.UpdateIPGroupMember(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/ip-groups/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Groups.RemoveIPGroupMember(r.Context(), subj, r.PathValue("mid")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// ------------------------------------------------------------- Host groups
	s.MustHandle("GET", p+"/host-groups", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Groups.ListHostGroups(r.Context(), subj, atoiDefault(q.Get("limit"), 0), q.Get("cursor"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/host-groups", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.HostGroup
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Groups.CreateHostGroup(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("GET", p+"/host-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Groups.GetHostGroup(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/host-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.HostGroup
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("id")
		v, err := d.Groups.UpdateHostGroup(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/host-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Groups.DeleteHostGroup(r.Context(), subj, r.PathValue("id")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.MustHandle("GET", p+"/host-groups/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		items, err := d.Groups.ListHostGroupMembers(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/host-groups/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.HostGroupMember
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.HostGroupID = r.PathValue("id")
		v, err := d.Groups.AddHostGroupMember(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusCreated, v)
	})
	s.MustHandle("PUT", p+"/host-groups/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.HostGroupMember
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		in.ID = r.PathValue("mid")
		in.HostGroupID = r.PathValue("id")
		v, err := d.Groups.UpdateHostGroupMember(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("DELETE", p+"/host-groups/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := d.Groups.RemoveHostGroupMember(r.Context(), subj, r.PathValue("mid")); err != nil {
			failSvc(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// registerScans mounts the async discovery scan routes.
func (s *Server) registerScans(d Deps, p string) {
	s.MustHandle("GET", p+"/ip-scans", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		q := r.URL.Query()
		items, err := d.Scan.ListScanJobs(r.Context(), subj, store.ScanFilter{
			SubnetID: q.Get("subnet_id"),
			Status:   q.Get("status"),
			Limit:    atoiDefault(q.Get("limit"), 0),
			CursorID: q.Get("cursor"),
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	s.MustHandle("POST", p+"/ip-scans", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			SubnetID        string `json:"subnet_id"`
			EnableSNMP      bool   `json:"enable_snmp"`
			EnableDNSUpdate bool   `json:"enable_dns_update"`
			SkipReverseDNS  bool   `json:"skip_reverse_dns"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		job, err := d.Scan.StartScan(r.Context(), subj, in.SubnetID, scan.Options{
			EnableSNMP:      in.EnableSNMP,
			EnableDNSUpdate: in.EnableDNSUpdate,
			SkipReverseDNS:  in.SkipReverseDNS,
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, job)
	})
	s.MustHandle("GET", p+"/ip-scans/{id}", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		job, err := d.Scan.GetScanJob(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	})
	s.MustHandle("POST", p+"/ip-scans/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		job, err := d.Scan.CancelScan(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	})
}

// registerSystem mounts the health, stats, DNS-config and backup routes.
func (s *Server) registerSystem(d Deps, p string) {
	// Public health check (no authentication).
	s.MustHandle("GET", p+"/health", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	s.MustHandle("GET", p+"/stats", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Stats.Tenant(r.Context(), subj)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("GET", p+"/dns-config", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.DNS.Get(r.Context(), subj)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p+"/dns-config", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in store.DNSConfig
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.DNS.Update(r.Context(), subj, in)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("POST", p+"/dns-config/test", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			TestIP string `json:"test_ip"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		hostname, latency, terr := d.DNS.Test(r.Context(), subj, in.TestIP)
		errStr := ""
		if terr != nil {
			errStr = terr.Error()
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"hostname":   hostname,
			"latency_ms": latency,
			"error":      errStr,
		})
	})
	s.MustHandle("POST", p+"/backup/export", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			IncludeSecrets bool `json:"include_secrets"`
		}
		if r.ContentLength != 0 {
			if err := DecodeJSON(r, &in, 0); err != nil {
				Fail(w, r, nil, err)
				return
			}
		}
		b, err := d.Backup.Export(r.Context(), subj, in.IncludeSecrets)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, b)
	})
	s.MustHandle("POST", p+"/backup/import", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in struct {
			Mode   string        `json:"mode"`
			Backup backup.Backup `json:"backup"`
		}
		if err := DecodeJSON(r, &in, MaxImportBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		res, err := d.Backup.Import(r.Context(), subj, in.Backup, in.Mode)
		if err != nil {
			failSvc(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
}

// decodeScanOptions reads the optional scan toggles from a request body.
func decodeScanOptions(r *http.Request) (scan.Options, error) {
	var in struct {
		EnableSNMP      bool `json:"enable_snmp"`
		EnableDNSUpdate bool `json:"enable_dns_update"`
		SkipReverseDNS  bool `json:"skip_reverse_dns"`
	}
	if r.ContentLength != 0 {
		if err := DecodeJSON(r, &in, 0); err != nil {
			return scan.Options{}, err
		}
	}
	return scan.Options{
		EnableSNMP:      in.EnableSNMP,
		EnableDNSUpdate: in.EnableDNSUpdate,
		SkipReverseDNS:  in.SkipReverseDNS,
	}, nil
}

// atoiDefault parses s as an int, returning def when s is empty or invalid.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// boolParam reports whether query parameter key is present and truthy.
func boolParam(r *http.Request, key string) bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// optBool returns a pointer to the parsed bool for key, or nil when absent or
// unparseable (so a service can distinguish "unset" from "false").
func optBool(q url.Values, key string) *bool {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}
