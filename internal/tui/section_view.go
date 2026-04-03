package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// viewContext holds shared dependencies injected into every sectionView.
type viewContext struct {
	sdk    *hey.Client
	ctx    context.Context
	styles styles
	width  int
	height int // content area height
}

// sectionView is the interface every top-level section must implement.
type sectionView interface {
	// Init returns the command to run when this section becomes active.
	Init() tea.Cmd

	// Update handles messages relevant to this section.
	// Returns the command and whether the message was consumed.
	Update(msg tea.Msg) (tea.Cmd, bool)

	// View renders the content area.
	View() string

	// HelpBindings returns section-specific help bindings for rowContent.
	HelpBindings() []helpBinding

	// SubnavItems returns the subnav state for header rendering.
	SubnavItems() (items []navItem, selected int, label string, centered bool)

	// SubnavLeft / SubnavRight handle subnav row navigation.
	SubnavLeft() tea.Cmd
	SubnavRight() tea.Cmd

	// HandleContentKey handles key presses when focus is on rowContent.
	HandleContentKey(msg tea.KeyPressMsg) tea.Cmd

	// InThread reports whether a detail/thread view is open.
	InThread() bool

	// ExitThread closes the detail/thread view.
	ExitThread()

	// Resize updates layout for new terminal dimensions.
	Resize(width, height int)

	// Loading reports whether the section is waiting on data.
	Loading() bool
}
