// Package ipmi is the out-of-band BMC/IPMI client for the IPAM service. It wraps
// github.com/bougou/go-ipmi with a small, stable surface — device info, power
// status + control, sensors and the system event log — behind the BMC interface
// so the module's power/console authorization is unit-tested against a Fake
// without any IPMI LAN traffic.
//
// Every method takes the BMC host and the credentials at call time: the IPAM
// module stores only a warden reference on the device and the caller fetches the
// real credentials from warden immediately before the call. Credentials are used
// to open the IPMI LAN session (UDP 623) and are never persisted or logged.
package ipmi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goipmi "github.com/bougou/go-ipmi"
)

// defaultTimeout bounds a single connect/command exchange.
const defaultTimeout = 15 * time.Second

// Power action verbs (mirror store.Power*).
const (
	ActionOn    = "on"
	ActionOff   = "off"
	ActionCycle = "cycle"
	ActionReset = "reset"
	ActionSoft  = "soft"
	ActionDiag  = "diag"
)

// ErrUnknownAction is returned when Power is given an unrecognized verb.
var ErrUnknownAction = errors.New("ipmi: unknown power action")

// Creds are the BMC login credentials. Protocol is "auto" (default), "1.5" or
// "2.0". These come from warden at use time and must never be persisted/logged.
type Creds struct {
	Username string
	Password string
	Protocol string
	Port     int // 0 -> 623
}

// PowerState is a decoded chassis power/identify snapshot.
type PowerState struct {
	On                 bool   `json:"on"`
	PowerRestorePolicy string `json:"power_restore_policy"`
	PowerFault         bool   `json:"power_fault"`
	PowerOverload      bool   `json:"power_overload"`
	Intrusion          bool   `json:"intrusion"`
	CoolingFault       bool   `json:"cooling_fault"`
	DriveFault         bool   `json:"drive_fault"`
	IdentifyActive     bool   `json:"identify_active"`
}

// SensorReading is one decoded sensor value.
type SensorReading struct {
	Number  uint8   `json:"number"`
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Reading string  `json:"reading"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Status  string  `json:"status"`
	Valid   bool    `json:"valid"`
}

// SELEntry is one decoded System Event Log record.
type SELEntry struct {
	RecordID    uint16 `json:"record_id"`
	RecordType  string `json:"record_type"`
	Timestamp   string `json:"timestamp,omitempty"`
	SensorType  string `json:"sensor_type,omitempty"`
	SensorName  string `json:"sensor_name,omitempty"`
	Description string `json:"description"`
	Severity    string `json:"severity,omitempty"`
}

// DeviceInfo summarizes BMC identity (Get Device ID + FRU details).
type DeviceInfo struct {
	Manufacturer    string `json:"manufacturer,omitempty"`
	Product         string `json:"product,omitempty"`
	SerialNumber    string `json:"serial_number,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	IPMIVersion     string `json:"ipmi_version,omitempty"`
	DeviceID        uint8  `json:"device_id"`
}

// BMC is the out-of-band management surface. Each call opens, uses and closes an
// IPMI LAN session to host with the supplied credentials.
type BMC interface {
	Info(ctx context.Context, host string, creds Creds) (DeviceInfo, error)
	PowerStatus(ctx context.Context, host string, creds Creds) (PowerState, error)
	Sensors(ctx context.Context, host string, creds Creds) ([]SensorReading, error)
	SEL(ctx context.Context, host string, creds Creds) ([]SELEntry, error)
	Power(ctx context.Context, host string, creds Creds, action string) error
}

// Client is the real go-ipmi-backed BMC.
type Client struct {
	timeout time.Duration
}

// NewClient builds a real BMC client. A non-positive timeout uses the default.
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{timeout: timeout}
}

// connect opens an IPMI LAN session to host. The caller must close the client.
func (c *Client) connect(ctx context.Context, host string, creds Creds) (*goipmi.Client, error) {
	if host == "" {
		return nil, fmt.Errorf("ipmi: host is required")
	}
	port := creds.Port
	if port == 0 {
		port = 623
	}
	ic, err := goipmi.NewClient(host, port, creds.Username, creds.Password)
	if err != nil {
		return nil, fmt.Errorf("ipmi: new client %s: %w", host, err)
	}
	ic.WithTimeout(c.timeout)
	switch creds.Protocol {
	case "1.5":
		err = ic.Connect15(ctx)
	case "2.0":
		err = ic.Connect20(ctx)
	default:
		err = ic.ConnectAuto(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("ipmi: connect %s: %w", host, err)
	}
	return ic, nil
}

// Info reads BMC identity and best-effort FRU product data.
func (c *Client) Info(ctx context.Context, host string, creds Creds) (DeviceInfo, error) {
	ic, err := c.connect(ctx, host, creds)
	if err != nil {
		return DeviceInfo{}, err
	}
	defer func() { _ = ic.Close(ctx) }()

	dev, err := ic.GetDeviceID(ctx)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("ipmi: get device id: %w", err)
	}
	info := DeviceInfo{
		DeviceID:        dev.DeviceID,
		FirmwareVersion: fmt.Sprintf("%d.%02x", dev.MajorFirmwareRevision, dev.MinorFirmwareRevision),
		IPMIVersion:     fmt.Sprintf("%d.%d", dev.MajorIPMIVersion, dev.MinorIPMIVersion),
	}
	if fru, ferr := ic.GetFRU(ctx, 0, "Builtin FRU"); ferr == nil && fru != nil {
		if p := fru.ProductInfoArea; p != nil {
			info.Manufacturer = strings.TrimSpace(string(p.Manufacturer))
			info.Product = strings.TrimSpace(string(p.Name))
			info.SerialNumber = strings.TrimSpace(string(p.SerialNumber))
		}
		if info.Product == "" && fru.BoardInfoArea != nil {
			info.Manufacturer = strings.TrimSpace(string(fru.BoardInfoArea.Manufacturer))
			info.Product = strings.TrimSpace(string(fru.BoardInfoArea.ProductName))
			info.SerialNumber = strings.TrimSpace(string(fru.BoardInfoArea.SerialNumber))
		}
	}
	return info, nil
}

// PowerStatus reads and decodes the current chassis status.
func (c *Client) PowerStatus(ctx context.Context, host string, creds Creds) (PowerState, error) {
	ic, err := c.connect(ctx, host, creds)
	if err != nil {
		return PowerState{}, err
	}
	defer func() { _ = ic.Close(ctx) }()

	resp, err := ic.GetChassisStatus(ctx)
	if err != nil {
		return PowerState{}, fmt.Errorf("ipmi: get chassis status: %w", err)
	}
	return PowerState{
		On:                 resp.PowerIsOn,
		PowerRestorePolicy: resp.PowerRestorePolicy.String(),
		PowerFault:         resp.PowerFault,
		PowerOverload:      resp.PowerOverload,
		Intrusion:          resp.ChassisIntrusionActive,
		CoolingFault:       resp.CollingFanFault,
		DriveFault:         resp.DriveFault,
		IdentifyActive:     resp.ChassisIdentifyState != goipmi.ChassisIdentifyStateOff,
	}, nil
}

// Sensors reads all sensors and decodes them into serializable readings.
func (c *Client) Sensors(ctx context.Context, host string, creds Creds) ([]SensorReading, error) {
	ic, err := c.connect(ctx, host, creds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ic.Close(ctx) }()

	sensors, err := ic.GetSensors(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipmi: get sensors: %w", err)
	}
	out := make([]SensorReading, 0, len(sensors))
	for _, s := range sensors {
		out = append(out, SensorReading{
			Number:  s.Number,
			Name:    s.Name,
			Type:    s.SensorType.String(),
			Reading: s.ReadingStr(),
			Value:   s.Value,
			Unit:    s.SensorUnit.String(),
			Status:  s.Status(),
			Valid:   s.IsReadingValid(),
		})
	}
	return out, nil
}

// SEL reads the System Event Log, tolerating firmware that reports an empty log
// with the "record not present" completion code.
func (c *Client) SEL(ctx context.Context, host string, creds Creds) ([]SELEntry, error) {
	ic, err := c.connect(ctx, host, creds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ic.Close(ctx) }()

	info, err := ic.GetSELInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipmi: get sel info: %w", err)
	}
	out := make([]SELEntry, 0, info.Entries)
	if info.Entries == 0 {
		return out, nil
	}
	var recordID uint16 = 0x0000
	for {
		resp, err := ic.GetSELEntry(ctx, 0, recordID)
		if err != nil {
			if isRecordNotPresent(err) {
				break
			}
			return nil, fmt.Errorf("ipmi: get sel entry %#04x: %w", recordID, err)
		}
		sel, err := goipmi.ParseSEL(resp.Data)
		if err != nil {
			return nil, fmt.Errorf("ipmi: parse sel %#04x: %w", recordID, err)
		}
		out = append(out, decodeSEL(sel))
		recordID = resp.NextRecordID
		if recordID == 0xffff {
			break
		}
	}
	return out, nil
}

// Power applies a chassis control action (on|off|cycle|reset|soft|diag).
func (c *Client) Power(ctx context.Context, host string, creds Creds, action string) error {
	control, ok := controlFor(action)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	ic, err := c.connect(ctx, host, creds)
	if err != nil {
		return err
	}
	defer func() { _ = ic.Close(ctx) }()

	if _, err := ic.ChassisControl(ctx, control); err != nil {
		return fmt.Errorf("ipmi: chassis control %s: %w", action, err)
	}
	return nil
}

// controlFor maps an action verb to a go-ipmi ChassisControl value.
func controlFor(action string) (goipmi.ChassisControl, bool) {
	switch action {
	case ActionOn:
		return goipmi.ChassisControlPowerUp, true
	case ActionOff:
		return goipmi.ChassisControlPowerDown, true
	case ActionCycle:
		return goipmi.ChassisControlPowerCycle, true
	case ActionReset:
		return goipmi.ChassisControlHardReset, true
	case ActionSoft:
		return goipmi.ChassisControlSoftShutdown, true
	case ActionDiag:
		return goipmi.ChassisControlDiagnosticInterrupt, true
	default:
		return 0, false
	}
}

func isRecordNotPresent(err error) bool {
	var respErr *goipmi.ResponseError
	if errors.As(err, &respErr) {
		return respErr.CompletionCode() == goipmi.CompletionCodeRequestedDataNotPresent
	}
	return false
}

func decodeSEL(e *goipmi.SEL) SELEntry {
	entry := SELEntry{RecordID: e.RecordID, RecordType: e.RecordType.String()}
	switch {
	case e.Standard != nil:
		s := e.Standard
		entry.Timestamp = s.Timestamp.Format("2006-01-02 15:04:05")
		entry.SensorType = s.SensorType.String()
		entry.SensorName = fmt.Sprintf("#%d", s.SensorNumber)
		entry.Description = s.EventString()
		entry.Severity = string(s.EventSeverity())
	case e.OEMTimestamped != nil:
		entry.Timestamp = e.OEMTimestamped.Timestamp.Format("2006-01-02 15:04:05")
		entry.Description = fmt.Sprintf("OEM event (mfg 0x%06x)", e.OEMTimestamped.ManufacturerID)
	case e.OEMNonTimestamped != nil:
		entry.Description = "OEM non-timestamped event"
	}
	return entry
}

// Fake is an in-memory BMC for tests. It records power actions and returns
// canned readings; it never opens an IPMI session.
type Fake struct {
	State    PowerState
	InfoData DeviceInfo
	Readings []SensorReading
	SELData  []SELEntry
	Err      error

	// Actions records every Power verb applied, in order.
	Actions []string
	// LastHost / LastCreds capture the last call's inputs for assertions.
	LastHost  string
	LastCreds Creds
}

// NewFake builds a Fake reporting a powered-on chassis.
func NewFake() *Fake { return &Fake{State: PowerState{On: true}} }

func (f *Fake) Info(_ context.Context, host string, creds Creds) (DeviceInfo, error) {
	f.LastHost, f.LastCreds = host, creds
	if f.Err != nil {
		return DeviceInfo{}, f.Err
	}
	return f.InfoData, nil
}

func (f *Fake) PowerStatus(_ context.Context, host string, creds Creds) (PowerState, error) {
	f.LastHost, f.LastCreds = host, creds
	if f.Err != nil {
		return PowerState{}, f.Err
	}
	return f.State, nil
}

func (f *Fake) Sensors(_ context.Context, host string, creds Creds) ([]SensorReading, error) {
	f.LastHost, f.LastCreds = host, creds
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Readings, nil
}

func (f *Fake) SEL(_ context.Context, host string, creds Creds) ([]SELEntry, error) {
	f.LastHost, f.LastCreds = host, creds
	if f.Err != nil {
		return nil, f.Err
	}
	return f.SELData, nil
}

func (f *Fake) Power(_ context.Context, host string, creds Creds, action string) error {
	f.LastHost, f.LastCreds = host, creds
	if f.Err != nil {
		return f.Err
	}
	if _, ok := controlFor(action); !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	f.Actions = append(f.Actions, action)
	switch action {
	case ActionOn:
		f.State.On = true
	case ActionOff, ActionSoft:
		f.State.On = false
	}
	return nil
}

// interface conformance.
var (
	_ BMC = (*Client)(nil)
	_ BMC = (*Fake)(nil)
)
