// Package hostsync runs the IPAM host sync (feature 020): a changed-since
// poller and a per-tenant reconcile pull host reports from the inventory
// module over the mesh, validate them (internal/hostreport), plan the changes
// (internal/hostplan) and apply each host in one tenant-scoped transaction
// with its audit rows (repo.HostSyncStore). It never accepts data from agents
// directly (SR-004), stops writing as soon as a tenant disables the sync
// (SR-006) and exposes the administrator operations (settings, status,
// re-sync, conflict clearing).
package hostsync
