package db

import (
	"context"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
)

// How long BitVoice waits on a database it cannot reach. Without limits, a connection to a database
// whose network went quiet (a firewall dropping packets, a cut link) waits as long as the operating
// system keeps retrying: minutes on Linux.
const (
	// connectWait bounds a new connection, TLS and sign-in included, unless the address names its
	// own connect_timeout.
	connectWait = 5 * time.Second
	// A connection idle this long is probed, then given up after probeCount unanswered probes.
	probeIdle     = 5 * time.Second
	probeInterval = 2 * time.Second
	probeCount    = 3
	// unacked is how long sent data may go unacknowledged before the connection is given up (Linux).
	unacked = 10 * time.Second
)

// Patient sets the limits on a PostgreSQL connection's configuration: a deadline on connecting, and
// TCP keepalive so a connection to a database that went quiet is noticed even while it waits for a
// notification. A connect_timeout in the address is kept.
func Patient(cfg *pgx.ConnConfig) {
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = connectWait
	}
	d := &net.Dialer{
		Timeout: cfg.ConnectTimeout,
		KeepAliveConfig: net.KeepAliveConfig{
			Enable: true, Idle: probeIdle, Interval: probeInterval, Count: probeCount,
		},
		Control: limitUnacked,
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return d.DialContext(ctx, network, addr)
	}
}
