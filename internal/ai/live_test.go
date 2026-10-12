package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveModel runs BitVoice's prompts against a real model, to check that what real models answer
// is understood. It needs BITVOICE_TEST_AI_KEY (an OpenRouter key) and runs nothing otherwise;
// BITVOICE_TEST_AI_MODEL picks the model (meta-llama/llama-3.3-70b-instruct by default).
func TestLiveModel(t *testing.T) {
	key := os.Getenv("BITVOICE_TEST_AI_KEY")
	if key == "" {
		t.Skip("set BITVOICE_TEST_AI_KEY to run against a real model")
	}
	model := os.Getenv("BITVOICE_TEST_AI_MODEL")
	if model == "" {
		model = "meta-llama/llama-3.3-70b-instruct"
	}
	c := &Client{BaseURL: DefaultBaseURL, Key: key, Model: model}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	call := "[0s] Agent: Thanks for calling Acme Dental, this is Sam.\n" +
		"[4s] Caller: Hi, I've had a terrible toothache since Tuesday and I'm really worried.\n" +
		"[11s] Agent: I'm sorry to hear that. We can see you tomorrow at 9. I'll text you the address.\n" +
		"[18s] Caller: Thank you so much, that's a relief.\n"

	n, err := c.SummarizeCall(ctx, call)
	if err != nil || n.Summary == "" || len(n.ActionItems) == 0 {
		t.Fatalf("summary: %+v %v", n, err)
	}
	t.Logf("summary: %s | actions: %q", n.Summary, n.ActionItems)

	in, err := c.CallInsight(ctx, call)
	if err != nil || in.MoodStart == "" || in.MoodEnd == "" {
		t.Fatalf("insight: %+v %v", in, err)
	}
	t.Logf("mood %s -> %s, %d moments", in.MoodStart, in.MoodEnd, len(in.Observations))

	tr, err := c.TriageVoicemail(ctx, "Hi, this is Jo from the bank. Your card payment failed and the account will be closed today unless you call back at once.")
	if err != nil || !urgencies[tr.Urgency] || !categories[tr.Category] {
		t.Fatalf("triage: %+v %v", tr, err)
	}
	t.Logf("triage: %s %s (%s)", tr.Urgency, tr.Category, tr.Reason)

	turn, err := c.Reception(ctx, "Acme Dental", "Open Monday to Friday 8 to 5. Cleaning costs 90 dollars.", "+12125550100",
		[]string{"caller: How much is a cleaning, and are you open Saturday?"})
	if err != nil || turn.Say == "" || !strings.Contains(turn.Say, "90") {
		t.Fatalf("receptionist: %+v %v", turn, err)
	}
	t.Logf("receptionist (%s): %s", turn.Action, turn.Say)
	turn, err = c.Reception(ctx, "Acme Dental", "Open Monday to Friday 8 to 5.", "+12125550100",
		[]string{"caller: I want to speak to a person please."})
	if err != nil || turn.Action != "transfer" {
		t.Fatalf("asked for a person: %+v %v", turn, err)
	}
}
