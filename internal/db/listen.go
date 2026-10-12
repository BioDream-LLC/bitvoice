package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

// Notify tells every server listening on channel (PostgreSQL only; does nothing on SQLite, where there
// are no other servers). The payload must be under 8000 bytes.
func (d *DB) Notify(ctx context.Context, channel, payload string) error {
	if !d.Postgres {
		return nil
	}
	_, err := d.Write.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	return err
}

// Listen calls f with each payload sent to channel, until ctx ends, reconnecting after a failure.
// It holds one connection of its own.
func (d *DB) Listen(ctx context.Context, channel string, f func(payload string)) error {
	if !d.Postgres {
		return errors.New("db: listening needs PostgreSQL")
	}
	for ctx.Err() == nil {
		err := d.listenOnce(ctx, channel, f)
		if ctx.Err() != nil {
			return nil
		}
		_ = err
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return nil
}

func (d *DB) listenOnce(ctx context.Context, channel string, f func(string)) error {
	conn, err := d.Write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return errors.New("db: not a pgx connection")
		}
		pc := sc.Conn()
		if _, err := pc.Exec(ctx, `LISTEN "`+channel+`"`); err != nil {
			return err
		}
		for {
			n, err := pc.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			f(n.Payload)
		}
	})
}
