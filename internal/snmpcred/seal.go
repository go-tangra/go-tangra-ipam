package snmpcred

import (
	"encoding/json"
	"errors"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
)

// Errors.
var (
	// ErrUnreadable: the stored blob cannot be opened (wrong or rotated KEK,
	// blob moved to another subnet/tenant, tampering).
	ErrUnreadable = errors.New("snmpcred: credentials unreadable")
	// ErrNoEnvelope: no module envelope is configured.
	ErrNoEnvelope = errors.New("snmpcred: no envelope configured")
)

// Sealer is the module envelope (*sealed.Envelope).
type Sealer interface {
	Seal(plaintext, ad []byte) ([]byte, error)
	Open(blob, ad []byte) ([]byte, error)
}

// Seal encrypts the secret values of in, bound to tenant and subnet.
func Seal(env Sealer, tenantID, subnetID string, in Input) ([]byte, error) {
	if env == nil {
		return nil, ErrNoEnvelope
	}
	doc, _ := json.Marshal(secretOf(in)) // a struct of strings always marshals
	return env.Seal(doc, sealed.ADSNMP(tenantID, subnetID))
}

// Open decrypts a blob sealed for tenant and subnet. Any failure is
// ErrUnreadable and carries no detail.
func Open(env Sealer, tenantID, subnetID string, blob []byte) (Secret, error) {
	if env == nil {
		return Secret{}, ErrUnreadable
	}
	doc, err := env.Open(blob, sealed.ADSNMP(tenantID, subnetID))
	if err != nil {
		return Secret{}, ErrUnreadable
	}
	var s Secret
	if err := json.Unmarshal(doc, &s); err != nil {
		return Secret{}, ErrUnreadable
	}
	return s, nil
}
