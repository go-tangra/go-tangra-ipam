package snmpcred

import (
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
)

var secretFields = []string{"community", "user", "auth_password", "priv_password"}

// ToCreds maps stored metadata and opened secret values to the SNMP client's
// credentials for one operation.
func ToCreds(m Meta, s Secret, timeoutMs, retries int) snmp.Creds {
	c := snmp.Creds{Version: m.Version, TimeoutMs: timeoutMs, Retries: retries}
	if m.Version != 3 {
		c.Community = s.Community
		return c
	}
	c.User, c.SecurityLevel = s.User, m.SecurityLevel
	c.AuthProtocol, c.AuthPassword = m.AuthProtocol, s.AuthPassword
	if m.SecurityLevel == LevelAuthPriv {
		c.PrivProtocol, c.PrivPassword = m.PrivProtocol, s.PrivPassword
	}
	return c
}

// Scrub removes every credential value (and its base64 form) from text,
// keeping only the first line (SR-004).
func Scrub(text string, s Secret) string {
	v := s.values()
	return sealed.Scrub(text, sealed.Settings{"community": v[0], "user": v[1], "auth_password": v[2], "priv_password": v[3]}, secretFields)
}
