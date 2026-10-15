//go:build !linux

package dahdi

import (
	"errors"

	"github.com/biodream-llc/bitvoice/internal/isdn"
)

func openChannel(int, bool) (Channel, error) {
	return nil, errors.New("dahdi: telephony cards need Linux and its DAHDI drivers")
}

func openDChannel(int) (isdn.Link, error) {
	return nil, errors.New("dahdi: telephony cards need Linux and its DAHDI drivers")
}
