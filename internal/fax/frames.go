package fax

import "strings"

// T.30 control fields (FCF), as octets in transmission order (least significant bit first, as HDLC
// and T.38 carry them). Commands from the station that received DIS have the low (X) bit set;
// compare with fcf&^1.
const (
	fcfDIS = 0x80
	fcfCSI = 0x40
	fcfNSF = 0x20
	fcfDCS = 0x82
	fcfTSI = 0x42
	fcfNSS = 0x22
	fcfCFR = 0x84
	fcfFTT = 0x44
	fcfEOM = 0x8e
	fcfMPS = 0x4e
	fcfEOP = 0x2e
	fcfMCF = 0x8c
	fcfRTN = 0x4c
	fcfRTP = 0xcc
	fcfDCN = 0xfa
	fcfCRP = 0x1a
)

const (
	hdlcAddress    = 0xff
	hdlcControl    = 0x03 // a frame followed by another
	hdlcControlEnd = 0x13 // the last frame of a block
)

// frame builds an HDLC frame (address, control, FCF, information); T.38 leaves out the FCS.
func frame(last bool, fcf byte, info ...byte) []byte {
	ctl := byte(hdlcControl)
	if last {
		ctl = hdlcControlEnd
	}
	return append([]byte{hdlcAddress, ctl, fcf}, info...)
}

// setBit sets T.30 FIF bit n (numbered from 1, in transmission order).
func setBit(fif []byte, n int) { fif[(n-1)/8] |= 1 << uint((n-1)%8) }

func bit(fif []byte, n int) bool {
	i := (n - 1) / 8
	return i < len(fif) && fif[i]&(1<<uint((n-1)%8)) != 0
}

// disInfo is BitVoice's capabilities as a receiver: V.27 ter, V.29 and V.17, fine resolution, 215 mm
// wide, any length, no minimum scan line time, one-dimensional coding (MH) only, no error correction.
func disInfo() []byte {
	fif := make([]byte, 3)
	setBit(fif, 10) // ready to receive
	setBit(fif, 11) // data signalling rates 11-14 = 1,1,0,1:
	setBit(fif, 12) // V.27 ter, V.29 and V.17
	setBit(fif, 14) //
	setBit(fif, 15) // fine resolution (7.7 lines/mm)
	setBit(fif, 20) // recording length: unlimited
	setBit(fif, 21) // minimum scan line time 0 ms (21-23 = 1,1,1)
	setBit(fif, 22) //
	setBit(fif, 23) //
	return fif
}

// Params is what the sender chose in DCS.
type Params struct {
	Fine  bool // 196 lines per inch, else 98
	TwoD  bool // modified READ (MR) coding
	Width int  // pixels per line
}

func parseDCS(fif []byte) Params {
	p := Params{Fine: bit(fif, 15), TwoD: bit(fif, 16), Width: 1728}
	switch {
	case !bit(fif, 17) && bit(fif, 18):
		p.Width = 2432 // 303 mm
	case bit(fif, 17) && !bit(fif, 18):
		p.Width = 2048 // 255 mm
	}
	return p
}

// idInfo encodes a station identity (CSI, TSI): 20 characters, sent last character first.
func idInfo(id string) []byte {
	id = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' || r == ' ' {
			return r
		}
		return -1
	}, id)
	if len(id) > 20 {
		id = id[:20]
	}
	b := []byte(id + strings.Repeat(" ", 20-len(id)))
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return b
}

func parseID(info []byte) string {
	b := append([]byte(nil), info...)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return strings.TrimSpace(string(b))
}
