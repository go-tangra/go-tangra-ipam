package bmc

import (
	"context"
	"fmt"
	"strings"

	goipmi "github.com/bougou/go-ipmi"
)

// Info reads BMC identity from Get Device ID and enriches it with FRU product
// data when available. FRU failures are non-fatal — the basic device ID is
// always returned.
func (c *Client) Info(ctx context.Context) (DeviceInfo, error) {
	dev, err := c.ipmi.GetDeviceID(ctx)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("bmc: get device id: %w", err)
	}
	info := DeviceInfo{
		DeviceID:        dev.DeviceID,
		FirmwareVersion: fmt.Sprintf("%d.%02x", dev.MajorFirmwareRevision, dev.MinorFirmwareRevision),
		IPMIVersion:     fmt.Sprintf("%d.%d", dev.MajorIPMIVersion, dev.MinorIPMIVersion),
	}
	enrichFromFRU(ctx, c.ipmi, &info)
	return info, nil
}

// enrichFromFRU best-effort fills manufacturer/product/serial from FRU device 0.
func enrichFromFRU(ctx context.Context, ic *goipmi.Client, info *DeviceInfo) {
	fru, err := ic.GetFRU(ctx, 0, "Builtin FRU")
	if err != nil || fru == nil {
		return
	}
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
