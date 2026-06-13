package bmc

import (
	"context"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// PowerAction is a chassis power control verb exposed to the API.
type PowerAction string

const (
	PowerOn       PowerAction = "on"
	PowerOff      PowerAction = "off"
	PowerCycle    PowerAction = "cycle"
	PowerReset    PowerAction = "reset"
	PowerSoftOff  PowerAction = "soft"
	PowerDiagIntr PowerAction = "diag"
)

var powerActionMap = map[PowerAction]goipmi.ChassisControl{
	PowerOn:       goipmi.ChassisControlPowerUp,
	PowerOff:      goipmi.ChassisControlPowerDown,
	PowerCycle:    goipmi.ChassisControlPowerCycle,
	PowerReset:    goipmi.ChassisControlHardReset,
	PowerSoftOff:  goipmi.ChassisControlSoftShutdown,
	PowerDiagIntr: goipmi.ChassisControlDiagnosticInterrupt,
}

// PowerStatus reads and decodes the current chassis status.
func (c *Client) PowerStatus(ctx context.Context) (PowerState, error) {
	resp, err := c.ipmi.GetChassisStatus(ctx)
	if err != nil {
		return PowerState{}, fmt.Errorf("bmc: get chassis status: %w", err)
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

// Power applies a chassis control action.
func (c *Client) Power(ctx context.Context, action PowerAction) error {
	control, ok := powerActionMap[action]
	if !ok {
		return fmt.Errorf("bmc: unknown power action %q", action)
	}
	if _, err := c.ipmi.ChassisControl(ctx, control); err != nil {
		return fmt.Errorf("bmc: chassis control %s: %w", action, err)
	}
	return nil
}
