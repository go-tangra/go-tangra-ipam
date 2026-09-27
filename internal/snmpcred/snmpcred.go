// Package snmpcred owns every rule that protects subnet SNMP credentials
// (feature 021): input validation (FR-003), sealing of the secret values with
// the module envelope bound to tenant+subnet (SR-001), inheritance from the
// nearest ancestor subnet (FR-011), the mapping to the SNMP client's
// credentials and the scrubbing of credential values from error text
// (SR-004). It is pure: no store, no network.
package snmpcred

import "log/slog"

// Limits and names.
const (
	MaxValueLen    = 256 // characters, every value
	MinPasswordLen = 8   // v3 passwords (protocol minimum)
	MaxDepth       = 64  // inheritance walk bound

	LevelAuthNoPriv = "authNoPriv"
	LevelAuthPriv   = "authPriv"
)

// redacted is what formatting or logging a credential-carrying type prints.
const redacted = "[REDACTED]"

var (
	authProtocols = []string{"MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512"}
	privProtocols = []string{"DES", "AES", "AES192", "AES256", "AES192C", "AES256C"}
	weak          = map[string]bool{"MD5": true, "SHA": true, "DES": true}
)

func validAuth(p string) bool { return contains(authProtocols, p) }
func validPriv(p string) bool { return contains(privProtocols, p) }

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// Weak reports whether a protocol is kept only for old devices (MD5, SHA-1,
// DES; FR-002).
func Weak(protocol string) bool { return weak[protocol] }

// Meta is the non-secret part of a credential set, stored in clear.
type Meta struct {
	Version       int
	SecurityLevel string
	AuthProtocol  string
	PrivProtocol  string
}

// Weak reports whether the set uses a weak protocol.
func (m Meta) Weak() bool { return Weak(m.AuthProtocol) || Weak(m.PrivProtocol) }

// Secret holds the secret values; it only ever exists sealed at rest and
// decrypted for the duration of one SNMP operation (FR-010).
type Secret struct {
	Community    string `json:"community,omitempty"`
	User         string `json:"user,omitempty"`
	AuthPassword string `json:"auth_password,omitempty"`
	PrivPassword string `json:"priv_password,omitempty"`
}

// String never prints a value.
func (Secret) String() string { return "snmpcred.Secret" + redacted }

// GoString never prints a value.
func (Secret) GoString() string { return "snmpcred.Secret" + redacted }

// LogValue never logs a value.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

func (s Secret) values() []string {
	return []string{s.Community, s.User, s.AuthPassword, s.PrivPassword}
}
