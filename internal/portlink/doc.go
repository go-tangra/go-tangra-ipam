// Package portlink correlates the MAC addresses reported by hosts with the
// switch forwarding tables and LLDP neighbours IPAM's SNMP scans collect
// (feature 020, US5, research D17) and links each host interface to the
// switch port it is connected to. Ranking is pure; applying it writes only the
// host interface link columns, inside the tenant's scope, with an audit row.
package portlink
