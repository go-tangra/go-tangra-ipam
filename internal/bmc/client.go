package bmc

import (
	"context"
	"fmt"
	"time"

	goipmi "github.com/bougou/go-ipmi"
)

// DefaultTimeout bounds a single connect/command exchange.
const DefaultTimeout = 10 * time.Second

// Client is a connected session to one BMC. It is not safe for concurrent use;
// callers should serialize commands per client (the server keeps one client per
// active request).
type Client struct {
	ipmi   *goipmi.Client
	target Target
}

// Connect opens an IPMI LAN session to the target. The caller must Close the
// returned client. The supplied context bounds the connect handshake.
func Connect(ctx context.Context, t Target) (*Client, error) {
	if t.Host == "" {
		return nil, fmt.Errorf("bmc: host is required")
	}
	port := t.Port
	if port == 0 {
		port = 623
	}
	ic, err := goipmi.NewClient(t.Host, port, t.Username, t.Password)
	if err != nil {
		return nil, fmt.Errorf("bmc: new client %s: %w", t.Host, err)
	}
	ic.WithTimeout(DefaultTimeout)

	if err := connectWithProtocol(ctx, ic, t.Protocol); err != nil {
		return nil, fmt.Errorf("bmc: connect %s: %w", t.Host, err)
	}
	return &Client{ipmi: ic, target: t}, nil
}

func connectWithProtocol(ctx context.Context, ic *goipmi.Client, protocol string) error {
	switch protocol {
	case "1.5":
		return ic.Connect15(ctx)
	case "2.0":
		return ic.Connect20(ctx)
	case "auto", "":
		return ic.ConnectAuto(ctx)
	default:
		return fmt.Errorf("unknown protocol %q", protocol)
	}
}

// Close tears down the IPMI session. It is safe to call on a nil client.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.ipmi == nil {
		return nil
	}
	return c.ipmi.Close(ctx)
}
