package form

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
)

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func typeText(f *Form, s string) {
	for _, r := range s {
		f.Update(press(string(r)))
	}
}

func testCommon() common.Common {
	return common.NewCommon(context.Background(), 80, 24)
}

func TestFormFields(t *testing.T) {
	f := New(testCommon(), "Import",
		Text("name", "Name", ""),
		Choice("mode", "Keep", []string{"fork", "mirror"}, "fork"),
		Toggle("auto", "Auto sync", true).When(func(f *Form) bool { return f.Choice("mode") == "mirror" }),
		Toggle("private", "Private", false),
	)
	f.Init()

	typeText(f, "repo")
	f.Update(press("tab"))
	f.Update(press("space")) // fork -> mirror
	f.Update(press("tab"))   // auto sync now shows
	f.Update(press("space")) // off
	f.Update(press("tab"))
	f.Update(press("space")) // private on

	if got := f.String("name"); got != "repo" {
		t.Errorf("name = %q", got)
	}
	if f.Choice("mode") != "mirror" || f.Bool("auto") || !f.Bool("private") {
		t.Errorf("mode=%q auto=%v private=%v", f.Choice("mode"), f.Bool("auto"), f.Bool("private"))
	}
	if !strings.Contains(f.View(), "Auto sync") {
		t.Error("auto sync hidden for a mirror")
	}

	// Back to fork: the mirror-only field is skipped and hidden.
	f.Update(press("tab")) // wraps to name
	f.Update(press("tab"))
	f.Update(press("left"))
	if f.Choice("mode") != "fork" || strings.Contains(f.View(), "Auto sync") {
		t.Error("auto sync shown for a fork")
	}

	// Typing letters that are keys elsewhere goes into the text field.
	f.Update(press("tab"))
	f.Update(press("tab")) // name again (auto sync skipped)
	typeText(f, "qx")
	if got := f.String("name"); got != "repoqx" {
		t.Errorf("name = %q", got)
	}

	f.Update(press("enter"))
	if !f.Submitted() {
		t.Error("enter did not submit")
	}
}

func TestConfirm(t *testing.T) {
	f := Confirm(testCommon(), "Delete?")
	f.Init()
	f.Update(press("n"))
	if !f.Cancelled() || f.Submitted() {
		t.Error("n did not cancel")
	}
	f = Confirm(testCommon(), "Delete?")
	f.Update(press("y"))
	if !f.Submitted() {
		t.Error("y did not confirm")
	}
}

func TestHost(t *testing.T) {
	var h Host
	calls := 0
	doneRan := false
	h.Open(New(testCommon(), "New", Text("name", "Name", "")), func(f *Form) error {
		calls++
		if f.String("name") == "bad" {
			return errors.New("name is bad")
		}
		return nil
	}, func() tea.Cmd { doneRan = true; return nil })

	for _, r := range "bad" {
		h.Update(press(string(r)))
	}
	_, cmd := h.Update(press("enter"))
	if cmd == nil || !h.Busy() {
		t.Fatal("submitting did not start the action")
	}
	// Keys are ignored while it runs.
	h.Update(press("x"))
	h.Update(cmd())
	if !h.Active() || !strings.Contains(h.View(), "name is bad") {
		t.Fatalf("failure did not stay open with the error: %q", h.View())
	}

	// Fix it and send again.
	for range 3 {
		h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "good" {
		h.Update(press(string(r)))
	}
	_, cmd = h.Update(press("enter"))
	h.Update(cmd())
	if h.Active() || !doneRan || calls != 2 {
		t.Errorf("active=%v done=%v calls=%d", h.Active(), doneRan, calls)
	}

	// Without a form, the error is kept for the caller.
	cmd = h.Run(func() error { return errors.New("nope") }, nil)
	h.Update(cmd())
	if h.Err() == nil || h.Busy() {
		t.Errorf("run error = %v, busy = %v", h.Err(), h.Busy())
	}

	// A stale result is not taken.
	if handled, _ := h.Update(resultMsg{id: -1}); handled {
		t.Error("took a result for another action")
	}
}
