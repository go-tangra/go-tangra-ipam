// Package bmc wraps github.com/bougou/go-ipmi with a small, stable surface
// tailored to the web API: connect to a BMC and read sensors, power state,
// the event log, and device/FRU information.
package bmc

// Target identifies a BMC to connect to. It mirrors the connection-relevant
// fields of an inventory.Device without importing that package, keeping bmc
// independent of storage concerns.
type Target struct {
	Host     string
	Port     int
	Username string
	Password string
	// Protocol is "auto", "1.5", or "2.0".
	Protocol string
}

// PowerState is a decoded snapshot of the chassis power/identify status.
type PowerState struct {
	On                 bool   `json:"on"`
	PowerRestorePolicy string `json:"powerRestorePolicy"`
	PowerFault         bool   `json:"powerFault"`
	PowerOverload      bool   `json:"powerOverload"`
	Intrusion          bool   `json:"intrusion"`
	CoolingFault       bool   `json:"coolingFault"`
	DriveFault         bool   `json:"driveFault"`
	IdentifyActive     bool   `json:"identifyActive"`
}

// SensorReading is a single decoded sensor value safe to serialize to JSON.
type SensorReading struct {
	Number   uint8   `json:"number"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Reading  string  `json:"reading"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
	Status   string  `json:"status"`
	IsThresh bool    `json:"isThreshold"`
	Valid    bool    `json:"valid"`

	// Threshold bounds (only populated for valid threshold sensors).
	LowerCritical *float64 `json:"lowerCritical,omitempty"`
	LowerNonCrit  *float64 `json:"lowerNonCritical,omitempty"`
	UpperNonCrit  *float64 `json:"upperNonCritical,omitempty"`
	UpperCritical *float64 `json:"upperCritical,omitempty"`
}

// SELEntry is a decoded System Event Log record.
type SELEntry struct {
	RecordID    uint16 `json:"recordId"`
	RecordType  string `json:"recordType"`
	Timestamp   string `json:"timestamp,omitempty"`
	SensorType  string `json:"sensorType,omitempty"`
	SensorName  string `json:"sensorName,omitempty"`
	Description string `json:"description"`
	Severity    string `json:"severity,omitempty"`
}

// DeviceInfo summarizes the BMC identity (Get Device ID + system/FRU details).
type DeviceInfo struct {
	Manufacturer    string `json:"manufacturer,omitempty"`
	Product         string `json:"product,omitempty"`
	SerialNumber    string `json:"serialNumber,omitempty"`
	FirmwareVersion string `json:"firmwareVersion,omitempty"`
	IPMIVersion     string `json:"ipmiVersion,omitempty"`
	DeviceID        uint8  `json:"deviceId"`
}
