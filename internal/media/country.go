package media

import "time"

// Country tones (ITU-T E.180 supplement). Each country's ringback is what callers there expect to hear
// while a phone rings; the hold tone is BitVoice's own and the same everywhere.

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

var ringbacks = map[string]Tone{
	"US": ToneRingback,
	"CA": ToneRingback,
	"GB": {Freqs: []float64{400, 450}, Level: 0.2, Cadence: []time.Duration{ms(400), ms(200), ms(400), ms(2000)}},
	"IE": {Freqs: []float64{400, 450}, Level: 0.2, Cadence: []time.Duration{ms(400), ms(200), ms(400), ms(2000)}},
	"AU": {Freqs: []float64{400, 450}, Level: 0.2, Cadence: []time.Duration{ms(400), ms(200), ms(400), ms(2000)}},
	"NZ": {Freqs: []float64{400, 450}, Level: 0.2, Cadence: []time.Duration{ms(400), ms(200), ms(400), ms(2000)}},
	"DE": {Freqs: []float64{425}, Level: 0.2, On: time.Second, Off: 4 * time.Second},
	"FR": {Freqs: []float64{440}, Level: 0.2, On: 1500 * time.Millisecond, Off: 3500 * time.Millisecond},
	"ES": {Freqs: []float64{425}, Level: 0.2, On: 1500 * time.Millisecond, Off: 3 * time.Second},
	"IT": {Freqs: []float64{425}, Level: 0.2, On: time.Second, Off: 4 * time.Second},
	"NL": {Freqs: []float64{425}, Level: 0.2, On: time.Second, Off: 4 * time.Second},
	"IN": {Freqs: []float64{400, 25}, Level: 0.2, Cadence: []time.Duration{ms(400), ms(200), ms(400), ms(2000)}},
	"JP": {Freqs: []float64{400, 15}, Level: 0.2, On: time.Second, Off: 2 * time.Second},
}

// Ringback returns the ringback tone for an ISO 3166 country code; the ITU 425 Hz tone for countries not
// listed, North American ringback when no country is set.
func Ringback(country string) Tone {
	if country == "" {
		return ToneRingback
	}
	if t, ok := ringbacks[country]; ok {
		return t
	}
	return Tone{Freqs: []float64{425}, Level: 0.2, On: time.Second, Off: 4 * time.Second}
}
