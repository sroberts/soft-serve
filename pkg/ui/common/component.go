package common

import (
	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
)

// Model represents a simple UI model.
type Model interface {
	Init() tea.Cmd
	Update(tea.Msg) (Model, tea.Cmd)
	View() string
}

// Component represents a Bubble Tea model that implements a SetSize function.
type Component interface {
	Model
	help.KeyMap
	SetSize(width, height int)
}

// TabComponenet represents a model that is mounted to a tab.
// TODO: find a better name
type TabComponent interface {
	Component

	// StatusBarValue returns the status bar value component.
	StatusBarValue() string

	// StatusBarInfo returns the status bar info component.
	StatusBarInfo() string

	// SpinnerID returns the ID of the spinner.
	SpinnerID() int

	// TabName returns the name of the tab.
	TabName() string

	// Path returns the hierarchical path of the tab.
	Path() string
}

// InputCapturer is a component that may be taking typed input, such as a
// form. While it is, keys that mean something everywhere else (quit, help,
// switching tabs, going back) are left to it.
type InputCapturer interface {
	CapturingInput() bool
}

// IsCapturingInput reports whether m is taking typed input.
func IsCapturingInput(m any) bool {
	ic, ok := m.(InputCapturer)
	return ok && ic.CapturingInput()
}

// OpenRepoMsg asks the UI to open the named repository, or open it again to
// show changes to it.
type OpenRepoMsg string

// HomeMsg asks the UI to go back to the repository list and reload it.
type HomeMsg struct{}
