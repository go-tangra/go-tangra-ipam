# Quickstart: validate ARP-based MAC linking (022)

## Automated

```bash
go test -race ./internal/arpplan/... ./internal/portlink/... ./internal/scan/... ./internal/httpapi/... ./internal/addresses/...
sg docker -c 'make test-integration'   # migration 0008 upgrade + backfill, RLS on ipam_arp_settings, ApplyARP, MAC search index
make cover                              # arpplan 100 %, existing gates unchanged
(cd ui && npm run lint && npm run test:unit && npm run build)
```

## Manual (production-like stack)

1. The network-devices subnet (e.g. 192.168.100.0/24) has SNMP credentials
   (feature 021). Scan it (the quick Scan runs SNMP automatically).
2. The scan line shows "ARP: 3 devices · N entries · applied A · created C ·
   ignored I (proxy_arp x, network_device y …)".
3. IP Addresses of a routed subnet (servers, IPMI): MACs are filled with
   source "ARP (MikroTik)" and a last-seen time; ns1 keeps its agent MAC.
4. Addresses whose MAC is learned on an access port show "Connected to
   <switch> <port> (VLAN n)"; the switch's interface list shows the address
   behind the port.
5. Search IP Addresses by a partial MAC (`0a5c`, `d2-f1`) → the address with
   its port.
6. Settings: disable ARP → the next scan shows "ARP: disabled" and changes no
   MAC; exclude the Fortinet → its entries are counted as `excluded_device`.
7. Audit shows `mac_learned`/`mac_changed`/`address_created` (origin arp),
   `port_linked` for addresses and one `arp_run` per scan.
