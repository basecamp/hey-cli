package auth

import (
	"strings"
	"testing"
)

func TestCallbackPagesRender(t *testing.T) {
	tests := []struct {
		name string
		page string
	}{
		{name: "success", page: callbackSuccess},
		{name: "error", page: callbackError},
		{name: "denied", page: callbackDenied},
		{name: "invalid", page: callbackInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.page, "<!DOCTYPE html>") {
				t.Error("page is not wrapped in the shell template")
			}
			if !strings.Contains(tt.page, "<svg") {
				t.Error("page does not contain the HEY logo")
			}
			if !strings.Contains(tt.page, "<h1>") {
				t.Error("page does not contain a heading")
			}
			if strings.Contains(tt.page, "{{") {
				t.Error("page contains an unrendered template directive")
			}
		})
	}
}

func TestRenderCallbackUnknownFile(t *testing.T) {
	if _, err := renderCallback(callbackFS, "callback_missing.html"); err == nil {
		t.Fatal("expected an error for an unknown template file")
	}
}
