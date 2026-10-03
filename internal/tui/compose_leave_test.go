package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEscClosesAFormWithNothingToLose(t *testing.T) {
	v, _ := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "x")
	v.HandleContentKey(keyPress("backspace"))
	if form.edited() {
		t.Fatal("a form typed into and emptied again has nothing in it")
	}
	v.HandleContentKey(keyPress("esc"))
	if v.CapturingInput() {
		t.Error("esc should close a form with nothing to lose")
	}
}

func TestEscLeavesAnUntouchedReplyWithoutAsking(t *testing.T) {
	v, _ := recipientsTestView(t)
	openThreadComposer(t, v, v.loadReplyContext(100, "Quarterly planning"))
	v.HandleContentKey(keyPress("esc"))
	if v.CapturingInput() {
		t.Error("a reply holding only what HEY prefilled has nothing to lose")
	}
}

func TestEscAsksBeforeLosingAMessage(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openComposer(t, v)
	form.focus = form.bodyIndex()
	_ = form.focusCurrent()
	typeText(v, "Lunch on Friday?")

	v.HandleContentKey(keyPress("esc"))
	if !form.confirmLeave || composeModal(v) == nil {
		t.Fatal("esc on an edited message should ask")
	}
	view := v.View()
	for _, want := range []string{"Close this message?", "save draft", "discard", "keep editing"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question should offer %q:\n%s", want, view)
		}
	}
	if b := v.HelpBindings(); len(b) == 0 || b[0].key != "s" {
		t.Errorf("the help bar should describe the question, got %v", b)
	}

	// Typing is not an answer: the message is left as it was.
	v.HandleContentKey(keyPress("x"))
	if form.body.Value() != "Lunch on Friday?" || !form.confirmLeave {
		t.Errorf("a stray key should neither type nor answer, body %q", form.body.Value())
	}

	v.HandleContentKey(keyPress("esc"))
	if form.confirmLeave || composeModal(v) != form {
		t.Fatal("esc on the question should go back to the message")
	}
	typeText(v, " Noon works.")
	if form.body.Value() != "Lunch on Friday? Noon works." {
		t.Errorf("editing should carry on where it was, body %q", form.body.Value())
	}

	v.HandleContentKey(keyPress("esc"))
	v.HandleContentKey(keyPress("d"))
	if v.CapturingInput() {
		t.Error("d should discard the message")
	}
	if rec.seen().writePath != "" {
		t.Errorf("discarding should write nothing, got %s %s", rec.seen().writeMethod, rec.seen().writePath)
	}
}

func TestSavingADraftOnTheWayOut(t *testing.T) {
	for _, key := range []string{"s", "enter"} {
		t.Run(key, func(t *testing.T) {
			v, rec := recipientsTestView(t)
			form := openComposer(t, v)
			typeText(v, "jan")
			v.HandleContentKey(keyPress("tab"))
			form.inputs[fieldSubject].SetValue("Lunch on Friday")

			v.HandleContentKey(keyPress("esc"))
			cmd := v.HandleContentKey(keyPress(key))
			if !form.sending || !strings.Contains(v.View(), "Saving draft") {
				t.Fatal("saving should hold the form and say so")
			}
			v.HandleContentKey(keyPress("esc"))
			if composeModal(v) != form {
				t.Fatal("a form that is saving holds on to esc")
			}
			saved := runCmd(cmd)
			if _, ok := saved.(draftSavedMsg); !ok {
				t.Fatalf("save = %#v", saved)
			}
			v.Update(saved)
			if v.CapturingInput() {
				t.Error("a saved draft should close the form")
			}

			if rec.seen().writeMethod != "POST" || rec.seen().writePath != "/messages.json" {
				t.Fatalf("draft went to %s %s", rec.seen().writeMethod, rec.seen().writePath)
			}
			entry, _ := rec.seen().writeBody["entry"].(map[string]any)
			message, _ := rec.seen().writeBody["message"].(map[string]any)
			addressed, _ := entry["addressed"].(map[string]any)
			if entry["status"] != "drafted" || message["subject"] != "Lunch on Friday" ||
				fmt.Sprint(addressed["directly"]) != "[Jane Doe <jane@example.com>]" {
				t.Errorf("draft body = %v", rec.seen().writeBody)
			}
		})
	}
}

func TestSavingAReplyDraftFilesItUnderTheEntry(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openThreadComposer(t, v, v.loadReplyContext(100, "Quarterly planning"))
	typeText(v, "Count me in.")
	v.HandleContentKey(keyPress("esc"))
	v.Update(runCmd(v.HandleContentKey(keyPress("s"))))
	if v.CapturingInput() {
		t.Fatal("a saved reply draft should close the form")
	}
	if rec.seen().writePath != "/entries/501/replies.json" {
		t.Fatalf("reply draft went to %s", rec.seen().writePath)
	}
	entry, _ := rec.seen().writeBody["entry"].(map[string]any)
	message, _ := rec.seen().writeBody["message"].(map[string]any)
	if entry["status"] != "drafted" || message["subject"] != "Re: Quarterly planning" ||
		!strings.Contains(fmt.Sprint(message["content"]), "Count me in.") {
		t.Errorf("reply draft body = %v", rec.seen().writeBody)
	}
	_ = form
}

func TestSavingAForwardDraftKeepsTheForwardedMessage(t *testing.T) {
	v, rec := recipientsTestView(t)
	openThreadComposer(t, v, v.loadForwardContext(100, "Quarterly planning"))
	typeText(v, "morty@example.com")
	v.HandleContentKey(keyPress("esc"))
	v.Update(runCmd(v.HandleContentKey(keyPress("s"))))
	if rec.seen().writePath != "/messages.json" {
		t.Fatalf("forward draft went to %s", rec.seen().writePath)
	}
	message, _ := rec.seen().writeBody["message"].(map[string]any)
	if message["subject"] != "Fwd: Quarterly planning" || !strings.Contains(fmt.Sprint(message["content"]), "Quoted message") {
		t.Errorf("forward draft body = %v", rec.seen().writeBody)
	}
}

func TestAFailedDraftSaveKeepsTheMessage(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "rick@example.com")
	rec.configure(func(r *recipientsRecorder) { r.failDrafts = true })
	v.HandleContentKey(keyPress("esc"))
	v.Update(runCmd(v.HandleContentKey(keyPress("s"))))
	if composeModal(v) != form || form.sending || form.confirmLeave {
		t.Fatal("a failed save should leave the message open to edit")
	}
	if !form.isError || !strings.Contains(form.status, "Could not save the draft") {
		t.Errorf("status = %q", form.status)
	}
	if form.inputs[fieldTo].Value() != "rick@example.com" {
		t.Errorf("the message should be as it was, To %q", form.inputs[fieldTo].Value())
	}
}

// A paste is held off exactly as keys are while the close question is up and
// while a draft save is on its way: a paste behind the question would edit a
// form the reader can't see, and one during the save would be lost when the
// saved form closes.
func TestAPasteWaitsForTheQuestionAndTheSave(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openComposer(t, v)
	form.focus = form.bodyIndex()
	_ = form.focusCurrent()
	typeText(v, "Lunch on Friday?")

	v.HandleContentKey(keyPress("esc"))
	v.Update(tea.PasteMsg{Content: " Noon works."})
	if got := form.body.Value(); got != "Lunch on Friday?" {
		t.Errorf("a paste behind the close question changed the message: %q", got)
	}

	cmd := v.HandleContentKey(keyPress("s"))
	if !form.sending {
		t.Fatal("s should start saving the draft")
	}
	v.Update(tea.PasteMsg{Content: " Noon works."})
	if got := form.body.Value(); got != "Lunch on Friday?" {
		t.Errorf("a paste during the save changed the message: %q", got)
	}
	v.Update(runCmd(cmd))
	message, _ := rec.seen().writeBody["message"].(map[string]any)
	if content := fmt.Sprint(message["content"]); !strings.Contains(content, "Lunch on Friday?") || strings.Contains(content, "Noon") {
		t.Errorf("the draft should be the message as it was when saved, got %q", content)
	}
}

func TestADraftWithAnAddressHEYWouldDropIsNotSaved(t *testing.T) {
	v, rec := recipientsTestView(t)
	form := openComposer(t, v)
	typeText(v, "sam@example")
	v.HandleContentKey(keyPress("esc"))
	if cmd := v.HandleContentKey(keyPress("s")); cmd != nil {
		t.Fatal("nothing should be sent")
	}
	if composeModal(v) != form || !form.isError || !strings.Contains(form.status, "sam@example") {
		t.Errorf("the form should stay open naming the address, status %q", form.status)
	}
	if rec.seen().writePath != "" {
		t.Errorf("nothing should have been written, got %s", rec.seen().writePath)
	}
}
