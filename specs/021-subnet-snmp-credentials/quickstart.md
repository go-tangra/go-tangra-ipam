# Quickstart: validate SNMP credentials on subnets (021)

## Prerequisites

- ipam built from this branch; KEK configured (`kek:` section, as today).
- An SNMP agent reachable from ipam: a real switch, or `snmpd` in a
  container (`net-snmp`) configured with a v2c community and a v3 user
  (`createUser lab SHA-256 labauthpass AES-256 labprivpass`).

## Automated

```bash
go test -race ./internal/snmpcred/... ./internal/scan/... ./internal/httpapi/... ./internal/subnets/...
make test-integration          # migration 0006 upgrade, RLS on ipam_subnet_snmp, seal/open against PostgreSQL
make cover                     # snmpcred 100 %, sealed 100 %
(cd ui && npm run lint && npm run test:unit)
```

## Manual (stack)

1. Open IPAM → Subnets → edit a parent subnet → "SNMP credentials" → Set:
   v3, authPriv, SHA-256/AES-256 → Save. Status shows "own · v3 authPriv";
   no value is shown again. `GET /api/ipam/v1/subnets/{id}/snmp` returns no
   secret fields.
2. Open a child subnet: status "inherited from <parent>".
3. "Test SNMP" on the child with the agent's address → `ok` with sysName.
   With an address outside the child → validation error. 11 tests in a
   minute → 429.
4. Scans → start a scan of the child with SNMP discovery → the agent appears
   under Devices with interfaces; the job shows "SNMP: ran (inherited from
   <parent>) · probed N · discovered 1".
5. Clear the parent's credentials → child shows "not configured"; scan again
   → "SNMP skipped: no credentials".
6. Audit (ipam audit events) shows set / tested / cleared rows with version
   only; searching the logs and a tenant backup for the community finds
   nothing (SC-003).
