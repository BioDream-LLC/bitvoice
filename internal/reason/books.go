package reason

// The rulebooks BitVoice's judges use. Each is checked against every combination of its facts by the tests
// (TestBooksCheckEverySituation) and again when the program starts.

// Trunk decides whether a trunk stays in its place in the outbound routes or is tried after the others: it tells
// a provider that is down from this system's own internet link being down, and does not trust a keep-alive check
// that fails while calls still get through.
var Trunk = &Book{
	ID: "trunk", Name: "Trunk health",
	About: "Decides, every 15 seconds, whether a trunk is used in its place or tried after the other trunks, from its keep-alive checks and what happened to its recent calls.",
	Rules: []Rule{
		{Name: "keepalive_failed", Kind: Fact, Text: "the trunk does not answer its keep-alive checks"},
		{Name: "calls_failing", Kind: Fact, Text: "its recent calls out failed on the provider's side, one after another"},
		{Name: "call_ok_recently", Kind: Fact, Text: "the provider accepted a call through it in the last 10 minutes"},
		{Name: "other_way_out", Kind: Fact, Text: "another trunk is working now"},
		{Name: "all_failing", Kind: Fact, Text: "every other trunk is failing too"},

		{Name: "not_answering", Kind: Conclusion, Text: "the provider is not answering", Needs: []string{"keepalive_failed"}},
		{Name: "refusing_calls", Kind: Conclusion, Text: "the provider is refusing calls", Needs: []string{"calls_failing"}},
		{Name: "calls_get_through", Kind: Conclusion, Text: "calls still get through, so a failing keep-alive alone is not trusted", Needs: []string{"call_ok_recently"}},
		{Name: "our_link", Kind: Conclusion, Text: "every trunk is failing at once, so the likelier cause is this system's own internet link, not one provider", Needs: []string{"all_failing"}},

		{Name: "avoid_down", Kind: Action, Does: "last", Text: "the provider is not answering and another trunk works", Needs: []string{"not_answering", "other_way_out"}},
		{Name: "avoid_refusing", Kind: Action, Does: "last", Text: "the provider is refusing calls and another trunk works", Needs: []string{"refusing_calls", "other_way_out"}},
		{Name: "stay_link", Kind: Action, Does: "use", Text: "moving between trunks will not help while every one is failing", Needs: []string{"our_link"}},
	},
	Hits: []Attack{
		{From: "calls_get_through", To: "not_answering"},
		{From: "our_link", To: "not_answering"},
		{From: "our_link", To: "refusing_calls"},
	},
	Choices: []Choice{
		{Does: "use", Text: "Use it in its place"},
		{Does: "last", Text: "Try it after the other trunks (it is still used if they fail)"},
	},
	Cautious: "use", Default: "use",
}

// Fraud judges a call out of the country before it is placed: toll fraud runs up bills abroad, fast and out of
// hours, often through a stolen extension password or a dial-in. An emergency call is never stopped.
var Fraud = &Book{
	ID: "fraud", Name: "Toll-fraud guard",
	About: "Judges every call out of the country before it is placed, and lets it through, tells the administrator, asks for a PIN, or refuses it.",
	Rules: []Rule{
		{Name: "emergency", Kind: Fact, Text: "the number is an emergency number"},
		{Name: "international", Kind: Fact, Text: "the call goes out of the country"},
		{Name: "new_country", Kind: Fact, Text: "this extension has not called that country in the last 90 days"},
		{Name: "burst", Kind: Fact, Text: "this extension made many calls out of the country in the last 10 minutes"},
		{Name: "after_hours", Kind: Fact, Text: "it is outside business hours"},
		{Name: "from_outside", Kind: Fact, Text: "the call was placed from outside the office (a dial-in, or a call forwarded out)"},
		{Name: "attack_seen", Kind: Fact, Text: "someone has been guessing passwords or PINs on this system in the last hour"},
		{Name: "approved", Kind: Fact, Text: "an exception approved for this extension covers today"},
		{Name: "allowed_country", Kind: Fact, Text: "the country is on the always-allowed list"},

		{Name: "unusual", Kind: Conclusion, Text: "it is this extension's first call to that country in 90 days", Needs: []string{"international", "new_country"}},
		{Name: "rapid", Kind: Conclusion, Text: "many calls abroad in a short time is how toll fraud looks", Needs: []string{"international", "burst"}},
		{Name: "odd_hours", Kind: Conclusion, Text: "a call abroad outside business hours", Needs: []string{"international", "after_hours"}},
		{Name: "risky_origin", Kind: Conclusion, Text: "a call abroad placed from outside the office", Needs: []string{"international", "from_outside"}},
		{Name: "under_attack", Kind: Conclusion, Text: "a call abroad while someone is attacking the system", Needs: []string{"international", "attack_seen"}},
		{Name: "known_trip", Kind: Conclusion, Text: "an approved exception covers this extension", Needs: []string{"approved"}},
		{Name: "known_place", Kind: Conclusion, Text: "calls to that country are always allowed", Needs: []string{"allowed_country"}},
		{Name: "life", Kind: Conclusion, Text: "an emergency call is never stopped", Needs: []string{"emergency"}},

		{Name: "block_rapid", Kind: Action, Does: "block", Text: "too many calls abroad in a short time", Needs: []string{"rapid"}},
		{Name: "block_attack", Kind: Action, Does: "block", Text: "a first call abroad while the system is under attack", Needs: []string{"under_attack", "unusual"}},
		{Name: "block_attack_outside", Kind: Action, Does: "block", Text: "a call abroad from outside while the system is under attack", Needs: []string{"under_attack", "risky_origin"}},
		{Name: "challenge_hours", Kind: Action, Does: "challenge", Text: "a first call to that country outside business hours", Needs: []string{"unusual", "odd_hours"}},
		{Name: "challenge_outside", Kind: Action, Does: "challenge", Text: "a first call to that country placed from outside", Needs: []string{"unusual", "risky_origin"}},
		{Name: "alert_new", Kind: Action, Does: "alert", Text: "a first call to that country", Needs: []string{"unusual"}},
		{Name: "alert_attack", Kind: Action, Does: "alert", Text: "a call abroad while the system is under attack", Needs: []string{"under_attack"}},
		{Name: "allow_life", Kind: Action, Does: "allow", Text: "an emergency call", Needs: []string{"life"}},
	},
	Hits: []Attack{
		{From: "known_trip", To: "unusual"},
		{From: "known_trip", To: "odd_hours"},
		{From: "known_trip", To: "risky_origin"},
		{From: "known_place", To: "unusual"},
		{From: "known_place", To: "odd_hours"},
		// The stronger answer wins over the milder one.
		{From: "block_rapid", To: "challenge_hours"}, {From: "block_rapid", To: "challenge_outside"},
		{From: "block_rapid", To: "alert_new"}, {From: "block_rapid", To: "alert_attack"},
		{From: "block_attack", To: "challenge_hours"}, {From: "block_attack", To: "challenge_outside"},
		{From: "block_attack", To: "alert_new"}, {From: "block_attack", To: "alert_attack"},
		{From: "block_attack_outside", To: "challenge_hours"}, {From: "block_attack_outside", To: "challenge_outside"},
		{From: "block_attack_outside", To: "alert_new"}, {From: "block_attack_outside", To: "alert_attack"},
		{From: "challenge_hours", To: "alert_new"}, {From: "challenge_hours", To: "alert_attack"},
		{From: "challenge_outside", To: "alert_new"}, {From: "challenge_outside", To: "alert_attack"},
		// Nothing stops an emergency call.
		{From: "life", To: "block_rapid"}, {From: "life", To: "block_attack"}, {From: "life", To: "block_attack_outside"},
		{From: "life", To: "challenge_hours"}, {From: "life", To: "challenge_outside"},
		{From: "life", To: "alert_new"}, {From: "life", To: "alert_attack"},
	},
	Choices: []Choice{
		{Does: "allow", Text: "Let the call through"},
		{Does: "alert", Text: "Let the call through and tell the administrator"},
		{Does: "challenge", Text: "Ask for a PIN before the call goes out"},
		{Does: "block", Text: "Refuse the call and tell the administrator"},
	},
	Cautious: "challenge", Default: "allow",
}

// Quality finds where a place's call sound is being lost, by comparing the two halves of the same calls.
var Quality = &Book{
	ID: "quality", Name: "Call quality diagnosis",
	About: "Reads a site's or provider's calls and says where sound is being lost, comparing each call's two halves: a site's phones and the provider the same calls went through.",
	Rules: []Rule{
		{Name: "few_calls", Kind: Fact, Text: "there were fewer than 3 calls to judge"},
		{Name: "loss_high", Kind: Fact, Text: "2% or more of the sound never arrived"},
		{Name: "jitter_high", Kind: Fact, Text: "the delay was uneven (jitter of 30 ms or more)"},
		{Name: "poor_many", Kind: Fact, Text: "one call in ten or more sounded poor"},
		{Name: "everywhere_lossy", Kind: Fact, Text: "every place with calls lost sound in the same time"},
		{Name: "other_side_clean", Kind: Fact, Text: "the other half of the same calls arrived clean"},
		{Name: "other_side_lossy", Kind: Fact, Text: "the other half of the same calls lost sound too"},

		{Name: "losing", Kind: Conclusion, Text: "sound is being lost on these calls", Needs: []string{"loss_high"}},
		{Name: "here", Kind: Conclusion, Text: "the other half of the same calls arrived clean, so the loss is on this place's side", Needs: []string{"losing", "other_side_clean"}},
		{Name: "shared", Kind: Conclusion, Text: "every place loses sound at once, which points to what they share: this system's internet link", Needs: []string{"losing", "everywhere_lossy"}},
		{Name: "both_sides", Kind: Conclusion, Text: "both halves of the same calls lost sound, which points to this system's own link rather than this place", Needs: []string{"losing", "other_side_lossy"}},
		{Name: "choppy", Kind: Conclusion, Text: "words may sound choppy from the uneven delay", Needs: []string{"jitter_high"}},
		{Name: "unclear", Kind: Conclusion, Text: "calls sounded poor without one clear cause", Needs: []string{"poor_many"}},
		{Name: "too_few", Kind: Conclusion, Text: "too few calls to judge", Needs: []string{"few_calls"}},

		{Name: "diag_here", Kind: Action, Does: "here", Text: "the loss is on this place's side", Needs: []string{"here"}},
		{Name: "diag_losing", Kind: Action, Does: "here", Text: "sound is lost on this place's link", Needs: []string{"losing"}},
		{Name: "diag_shared", Kind: Action, Does: "shared", Text: "every place is affected", Needs: []string{"shared"}},
		{Name: "diag_both", Kind: Action, Does: "shared", Text: "both halves of the calls lost sound", Needs: []string{"both_sides"}},
		{Name: "diag_jitter", Kind: Action, Does: "jitter", Text: "uneven delay", Needs: []string{"choppy"}},
		{Name: "diag_poor", Kind: Action, Does: "poor", Text: "poor calls without one cause", Needs: []string{"unclear"}},
		{Name: "diag_few", Kind: Action, Does: "idle", Text: "too few calls", Needs: []string{"too_few"}},
	},
	Hits: []Attack{
		{From: "too_few", To: "losing"}, {From: "too_few", To: "choppy"}, {From: "too_few", To: "unclear"},
		{From: "here", To: "shared"}, {From: "here", To: "both_sides"},
		{From: "shared", To: "diag_losing"}, {From: "both_sides", To: "diag_losing"},
		{From: "other_side_clean", To: "both_sides"},
		{From: "losing", To: "diag_jitter"}, {From: "losing", To: "diag_poor"},
		{From: "choppy", To: "diag_poor"},
	},
	Choices: []Choice{
		{Does: "ok", Text: "Calls sound good"},
		{Does: "here", Text: "Sound is being lost on this place's side"},
		{Does: "shared", Text: "Sound is being lost on this system's own internet link, not at this place alone"},
		{Does: "jitter", Text: "The delay is uneven, so words may sound choppy"},
		{Does: "poor", Text: "Calls sounded poor without one clear cause"},
		{Does: "idle", Text: "Too few calls to judge"},
		{Does: "unsure", Text: "Cannot tell where the trouble is"},
	},
	Cautious: "unsure", Default: "ok",
}

// Location chooses which of two locations for an emergency caller to show first when they disagree (a
// rebid's answer against the one the call came with).
var Location = &Book{
	ID: "location", Name: "Caller location check",
	About: "When an emergency caller's location changes (a rebid, or a lookup by callback number), judges which to show first, and says when they disagree.",
	Rules: []Rule{
		{Name: "new_measured", Kind: Fact, Text: "the new location was measured by the device (GPS or similar)"},
		{Name: "old_measured", Kind: Fact, Text: "the first location was measured by the device"},
		{Name: "new_precise", Kind: Fact, Text: "the new location is within 100 m"},
		{Name: "old_precise", Kind: Fact, Text: "the first location is within 100 m"},
		{Name: "far_apart", Kind: Fact, Text: "the two locations are further apart than their uncertainty allows"},
		{Name: "old_stale", Kind: Fact, Text: "the first location is more than 2 minutes old"},

		{Name: "newer", Kind: Conclusion, Text: "the new location is the latest", Needs: []string{}},
		{Name: "rougher", Kind: Conclusion, Text: "the new location is rougher than a precise device fix the call already had", Needs: []string{"old_measured", "old_precise"}},
		{Name: "new_good", Kind: Conclusion, Text: "the new location is a precise device fix", Needs: []string{"new_measured", "new_precise"}},
		{Name: "moved_on", Kind: Conclusion, Text: "the first fix is old, so the caller may have moved", Needs: []string{"old_stale"}},
		{Name: "disagree", Kind: Conclusion, Text: "the two locations disagree", Needs: []string{"far_apart"}},

		{Name: "show_new", Kind: Action, Does: "new", Text: "the latest location", Needs: []string{"newer"}},
		{Name: "keep_old", Kind: Action, Does: "old", Text: "the first location was a precise device fix", Needs: []string{"rougher"}},
		{Name: "show_both", Kind: Action, Does: "both", Text: "they disagree", Needs: []string{"disagree"}},
	},
	Hits: []Attack{
		{From: "new_good", To: "rougher"},
		{From: "moved_on", To: "rougher"},
		{From: "new_precise", To: "rougher"},
		{From: "rougher", To: "show_new"},
		{From: "disagree", To: "show_new"}, {From: "disagree", To: "keep_old"},
	},
	Choices: []Choice{
		{Does: "new", Text: "Show the new location first"},
		{Does: "old", Text: "Keep showing the first location first"},
		{Does: "both", Text: "Show both: the locations disagree, ask the caller"},
	},
	Cautious: "both", Default: "new",
}

// Books are every rulebook, for the screen that shows them and the start-up check.
var Books = []*Book{Trunk, Fraud, Quality, Location}

// Lookup finds a rulebook by id.
func Lookup(id string) *Book {
	for _, b := range Books {
		if b.ID == id {
			return b
		}
	}
	return nil
}
