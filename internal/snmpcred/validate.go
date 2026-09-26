package snmpcred

import (
	"log/slog"
	"unicode/utf8"
)

// Input is the set/replace request body (contracts/ipam-http.md). Every
// field of the chosen kind must be sent; nothing is ever pre-filled (FR-006).
type Input struct {
	Version       int    `json:"version"`
	Community     string `json:"community,omitempty"`
	User          string `json:"user,omitempty"`
	SecurityLevel string `json:"security_level,omitempty"`
	AuthProtocol  string `json:"auth_protocol,omitempty"`
	AuthPassword  string `json:"auth_password,omitempty"`
	PrivProtocol  string `json:"priv_protocol,omitempty"`
	PrivPassword  string `json:"priv_password,omitempty"`
}

// String never prints a value.
func (Input) String() string { return "snmpcred.Input" + redacted }

// GoString never prints a value.
func (Input) GoString() string { return "snmpcred.Input" + redacted }

// LogValue never logs a value.
func (Input) LogValue() slog.Value { return slog.StringValue(redacted) }

// FieldError names the rejected field; it never carries the value.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return "snmpcred: " + e.Field + ": " + e.Msg }

func fieldErr(field, msg string) error { return &FieldError{Field: field, Msg: msg} }

// Validate checks completeness per kind (FR-003): v2c needs a community and
// nothing else; v3 needs user, level, auth protocol and password (≥ 8), and
// for authPriv a privacy protocol and password (≥ 8); authNoPriv carries no
// privacy fields. No value is empty or longer than 256 characters.
func (in Input) Validate() error {
	switch in.Version {
	case 2:
		if err := value("community", in.Community, 1); err != nil {
			return err
		}
		for _, f := range []struct{ name, v string }{
			{"user", in.User}, {"security_level", in.SecurityLevel}, {"auth_protocol", in.AuthProtocol},
			{"auth_password", in.AuthPassword}, {"priv_protocol", in.PrivProtocol}, {"priv_password", in.PrivPassword},
		} {
			if f.v != "" {
				return fieldErr(f.name, "not allowed for SNMP v2c")
			}
		}
		return nil
	case 3:
		return in.validateV3()
	default:
		return fieldErr("version", "must be 2 or 3")
	}
}

func (in Input) validateV3() error {
	if in.Community != "" {
		return fieldErr("community", "not allowed for SNMP v3")
	}
	if err := value("user", in.User, 1); err != nil {
		return err
	}
	if in.SecurityLevel != LevelAuthNoPriv && in.SecurityLevel != LevelAuthPriv {
		return fieldErr("security_level", "must be authNoPriv or authPriv")
	}
	if !validAuth(in.AuthProtocol) {
		return fieldErr("auth_protocol", "unsupported protocol")
	}
	if err := value("auth_password", in.AuthPassword, MinPasswordLen); err != nil {
		return err
	}
	if in.SecurityLevel == LevelAuthNoPriv {
		if in.PrivProtocol != "" {
			return fieldErr("priv_protocol", "not allowed for authNoPriv")
		}
		if in.PrivPassword != "" {
			return fieldErr("priv_password", "not allowed for authNoPriv")
		}
		return nil
	}
	if !validPriv(in.PrivProtocol) {
		return fieldErr("priv_protocol", "unsupported protocol")
	}
	return value("priv_password", in.PrivPassword, MinPasswordLen)
}

// value checks a secret value's length in characters.
func value(field, v string, minLen int) error {
	n := utf8.RuneCountInString(v)
	switch {
	case n == 0:
		return fieldErr(field, "required")
	case n < minLen:
		return fieldErr(field, "too short")
	case n > MaxValueLen:
		return fieldErr(field, "too long")
	}
	return nil
}

// Meta returns the non-secret part of a validated input.
func (in Input) Meta() Meta {
	if in.Version != 3 {
		return Meta{Version: in.Version}
	}
	return Meta{Version: 3, SecurityLevel: in.SecurityLevel, AuthProtocol: in.AuthProtocol, PrivProtocol: in.PrivProtocol}
}

// secretOf extracts the values to seal.
func secretOf(in Input) Secret {
	return Secret{Community: in.Community, User: in.User, AuthPassword: in.AuthPassword, PrivPassword: in.PrivPassword}
}
