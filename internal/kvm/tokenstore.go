package kvm

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// tokenTTL bounds how long a minted KVM session token is valid. Long enough to
// open the console and let the BMC's session keep itself alive; short enough to
// limit exposure of the unauthenticated /bmc proxy path.
const tokenTTL = 20 * time.Minute

// kvmToken binds an opaque token to a specific device's BMC target. It carries
// the resolved credentials so the proxy never re-hits Warden per asset request.
type kvmToken struct {
	deviceID string
	target   bmcTarget
	expires  time.Time
}

// tokenStore holds short-lived KVM session tokens. The authenticated RPC mints
// them (after a platform-admin check); the unauthenticated /bmc proxy path
// validates them. Safe for concurrent use.
type tokenStore struct {
	now func() time.Time

	mu     sync.Mutex
	tokens map[string]kvmToken
}

func newTokenStore() *tokenStore {
	return &tokenStore{now: time.Now, tokens: make(map[string]kvmToken)}
}

// mint creates a new token bound to the device + BMC target and returns it.
func (s *tokenStore) mint(deviceID string, target bmcTarget) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[tok] = kvmToken{deviceID: deviceID, target: target, expires: s.now().Add(tokenTTL)}
	s.gcLocked()
	return tok, nil
}

// resolve returns the token's binding if it is valid and unexpired.
func (s *tokenStore) resolve(tok string) (kvmToken, bool) {
	if tok == "" {
		return kvmToken{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kt, ok := s.tokens[tok]
	if !ok || !s.now().Before(kt.expires) {
		return kvmToken{}, false
	}
	return kt, true
}

// gcLocked drops expired tokens. Caller must hold the lock.
func (s *tokenStore) gcLocked() {
	now := s.now()
	for k, v := range s.tokens {
		if !now.Before(v.expires) {
			delete(s.tokens, k)
		}
	}
}
