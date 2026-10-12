package pbx

import (
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// richCallData adds RFC 9796 rich call data to a call out: the brand's logo as a Call-Info icon, with
// the reason for the call. Carriers that sign it (STIR/SHAKEN "rcd" PASSporTs) show it on the phone.
func richCallData(inv *sip.Message, b *config.Brand) {
	if b.Logo == "" && b.Reason == "" {
		return
	}
	logo := b.Logo
	if logo == "" {
		logo = "data:,"
	}
	v := "<" + logo + ">;purpose=icon"
	if b.Reason != "" {
		v += `;call-reason="` + strings.NewReplacer(`"`, "'", `\`, "", "\r", " ", "\n", " ").Replace(b.Reason) + `"`
	}
	inv.Set("Call-Info", v)
}
