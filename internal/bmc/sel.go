package bmc

import (
	"context"
	"errors"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// SEL reads the System Event Log. It iterates records itself (rather than using
// go-ipmi's GetSELEntries helper) so it can tolerate firmware that reports an
// empty or short log with completion code 0xCB ("record not found") instead of
// a clean empty result — go-ipmi's helper surfaces that 0xCB as a hard error,
// which made the Event Log tab fail on some BMCs. Here 0xCB is treated as
// end-of-log, so an empty SEL reads as zero entries.
func (c *Client) SEL(ctx context.Context) ([]SELEntry, error) {
	// GetSELInfo gives the entry count up front (and works around a Huawei
	// NextRecordID quirk noted in go-ipmi). An empty log needs no iteration.
	info, err := c.ipmi.GetSELInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("bmc: get sel info: %w", err)
	}
	out := make([]SELEntry, 0, info.Entries)
	if info.Entries == 0 {
		return out, nil
	}

	var recordID uint16 = 0x0000 // 0x0000 = first record
	for {
		resp, err := c.ipmi.GetSELEntry(ctx, 0, recordID)
		if err != nil {
			if isRecordNotPresent(err) {
				break // empty/short log — end of records
			}
			return nil, fmt.Errorf("bmc: get sel entry %#04x: %w", recordID, err)
		}
		sel, err := goipmi.ParseSEL(resp.Data)
		if err != nil {
			return nil, fmt.Errorf("bmc: parse sel record %#04x: %w", recordID, err)
		}
		out = append(out, decodeSEL(sel))

		recordID = resp.NextRecordID
		if recordID == 0xffff { // 0xFFFF = no more records
			break
		}
	}
	return out, nil
}

// isRecordNotPresent reports whether err is an IPMI response carrying completion
// code 0xCB (requested record not present), which some BMCs return at the end
// of — or in place of — an empty System Event Log.
func isRecordNotPresent(err error) bool {
	var respErr *goipmi.ResponseError
	if errors.As(err, &respErr) {
		return respErr.CompletionCode() == goipmi.CompletionCodeRequestedDataNotPresent
	}
	return false
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
