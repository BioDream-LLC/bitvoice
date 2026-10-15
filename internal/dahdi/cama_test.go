package dahdi

import "testing"

func TestCAMAANI(t *testing.T) {
	npd := []string{"570", "717"}
	for _, c := range []struct{ in, ani, info string }{
		{"K007175550100S", "7175550100", "00"}, // enhanced
		{"K027175550100S", "7175550100", "02"},
		{"K05550100S", "5705550100", "0"},                                          // traditional, NPD 0
		{"K15550100C", "7175550100", "1"},                                          // ended by ST'''
		{"K35550100S", "5550100", "3"},                                             // no area code set for NPD 3
		{"K4911S", "", "raw:K4911S"},                                               // neither layout
		{"K40000000S", "", "4"},                                                    // ANI failure
		{"K007175550100SK0215551234567S", "", "raw:K007175550100SK0215551234567S"}, // a second spill of no known layout
		{"K007175550100SK027175550111S", "7175550111", "02"},                       // a second spill: the last one counts
	} {
		ani, info := camaANI(c.in, npd)
		if ani != c.ani || info != c.info {
			t.Errorf("%s: %q %q, want %q %q", c.in, ani, info, c.ani, c.info)
		}
	}
}
