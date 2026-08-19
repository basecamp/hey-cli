package tui

import "testing"

func TestImageRendererSelection(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want imageProtocol
	}{
		{"Kitty window", map[string]string{"KITTY_WINDOW_ID": "1"}, imageProtocolKitty},
		{"Kitty TERM", map[string]string{"TERM": "xterm-kitty"}, imageProtocolKitty},
		{"Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, imageProtocolKitty},
		{"Foot uses text until Sixel renderer is available", map[string]string{"TERM": "foot"}, imageProtocolText},
		{"unsupported terminal", map[string]string{"TERM": "xterm-256color"}, imageProtocolText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderer := selectImageRenderer(func(key string) string { return tt.env[key] })
			if got := renderer.protocol(); got != tt.want {
				t.Errorf("selected protocol = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTerminalCapabilityRecognizesSixel(t *testing.T) {
	capability := detectImageCapability(func(key string) string {
		if key == "TERM" {
			return "foot"
		}
		return ""
	})
	if capability != imageProtocolSixel {
		t.Errorf("Foot capability = %q, want %q", capability, imageProtocolSixel)
	}
}

func TestTextRendererDoesNotEmitInvisiblePlaceholders(t *testing.T) {
	rendered := textImageRenderer{}.render([]byte("image"), 1, 40)
	if rendered.content != "" || rendered.raw != "" {
		t.Errorf("text renderer output = content %q raw %q, want visible message marker only", rendered.content, rendered.raw)
	}
}

func TestKittyRendererPreservesUnicodePlaceholderBehavior(t *testing.T) {
	rendered := kittyImageRenderer{}.render([]byte("not an image"), 1, 40)
	if rendered.content == "" || rendered.raw == "" {
		t.Fatalf("Kitty renderer output = content %q raw %q", rendered.content, rendered.raw)
	}
}
