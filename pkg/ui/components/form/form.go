// Package form is a small form for the TUI: text fields, on/off toggles and
// choices, or a yes/no confirmation, and a Host that runs what the form is
// for off the UI loop and shows the outcome in the form.
package form

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/x/ansi"
)

type kind int

const (
	textKind kind = iota
	toggleKind
	choiceKind
)

// Field is one field of a form.
type Field struct {
	key, label, hint string
	kind             kind
	input            textinput.Model
	on               bool
	options          []string
	choice           int
	visible          func(*Form) bool
}

// Text is a text field.
func Text(key, label, value string) *Field {
	in := textinput.New()
	in.Prompt = ""
	in.SetValue(value)
	in.CursorEnd()
	return &Field{key: key, label: label, kind: textKind, input: in}
}

// Secret is a text field whose value is not shown.
func Secret(key, label string) *Field {
	f := Text(key, label, "")
	f.input.EchoMode = textinput.EchoPassword
	return f
}

// Toggle is an on/off field.
func Toggle(key, label string, on bool) *Field {
	return &Field{key: key, label: label, kind: toggleKind, on: on}
}

// Choice is a field that takes one of options, starting at selected.
func Choice(key, label string, options []string, selected string) *Field {
	f := &Field{key: key, label: label, kind: choiceKind, options: options}
	for i, o := range options {
		if o == selected {
			f.choice = i
		}
	}
	return f
}

// Hint adds a line of explanation under the field.
func (f *Field) Hint(h string) *Field {
	f.hint = h
	return f
}

// Placeholder sets what an empty text field shows.
func (f *Field) Placeholder(p string) *Field {
	f.input.Placeholder = p
	return f
}

// When shows the field only while fn holds, as for a setting that only
// applies to some choice of another field.
func (f *Field) When(fn func(*Form) bool) *Field {
	f.visible = fn
	return f
}

// Form is a list of fields with a title, or with no fields, a confirmation.
type Form struct {
	common    common.Common
	title     string
	fields    []*Field
	focus     int
	confirm   bool
	submitted bool
	cancelled bool
	busy      bool
	err       error
}

// New returns a form.
func New(c common.Common, title string, fields ...*Field) *Form {
	return &Form{common: c, title: title, fields: fields}
}

// Confirm returns a yes/no question; y or enter says yes.
func Confirm(c common.Common, question string) *Form {
	return &Form{common: c, title: question, confirm: true}
}

// Init focuses the first field.
func (f *Form) Init() tea.Cmd {
	f.focus = -1
	return f.move(1)
}

func (f *Form) shown(i int) bool {
	fl := f.fields[i]
	return fl.visible == nil || fl.visible(f)
}

// move focuses the next shown field in direction d, wrapping around.
func (f *Form) move(d int) tea.Cmd {
	n := len(f.fields)
	if n == 0 {
		return nil
	}
	if f.focus >= 0 && f.focus < n && f.fields[f.focus].kind == textKind {
		f.fields[f.focus].input.Blur()
	}
	i := f.focus
	for range n {
		i = (i + d + n) % n
		if f.shown(i) {
			break
		}
	}
	f.focus = i
	if f.fields[i].kind == textKind {
		return f.fields[i].input.Focus()
	}
	return nil
}

// Submitted reports whether the form was sent and is waiting to be carried
// out.
func (f *Form) Submitted() bool { return f.submitted }

// Cancelled reports whether the form was dismissed.
func (f *Form) Cancelled() bool { return f.cancelled }

// Fail shows err and lets the form be changed and sent again.
func (f *Form) Fail(err error) {
	f.err = err
	f.submitted = false
	f.busy = false
}

// Busy marks the form as being carried out.
func (f *Form) Busy() { f.busy = true }

// Update handles a key or other message.
func (f *Form) Update(msg tea.Msg) tea.Cmd {
	if f.busy {
		return nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if f.focus >= 0 && f.focus < len(f.fields) && f.fields[f.focus].kind == textKind {
			var cmd tea.Cmd
			f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
			return cmd
		}
		return nil
	}

	if f.confirm {
		switch strings.ToLower(k.String()) {
		case "y", "enter":
			f.submitted, f.err = true, nil
		case "n", "esc", "q":
			f.cancelled = true
		}
		return nil
	}

	switch k.String() {
	case "esc":
		f.cancelled = true
		return nil
	case "enter":
		f.submitted, f.err = true, nil
		return nil
	case "tab", "down":
		return f.move(1)
	case "shift+tab", "up":
		return f.move(-1)
	}

	if f.focus < 0 || f.focus >= len(f.fields) {
		return nil
	}
	fl := f.fields[f.focus]
	switch fl.kind {
	case toggleKind:
		switch k.String() {
		case "space", " ", "left", "right", "h", "l", "x":
			fl.on = !fl.on
		case "y":
			fl.on = true
		case "n":
			fl.on = false
		}
	case choiceKind:
		switch k.String() {
		case "left", "h":
			fl.choice = (fl.choice - 1 + len(fl.options)) % len(fl.options)
		case "right", "l", "space", " ":
			fl.choice = (fl.choice + 1) % len(fl.options)
		}
	case textKind:
		var cmd tea.Cmd
		fl.input, cmd = fl.input.Update(msg)
		return cmd
	}
	return nil
}

func (f *Form) field(key string) *Field {
	for _, fl := range f.fields {
		if fl.key == key {
			return fl
		}
	}
	return nil
}

// String is a text field's value, trimmed.
func (f *Form) String(key string) string {
	if fl := f.field(key); fl != nil {
		return strings.TrimSpace(fl.input.Value())
	}
	return ""
}

// Bool is a toggle's value.
func (f *Form) Bool(key string) bool {
	if fl := f.field(key); fl != nil {
		return fl.on
	}
	return false
}

// Choice is a choice field's value.
func (f *Form) Choice(key string) string {
	if fl := f.field(key); fl != nil && len(fl.options) > 0 {
		return fl.options[fl.choice]
	}
	return ""
}

// ShortHelp is the keys the form takes.
func (f *Form) ShortHelp() []key.Binding {
	if f.confirm {
		return []key.Binding{
			key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n/esc", "no")),
		}
	}
	b := []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
	if len(f.fields) > 1 {
		b = append(b, key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab/↑↓", "next field")))
	}
	if f.focus >= 0 && f.focus < len(f.fields) {
		switch f.fields[f.focus].kind {
		case toggleKind:
			b = append(b, key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle")))
		case choiceKind:
			b = append(b, key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "choose")))
		}
	}
	return b
}

// View renders the form.
func (f *Form) View() string {
	st := f.common.Styles.Form
	var b strings.Builder
	b.WriteString(st.Title.Render(f.title))
	b.WriteString("\n")

	width := 0
	for i, fl := range f.fields {
		if f.shown(i) && lipgloss.Width(fl.label) > width {
			width = lipgloss.Width(fl.label)
		}
	}
	for i, fl := range f.fields {
		if !f.shown(i) {
			continue
		}
		focused := i == f.focus
		label, value := st.Label, st.Value
		marker := "  "
		if focused {
			label, value = st.LabelFocused, st.ValueFocused
			marker = "> "
		}
		var v string
		switch fl.kind {
		case textKind:
			// Only on a change: a sent form's action reads the fields
			// off the UI loop while the form is still drawn.
			if w := max(10, f.common.Width-width-8); fl.input.Width() != w {
				fl.input.SetWidth(w)
			}
			v = fl.input.View()
		case toggleKind:
			v = "[ ] no"
			if fl.on {
				v = "[x] yes"
			}
			v = value.Render(v)
		case choiceKind:
			opts := make([]string, len(fl.options))
			for j, o := range fl.options {
				if j == fl.choice {
					opts[j] = value.Render("‹" + o + "›")
				} else {
					opts[j] = st.Hint.Render(o)
				}
			}
			v = strings.Join(opts, " ")
		}
		b.WriteString(marker + label.Render(padRight(fl.label, width)) + "  " + v + "\n")
		if fl.hint != "" && focused {
			b.WriteString(strings.Repeat(" ", width+4) + st.Hint.Render(fl.hint) + "\n")
		}
	}

	switch {
	case f.busy:
		b.WriteString("\n" + st.Hint.Render("working…"))
	case f.err != nil:
		// Errors can be long, such as one naming the repositories a user
		// owns, so they wrap at the screen's width.
		msg := ansi.Wordwrap(f.err.Error(), max(20, f.common.Width-2), " ,")
		b.WriteString("\n" + st.Error.Render(msg))
	case f.confirm:
		b.WriteString("\n" + st.Hint.Render("y to confirm, n or esc to cancel"))
	}
	return b.String()
}

func padRight(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
