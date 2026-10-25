package inbox

import (
	"context"
	"os"
	"testing"
)

// Against a mock built from Meta's own published OpenAPI specification of the WhatsApp Business
// Platform (facebook/openapi business-messaging-api_v23.0.yaml), served by Stoplight Prism 4 with
// --errors, which refuses a request the specification does not allow:
//
//	docker run -p 127.0.0.1:4012:4012 -v DIR:/s stoplight/prism:4 mock -h 0.0.0.0 -p 4012 --errors /s/business-messaging-api_v23.0.yaml
//	BITVOICE_TEST_SPEC_META=http://127.0.0.1:4012 go test ./internal/inbox -run Spec
func TestSpecWhatsApp(t *testing.T) {
	base := os.Getenv("BITVOICE_TEST_SPEC_META")
	if base == "" {
		t.Skip("set BITVOICE_TEST_SPEC_META to a Prism mock of Meta's specification")
	}
	old := GraphBase
	GraphBase = base + "/v23.0"
	t.Cleanup(func() { GraphBase = old })
	ctx := context.Background()
	if id, err := SendWhatsApp(ctx, "106540352242922", "TOKEN", "+34600111222", "hello from BitVoice"); err != nil || id == "" {
		t.Fatalf("text: %q %v", id, err)
	}
	ts, err := WhatsAppTemplates(ctx, "102290129340398", "TOKEN")
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	t.Logf("%d templates read", len(ts))
	tpl := Template{Name: "order_ready", Language: "en_US"}
	if id, err := SendWhatsAppTemplate(ctx, "106540352242922", "TOKEN", "+34600111222", tpl, []string{"Carla", "A-17"}); err != nil || id == "" {
		t.Fatalf("template: %q %v", id, err)
	}
}
