// Package hostreport is the trust boundary between the inventory module and
// the IPAM host sync (feature 020, research D15). It converts a host report
// fetched from inventory into a normalised report: the report's tenant must
// equal the tenant the sync asked for (cross-tenant guard), host ids are
// uuids, names are printable and bounded, MACs are canonical and unicast,
// addresses parse, and every list is capped. Invalid entries are skipped and
// returned as issues rather than silently dropped. It also validates and
// applies the per-tenant interface exclusion patterns. Nothing in this
// package writes anything; it is pure and held at 100 % coverage.
package hostreport
