package cable

import "testing"

func TestURL(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://app.hey.com", "wss://app.hey.com/cable"},
		{"https://app.hey.com/", "wss://app.hey.com/cable"},
		{"http://app.hey.localhost:3003", "ws://app.hey.localhost:3003/cable"},
	}

	for _, c := range cases {
		got, err := URL(c.baseURL)
		if err != nil {
			t.Fatalf("URL(%q) failed: %v", c.baseURL, err)
		}
		if got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.baseURL, got, c.want)
		}
	}
}

func TestURLOverride(t *testing.T) {
	t.Setenv("HEY_CABLE_URL", "ws://cable.example.com/cable")

	got, err := URL("https://app.hey.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ws://cable.example.com/cable" {
		t.Errorf("URL = %q, want the HEY_CABLE_URL override", got)
	}
}

func TestURLRejectsOtherSchemes(t *testing.T) {
	if _, err := URL("ftp://app.hey.com"); err == nil {
		t.Fatal("expected an error for a base URL that isn't http or https")
	}
}
