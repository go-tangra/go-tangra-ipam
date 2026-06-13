package bmc

import (
	"context"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// Sensors reads all sensors and decodes them into serializable readings.
func (c *Client) Sensors(ctx context.Context) ([]SensorReading, error) {
	sensors, err := c.ipmi.GetSensors(ctx)
	if err != nil {
		return nil, fmt.Errorf("bmc: get sensors: %w", err)
	}
	out := make([]SensorReading, 0, len(sensors))
	for _, s := range sensors {
		out = append(out, decodeSensor(s))
	}
	return out, nil
}

func decodeSensor(s *goipmi.Sensor) SensorReading {
	r := SensorReading{
		Number:   s.Number,
		Name:     s.Name,
		Type:     s.SensorType.String(),
		Reading:  s.ReadingStr(),
		Value:    s.Value,
		Unit:     s.SensorUnit.String(),
		Status:   s.Status(),
		IsThresh: s.IsThreshold(),
		Valid:    s.IsReadingValid(),
	}
	if s.IsThreshold() && s.IsThresholdAndReadingValid() {
		r.LowerCritical = thresholdPtr(s, goipmi.SensorThresholdType_LCR)
		r.LowerNonCrit = thresholdPtr(s, goipmi.SensorThresholdType_LNC)
		r.UpperNonCrit = thresholdPtr(s, goipmi.SensorThresholdType_UNC)
		r.UpperCritical = thresholdPtr(s, goipmi.SensorThresholdType_UCR)
	}
	return r
}

// thresholdPtr returns a pointer to the threshold value if it is readable,
// otherwise nil so it is omitted from JSON.
func thresholdPtr(s *goipmi.Sensor, t goipmi.SensorThresholdType) *float64 {
	if !s.IsThresholdReadable(t) {
		return nil
	}
	v := s.ConvertReading(s.SensorThreshold(t).Raw)
	return &v
}
