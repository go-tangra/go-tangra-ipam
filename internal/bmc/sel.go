package bmc

import (
	"context"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// SEL reads the System Event Log, newest decoding applied. startRecordID of 0
// begins at the first record.
func (c *Client) SEL(ctx context.Context) ([]SELEntry, error) {
	entries, err := c.ipmi.GetSELEntries(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("bmc: get sel entries: %w", err)
	}
	out := make([]SELEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, decodeSEL(e))
	}
	return out, nil
}

func decodeSEL(e *goipmi.SEL) SELEntry {
	entry := SELEntry{
		RecordID:   e.RecordID,
		RecordType: e.RecordType.String(),
	}
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
