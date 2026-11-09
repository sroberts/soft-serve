package form

import (
	"sync/atomic"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

var nextID atomic.Int64

// resultMsg is the outcome of the action a Host ran.
type resultMsg struct {
	id  int64
	err error
}

// Host shows at most one form at a time and, when it is sent, runs the
// action it is for off the UI loop. A failed action leaves the form open
// with the error, so it can be fixed and sent again; a successful one closes
// it and runs done.
//
// An action can also run without a form, as for a toggle; then its error is
// kept for the caller to show.
type Host struct {
	form   *Form
	action func(*Form) error
	done   func() tea.Cmd
	id     int64
	busy   bool
	err    error
}

// Open shows f, to run action when it is sent and then done.
func (h *Host) Open(f *Form, action func(*Form) error, done func() tea.Cmd) tea.Cmd {
	h.form, h.action, h.done, h.err = f, action, done, nil
	return f.Init()
}

// Run runs action without a form, and done if it succeeds.
func (h *Host) Run(action func() error, done func() tea.Cmd) tea.Cmd {
	h.form, h.done, h.err = nil, done, nil
	h.busy = true
	h.id = nextID.Add(1)
	id := h.id
	return func() tea.Msg { return resultMsg{id: id, err: action()} }
}

// Active reports whether a form is showing.
func (h *Host) Active() bool { return h.form != nil }

// Busy reports whether an action is running.
func (h *Host) Busy() bool { return h.busy }

// Err is the error of the last action run without a form.
func (h *Host) Err() error { return h.err }

// ClearErr forgets it.
func (h *Host) ClearErr() { h.err = nil }

// Close dismisses the form.
func (h *Host) Close() { h.form = nil }

// Update takes msg if it is for the host: a key while a form is showing, or
// the outcome of an action. It reports whether it did.
func (h *Host) Update(msg tea.Msg) (bool, tea.Cmd) {
	if r, ok := msg.(resultMsg); ok {
		if r.id != h.id {
			return false, nil
		}
		h.busy = false
		if r.err != nil {
			if h.form != nil {
				h.form.Fail(r.err)
			} else {
				h.err = r.err
			}
			return true, nil
		}
		h.form = nil
		if h.done != nil {
			return true, h.done()
		}
		return true, nil
	}

	if h.form == nil {
		return false, nil
	}
	cmd := h.form.Update(msg)
	switch {
	case h.form.Cancelled():
		h.form = nil
	case h.form.Submitted() && !h.busy:
		h.form.Busy()
		h.busy = true
		h.id = nextID.Add(1)
		id, f, action := h.id, h.form, h.action
		return true, func() tea.Msg { return resultMsg{id: id, err: action(f)} }
	}
	_, isKey := msg.(tea.KeyPressMsg)
	return isKey, cmd
}

// View renders the form, if one is showing.
func (h *Host) View() string {
	if h.form == nil {
		return ""
	}
	return h.form.View()
}

// ShortHelp is the showing form's keys.
func (h *Host) ShortHelp() []key.Binding {
	if h.form == nil {
		return nil
	}
	return h.form.ShortHelp()
}
