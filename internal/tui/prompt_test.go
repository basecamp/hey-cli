package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func confirmAfter(t *testing.T, defaultYes bool, keys ...string) confirmModel {
	t.Helper()
	m := newConfirmModel("Sign in now?", defaultYes)
	for _, key := range keys {
		updated, _ := m.Update(keyPress(key))
		m = updated.(confirmModel)
	}
	return m
}

func TestConfirmDefaultHighlightsAnswer(t *testing.T) {
	yes := newConfirmModel("Sign in now?", true)
	if !yes.value {
		t.Error("defaultYes=true should preselect Yes")
	}
	no := newConfirmModel("Sign in now?", false)
	if no.value {
		t.Error("defaultYes=false should preselect No")
	}
}

func TestConfirmDirectAnswers(t *testing.T) {
	if m := confirmAfter(t, false, "y"); !m.value || m.canceled {
		t.Errorf("y should answer yes, got value=%v canceled=%v", m.value, m.canceled)
	}
	if m := confirmAfter(t, true, "n"); m.value || m.canceled {
		t.Errorf("n should answer no, got value=%v canceled=%v", m.value, m.canceled)
	}
}

func TestConfirmEnterAcceptsHighlighted(t *testing.T) {
	if m := confirmAfter(t, true, "enter"); !m.value {
		t.Error("enter should accept the default Yes")
	}
	if m := confirmAfter(t, true, "left", "enter"); m.value {
		t.Error("left should toggle to No before enter accepts")
	}
}

func TestConfirmEscapeCancels(t *testing.T) {
	if m := confirmAfter(t, true, "esc"); !m.canceled {
		t.Error("esc should cancel")
	}
	if m := confirmAfter(t, true, "ctrl+c"); !m.canceled {
		t.Error("ctrl+c should cancel")
	}
}

func TestConfirmQuitsOnAnswer(t *testing.T) {
	m := newConfirmModel("Sign in now?", true)
	_, cmd := m.Update(keyPress("y"))
	if cmd == nil {
		t.Fatal("answering should quit the prompt")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("answer produced %T, want tea.QuitMsg", cmd())
	}
}

func TestConfirmViewShowsMessageAndOptions(t *testing.T) {
	view := newConfirmModel("Sign in now?", true).View()
	text := view.Content
	for _, want := range []string{"Sign in now?", "Yes", "No", "esc cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirm view missing %q:\n%s", want, text)
		}
	}
}
