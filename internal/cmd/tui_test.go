package cmd

import "testing"

func TestHeyAliasIsHiddenTuiCommand(t *testing.T) {
	root := newRootCmd()

	alias, _, err := root.Find([]string{"hey"})
	if err != nil {
		t.Fatal(err)
	}
	if alias.Name() != "hey" {
		t.Fatalf("expected the hey alias, got %q", alias.CommandPath())
	}
	if !alias.Hidden {
		t.Error("expected the hey alias to be hidden")
	}

	tui, _, err := root.Find([]string{"tui"})
	if err != nil {
		t.Fatal(err)
	}
	if tui.Hidden {
		t.Error("expected tui to be listed")
	}
	if alias.Short != tui.Short {
		t.Errorf("alias describes itself as %q, tui as %q", alias.Short, tui.Short)
	}
}
