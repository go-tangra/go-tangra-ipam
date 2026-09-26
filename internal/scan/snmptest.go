package scan

import (
	"context"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Credentials test outcomes beyond the SNMP client's (contracts/ipam-http.md).
const (
	OutcomeNoCredentials = "no_credentials"
	OutcomeUnreadable    = "credentials_unreadable"
)

// testMargin is added to the SNMP timeout to bound a credentials test (FR-020).
const testMargin = 2 * time.Second

// maxSysText bounds sysName/sysDescr in a test result.
const maxSysText = 256

// TestResult is the answer of one credentials test; it never carries a
// credential value.
type TestResult struct {
	Outcome        string `json:"outcome"`
	SysName        string `json:"sys_name,omitempty"`
	SysDescr       string `json:"sys_descr,omitempty"`
	SourceSubnetID string `json:"source_subnet_id,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
}

// TestCredentials probes one usable address inside the subnet with the
// subnet's effective SNMP credentials (FR-019) and audits the test (FR-021).
// The target must be a usable host of the subnet (SR-003); the probe is
// bounded by the SNMP timeout plus 2 seconds (FR-020).
func (s *Service) TestCredentials(ctx context.Context, subj authz.Subjects, subnetID, address string) (TestResult, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return TestResult{}, err
	}
	tid := subj.TenantID
	subnet, err := s.st.GetSubnet(ctx, tid, subnetID)
	if err != nil {
		return TestResult{}, err
	}
	target, err := usableTarget(subnet.CIDR, address)
	if err != nil {
		return TestResult{}, err
	}
	cred, err := s.effectiveCreds(ctx, tid, subnetID)
	if err != nil {
		return TestResult{}, err
	}
	start := s.now()
	res := TestResult{SourceSubnetID: cred.source}
	switch cred.status {
	case store.SNMPNoCredentials:
		res.Outcome = OutcomeNoCredentials
	case store.SNMPUnreadable:
		res.Outcome = OutcomeUnreadable
	default:
		pctx, cancel := context.WithTimeout(ctx, s.timeout()+testMargin)
		name, descr, perr := s.snmp.Probe(pctx, target, cred.creds)
		cancel()
		res.Outcome = string(snmp.Classify(perr))
		if perr == nil {
			res.SysName, res.SysDescr = clip(name), clip(descr)
		}
	}
	res.DurationMs = s.now().Sub(start).Milliseconds()
	s.auditTest(ctx, subj, subnetID, target, res)
	return res, nil
}

// usableTarget accepts only a literal IP that is a usable host of cidr (not
// the network or broadcast address) and returns its canonical form.
func usableTarget(cidr, address string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil {
		return "", &snmpcred.FieldError{Field: "address", Msg: "must be an IP address"}
	}
	if ok, err := ipnet.GatewayInRange(cidr, ip.String()); err != nil || !ok {
		return "", &snmpcred.FieldError{Field: "address", Msg: "must be a usable address of the subnet"}
	}
	return ip.String(), nil
}

// auditTest records the test with neutral detail keys (never a credential).
func (s *Service) auditTest(ctx context.Context, subj authz.Subjects, subnetID, target string, res TestResult) {
	outcome := audit.OutcomeOK
	if res.Outcome != string(snmp.OutcomeOK) {
		outcome = audit.OutcomeError
	}
	row, _ := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: audit.SNMPCredentialsTested, ActorKind: audit.ActorUser,
		ActorID: subj.ActorID(), SubjectKind: audit.SubjectSubnet, SubjectID: subnetID, Target: target, Outcome: outcome,
		Details: map[string]any{"target": target, "outcome": res.Outcome, "source_subnet_id": res.SourceSubnetID}}, s.now())
	_ = s.st.AppendAudit(ctx, row)
}

// clip bounds a device-reported string to maxSysText characters.
func clip(v string) string {
	if utf8.RuneCountInString(v) <= maxSysText {
		return v
	}
	return string([]rune(v)[:maxSysText])
}
