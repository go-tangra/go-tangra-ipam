// Package openapi embeds the IPAM browser API contract.
package openapi

import _ "embed"

// Ipam is the OpenAPI 3.1 document served and validated by the service.
//
//go:embed ipam.yaml
var Ipam []byte
