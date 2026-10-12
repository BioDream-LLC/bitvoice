package fax

import (
	"bytes"
	"testing"
)

func TestIFPRoundTrip(t *testing.T) {
	cases := []IFP{
		{Indicator: true, Kind: IndCED},
		{Indicator: true, Kind: IndV17_14400Long},
		{Kind: DataV21, Fields: []Field{{Type: FieldHDLCData, Data: []byte{0xff, 0x13, 0x80, 0x00, 0xee}}, {Type: FieldHDLCFCSOKSigEnd}}},
		{Kind: DataV17_14400, Fields: []Field{{Type: FieldT4Data, Data: bytes.Repeat([]byte{0}, 300)}}},
		{Kind: DataV21, Fields: []Field{{Type: FieldHDLCFCSOK}, {Type: FieldHDLCSigEnd}}},
	}
	for _, c := range cases {
		b := c.Marshal()
		got, err := ParseIFP(b)
		if err != nil || got.Indicator != c.Indicator || got.Kind != c.Kind || len(got.Fields) != len(c.Fields) {
			t.Fatalf("%+v -> % x -> %+v %v", c, b, got, err)
		}
		for i := range c.Fields {
			if got.Fields[i].Type != c.Fields[i].Type || !bytes.Equal(got.Fields[i].Data, c.Fields[i].Data) {
				t.Fatalf("field %d: %+v", i, got.Fields[i])
			}
		}
	}
	// Known encodings: the CED indicator is one octet 0x04; V.21 data with one HDLC field begins 0xc0.
	if b := (IFP{Indicator: true, Kind: IndCED}).Marshal(); !bytes.Equal(b, []byte{0x04}) {
		t.Fatalf("CED % x", b)
	}
	if b := cases[2].Marshal(); b[0] != 0xc0 || b[1] != 2 || b[2] != 0x80 || b[len(b)-1] != 0x40 {
		t.Fatalf("V.21 % x", b) // the frame's end (hdlc-fcs-OK-sig-end) is 0x40, as Asterisk sends it
	}
	u := MarshalUDPTL(7, []byte{1, 2}, [][]byte{{3}, {4, 5}})
	p, err := ParseUDPTL(u)
	if err != nil || p.Seq != 7 || !bytes.Equal(p.Primary, []byte{1, 2}) || len(p.Secondary) != 2 || !bytes.Equal(p.Secondary[1], []byte{4, 5}) {
		t.Fatalf("udptl %+v %v", p, err)
	}
}
