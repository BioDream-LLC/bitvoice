//go:build !linux

package vip

import (
	"errors"
	"net/netip"
)

func announce(string, netip.Addr) error {
	return errors.New("vip: a shared address needs Linux")
}
