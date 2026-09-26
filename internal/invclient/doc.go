// Package invclient is IPAM's view of the inventory module's
// HostReportService (feature 020, research D4): tenants with changed reports,
// host reports of one tenant (paged to exhaustion) and one host's latest
// report, as plain Go values. Every transport failure is reported as
// ErrUnavailable (with a sanitised code) so the host sync degrades without
// writing anything; the service identity and tenant scoping of the mesh
// connection are the only credentials involved.
package invclient
