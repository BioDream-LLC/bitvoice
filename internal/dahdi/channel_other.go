//go:build !linux

package dahdi

import "errors"

func openChannel(int, bool) (Channel, error) {
	return nil, errors.New("dahdi: telephony cards need Linux and its DAHDI drivers")
}
