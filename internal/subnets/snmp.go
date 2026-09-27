package subnets

import (
	"context"
	"errors"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// SNMPStatus is the write-only credential status of a subnet
// (contracts/ipam-http.md): its own metadata and the effective state. It
// never carries a credential value.
type SNMPStatus struct {
	Own       *SNMPOwn          `json:"own"`
	Effective store.SNMPSummary `json:"effective"`
}

// SNMPOwn is the metadata of a subnet's own credentials.
type SNMPOwn struct {
	Version       int       `json:"version"`
	SecurityLevel string    `json:"security_level,omitempty"`
	AuthProtocol  string    `json:"auth_protocol,omitempty"`
	PrivProtocol  string    `json:"priv_protocol,omitempty"`
	Weak          bool      `json:"weak"`
	UpdatedBy     string    `json:"updated_by,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SetEnvelope installs the module envelope that seals SNMP credentials.
func (s *Service) SetEnvelope(env snmpcred.Sealer) { s.env = env }

// SNMPIndex loads one tenant's subnets and credential metadata into the
// shared inheritance index (no blob is read).
func SNMPIndex(ctx context.Context, st repo.Store, tenantID string) (*snmpcred.Index, error) {
	subs, err := st.AllSubnetCIDRs(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := st.ListSubnetSNMP(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return snmpcred.NewIndex(subs, rows), nil
}

// GetSNMP returns the subnet's credential status (FR-004, FR-012).
func (s *Service) GetSNMP(ctx context.Context, subj authz.Subjects, subnetID string) (SNMPStatus, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return SNMPStatus{}, err
	}
	if _, err := s.st.GetSubnet(ctx, subj.TenantID, subnetID); err != nil {
		return SNMPStatus{}, mapErr(err)
	}
	idx, err := SNMPIndex(ctx, s.st, subj.TenantID)
	if err != nil {
		return SNMPStatus{}, err
	}
	eff, _, _ := idx.Effective(subnetID)
	out := SNMPStatus{Effective: eff}
	if r, ok := idx.Own(subnetID); ok {
		out.Own = &SNMPOwn{Version: r.Version, SecurityLevel: r.SecurityLevel, AuthProtocol: r.AuthProtocol,
			PrivProtocol: r.PrivProtocol, Weak: snmpcred.MetaOf(r).Weak(), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// SetSNMP validates, seals and stores the subnet's own credentials (set or
// replace, every field required: FR-003, FR-006) with its audit row.
func (s *Service) SetSNMP(ctx context.Context, subj authz.Subjects, subnetID string, in snmpcred.Input) (SNMPStatus, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return SNMPStatus{}, err
	}
	if err := in.Validate(); err != nil {
		return SNMPStatus{}, err
	}
	tid := subj.TenantID
	if _, err := s.st.GetSubnet(ctx, tid, subnetID); err != nil {
		return SNMPStatus{}, mapErr(err)
	}
	prev, err := s.st.GetSubnetSNMP(ctx, tid, subnetID)
	replacing := err == nil
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return SNMPStatus{}, err
	}
	blob, err := snmpcred.Seal(s.env, tid, subnetID, in)
	if err != nil {
		return SNMPStatus{}, err
	}
	meta := in.Meta()
	event, detail := audit.SNMPCredentialsSet, map[string]any{"protocol_version": meta.Version, "security_level": meta.SecurityLevel}
	if replacing {
		event, detail["previous_version"] = audit.SNMPCredentialsReplaced, prev.Version
	}
	row := store.SubnetSNMP{TenantID: tid, SubnetID: subnetID, Version: meta.Version, SecurityLevel: meta.SecurityLevel,
		AuthProtocol: meta.AuthProtocol, PrivProtocol: meta.PrivProtocol, Sealed: blob, UpdatedBy: subj.ActorID()}
	if err := s.st.PutSubnetSNMP(ctx, row, s.snmpAudit(subj, event, subnetID, detail)); err != nil {
		return SNMPStatus{}, mapErr(err)
	}
	return s.GetSNMP(ctx, subj, subnetID)
}

// ClearSNMP deletes the subnet's own credentials permanently (FR-007) with
// its audit row; the subnet then inherits again or has none. Clearing a
// subnet without own credentials is a no-op.
func (s *Service) ClearSNMP(ctx context.Context, subj authz.Subjects, subnetID string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	tid := subj.TenantID
	if _, err := s.st.GetSubnet(ctx, tid, subnetID); err != nil {
		return mapErr(err)
	}
	prev, err := s.st.GetSubnetSNMP(ctx, tid, subnetID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	row := s.snmpAudit(subj, audit.SNMPCredentialsCleared, subnetID, map[string]any{"previous_version": prev.Version})
	if err := s.st.DeleteSubnetSNMP(ctx, tid, subnetID, row); err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	return nil
}

// snmpAudit builds a user audit row for a credential change.
func (s *Service) snmpAudit(subj authz.Subjects, t audit.EventType, subnetID string, detail map[string]any) store.AuditRow {
	row, _ := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorUser, ActorID: subj.ActorID(),
		SubjectKind: audit.SubjectSubnet, SubjectID: subnetID, Outcome: audit.OutcomeOK, Details: detail}, s.now())
	return row
}

// withSNMP fills the effective summary of each subnet and replaces the
// legacy fields: snmp_version carries the effective version and
// snmp_secret_ref is never returned.
func (s *Service) withSNMP(ctx context.Context, tenantID string, subs ...*store.Subnet) error {
	idx, err := SNMPIndex(ctx, s.st, tenantID)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		eff, _, _ := idx.Effective(sub.ID)
		sub.SNMP = &eff
		sub.SNMPVersion = eff.Version
		sub.SNMPSecretRef = ""
	}
	return nil
}

// dropLegacySNMP ignores client-sent SNMP fields: credentials live in their
// own table and the legacy warden reference is retired (FR-005, FR-023).
func dropLegacySNMP(in *store.Subnet) {
	in.SNMPSecretRef, in.SNMPVersion, in.SNMP = "", 0, nil
}
