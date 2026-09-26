// Package hostplan is the pure host-sync planner (feature 020, research
// D6-D13): given the IPAM state loaded for one host and its normalised report,
// it decides which device, interface, subnet, address, package and hypervisor
// changes to make and the audit row that records each of them. It is the
// place that enforces field ownership (D8): administrator fields (description,
// tags, location, rack, asset tag, status, contact, ipmi_secret_ref, firmware,
// group membership, address notes/owners/DNS names) never appear in any op.
// It is deterministic, has no I/O and is held at 100 % coverage and fuzzed
// (idempotence and the admin-field invariant).
package hostplan
