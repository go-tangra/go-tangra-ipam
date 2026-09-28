// Package bmc decides whether and how IPAM may contact a device's BMC
// (feature 024). It owns the device's warden BMC reference (validated against
// warden for the acting user and audited in the same transaction), the BMC
// address (management IP, else the address the inventory agent reported on
// the device's bmc interface), the per-viewer status, and the closed set of
// failure reasons the HTTP and gRPC surfaces translate.
//
// Credentials are fetched from warden for the signed-in user (the caller puts
// the user's platform token into the context with warden.WithUserToken) at
// the moment of each action and are never stored, cached or logged here.
package bmc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// Errors of the decision flow (the warden and ipmi sentinels complete the set).
var (
	ErrNotConfigured    = errors.New("bmc: no warden reference configured")
	ErrNoAddress        = errors.New("bmc: no BMC address")
	ErrInvalidReference = errors.New("bmc: reference must be a warden secret id")
)

// Access states of a configured reference for the viewing user.
const (
	AccessOK          = "ok"
	AccessForbidden   = "forbidden"
	AccessNotFound    = "not_found"
	AccessUnavailable = "unavailable"
)

// Address sources.
const (
	SourceManagementIP = "management_ip"
	SourceReported     = "reported"
)

// Store is the persistence the package needs (repo.Store satisfies it).
type Store interface {
	GetDevice(ctx context.Context, tenantID, id string) (store.Device, error)
	AddressesForDevice(ctx context.Context, tenantID, deviceID string) ([]store.IPAddress, error)
	SetDeviceBMCRef(ctx context.Context, tenantID, deviceID, ref string, audit store.AuditRow) (string, error)
	AppendAudit(ctx context.Context, row store.AuditRow) error
}

// Input is the body of PUT /devices/{id}/bmc.
type Input struct {
	Reference string `json:"reference"`
}

// SecretInfo is the warden metadata shown for a reference (never material).
type SecretInfo struct {
	Name       string `json:"name"`
	Username   string `json:"username,omitempty"`
	FolderPath string `json:"folder_path,omitempty"`
}

// Status is a device's BMC state as seen by the viewing user.
type Status struct {
	Configured    bool        `json:"configured"`
	Reference     string      `json:"reference,omitempty"`
	Access        string      `json:"access,omitempty"`
	Secret        *SecretInfo `json:"secret,omitempty"`
	Address       string      `json:"address,omitempty"`
	AddressSource string      `json:"address_source,omitempty"`
	Ready         bool        `json:"ready"`
	Reason        string      `json:"reason,omitempty"`
}

// Service implements the BMC reference and access decisions.
type Service struct {
	st  Store
	w   warden.Client
	now func() time.Time
}

// New builds the service.
func New(st Store, w warden.Client) *Service {
	return &Service{st: st, w: w, now: func() time.Time { return time.Now().UTC() }}
}

// ValidReference reports whether ref is a warden secret id.
func ValidReference(ref string) bool { return warden.ValidRef(ref) }

// CanonicalReference is the stored form of a valid reference.
func CanonicalReference(ref string) string { return strings.ToLower(ref) }

func access(err error) string {
	switch {
	case err == nil:
		return AccessOK
	case errors.Is(err, warden.ErrForbidden):
		return AccessForbidden
	case errors.Is(err, warden.ErrNotFound), errors.Is(err, warden.ErrEmptyRef):
		return AccessNotFound
	}
	return AccessUnavailable
}

func (s *Service) load(ctx context.Context, subj authz.Subjects, deviceID string) (store.Device, string, string, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Device{}, "", "", err
	}
	dev, err := s.st.GetDevice(ctx, subj.TenantID, deviceID)
	if err != nil {
		return store.Device{}, "", "", err
	}
	addrs, err := s.st.AddressesForDevice(ctx, subj.TenantID, deviceID)
	if err != nil {
		return store.Device{}, "", "", err
	}
	addr, src := Address(dev, addrs)
	return dev, addr, src, nil
}

// status assembles a Status from the loaded parts; metaErr is the result of
// the warden metadata read (ignored when no reference is set).
func status(ref, addr, src string, meta warden.SecretMeta, metaErr error) Status {
	st := Status{Configured: ref != "", Reference: ref, Address: addr, AddressSource: src}
	if st.Configured {
		st.Access = access(metaErr)
		if metaErr == nil {
			st.Secret = &SecretInfo{Name: meta.Name, Username: meta.Username, FolderPath: meta.FolderPath}
		}
	}
	switch {
	case !st.Configured:
		st.Reason = ReasonNotConfigured
	case addr == "":
		st.Reason = ReasonNoAddress
	case metaErr != nil:
		st.Reason = Reason(metaErr)
	default:
		st.Ready = true
	}
	return st
}

// Status returns the device's BMC state for the viewer (warden metadata is
// read as the viewer; a refusal shows as access "forbidden").
func (s *Service) Status(ctx context.Context, subj authz.Subjects, deviceID string) (Status, error) {
	dev, addr, src, err := s.load(ctx, subj, deviceID)
	if err != nil {
		return Status{}, err
	}
	var meta warden.SecretMeta
	var metaErr error
	if dev.IPMISecretRef != "" {
		meta, metaErr = s.w.Meta(ctx, dev.IPMISecretRef)
	}
	return status(dev.IPMISecretRef, addr, src, meta, metaErr), nil
}

// Set points the device at a warden secret after warden confirmed that the
// acting user may read it; the change is audited in the same transaction.
// Setting the current reference again writes nothing.
func (s *Service) Set(ctx context.Context, subj authz.Subjects, deviceID, ref string) (Status, error) {
	if !ValidReference(ref) {
		return Status{}, ErrInvalidReference
	}
	ref = CanonicalReference(ref)
	dev, addr, src, err := s.load(ctx, subj, deviceID)
	if err != nil {
		return Status{}, err
	}
	meta, err := s.w.Meta(ctx, ref)
	if err != nil {
		return Status{}, err
	}
	if !strings.EqualFold(dev.IPMISecretRef, ref) {
		et, details := audit.BMCReferenceSet, map[string]any{"reference": ref, "reference_name": meta.Name}
		if dev.IPMISecretRef != "" {
			et = audit.BMCReferenceChanged
			details["previous_reference"] = dev.IPMISecretRef
		}
		if _, err := s.st.SetDeviceBMCRef(ctx, subj.TenantID, deviceID, ref, s.row(subj, et, deviceID, details)); err != nil {
			return Status{}, err
		}
	}
	return status(ref, addr, src, meta, nil), nil
}

// Clear removes the device's reference (audited; a no-op when none is set).
func (s *Service) Clear(ctx context.Context, subj authz.Subjects, deviceID string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	dev, err := s.st.GetDevice(ctx, subj.TenantID, deviceID)
	if err != nil {
		return err
	}
	if dev.IPMISecretRef == "" {
		return nil
	}
	_, err = s.st.SetDeviceBMCRef(ctx, subj.TenantID, deviceID, "",
		s.row(subj, audit.BMCReferenceCleared, deviceID, map[string]any{"previous_reference": dev.IPMISecretRef}))
	return err
}

func actorKind(subj authz.Subjects) string {
	switch subj.ActorKind {
	case audit.ActorService, audit.ActorSystem:
		return subj.ActorKind
	}
	return audit.ActorUser
}

func (s *Service) row(subj authz.Subjects, et audit.EventType, deviceID string, details map[string]any) store.AuditRow {
	r, _ := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: et, ActorKind: actorKind(subj), ActorID: subj.ActorID(),
		SubjectKind: audit.SubjectDevice, SubjectID: deviceID, Outcome: audit.OutcomeOK, Details: details}, s.now())
	return r
}

// Target is everything needed to contact one BMC for one action. Creds is
// redacted in every textual form and must be dropped after the action.
type Target struct {
	Device  store.Device
	Address string
	Creds   warden.Credentials
}

// IPMI returns the IPMI-over-LAN credentials.
func (t Target) IPMI() ipmi.Creds { return IPMICreds(t.Creds) }

// KVM returns the BMC web-console credentials.
func (t Target) KVM() kvm.Creds {
	return kvm.Creds{Username: t.Creds.Username, Password: t.Creds.Password}
}

// Resolve decides whether the caller may contact the device's BMC now and,
// if so, returns its address and the credentials warden released for the
// caller. Checks run in order: platform-admin, device, reference, address,
// warden — the BMC is never contacted unless all pass.
func (s *Service) Resolve(ctx context.Context, subj authz.Subjects, deviceID string) (Target, error) {
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		return Target{}, err
	}
	dev, addr, _, err := s.load(ctx, subj, deviceID)
	if err != nil {
		return Target{}, err
	}
	if dev.IPMISecretRef == "" {
		return Target{Device: dev, Address: addr}, ErrNotConfigured
	}
	if addr == "" {
		return Target{Device: dev}, ErrNoAddress
	}
	creds, err := s.w.Credentials(ctx, dev.IPMISecretRef)
	if err != nil {
		return Target{Device: dev, Address: addr}, err
	}
	return Target{Device: dev, Address: addr, Creds: creds}, nil
}

// AuditAction records a power action or KVM session with its outcome (the
// reason is empty on success). Recording never fails the caller.
func (s *Service) AuditAction(ctx context.Context, subj authz.Subjects, et audit.EventType, deviceID, target, action, reason string) {
	kind := audit.SubjectPower
	if et == audit.KVMSessionStarted {
		kind = audit.SubjectKVM
	}
	details := map[string]any{}
	if action != "" {
		details["action"] = action
	}
	row, err := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: et, ActorKind: actorKind(subj), ActorID: subj.ActorID(),
		SubjectKind: kind, SubjectID: deviceID, Target: target, Outcome: Outcome(reason), Reason: reason, Details: details}, s.now())
	if err != nil {
		return
	}
	_ = s.st.AppendAudit(ctx, row)
}
