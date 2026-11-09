package repo

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
	"github.com/dustin/go-humanize"
)

type webhooksMsg struct {
	name  string
	hooks []ops.Webhook
	err   error
}

type deliveriesMsg struct {
	name string
	id   int64
	dels []ops.Delivery
	err  error
}

type deliveryMsg struct {
	name string
	id   int64
	del  ops.DeliveryDetail
	err  error
}

type webhookView int

const (
	hooksView webhookView = iota
	deliveriesView
	deliveryView
)

// Webhooks is the tab for a repository's webhooks and their deliveries,
// `repo webhook` over SSH. It needs admin access, as the commands do.
type Webhooks struct {
	common common.Common
	repo   proto.Repository
	access string
	view   webhookView

	hooks  []ops.Webhook
	loaded bool
	list   rowList

	hook    *ops.Webhook
	dels    []ops.Delivery
	delList rowList

	del    *ops.DeliveryDetail
	scroll int

	err   error
	host  form.Host
	flash string
}

// NewWebhooks returns the webhooks tab.
func NewWebhooks(c common.Common) *Webhooks {
	return &Webhooks{common: c}
}

// TabName implements common.TabComponent.
func (w *Webhooks) TabName() string { return "Webhooks" }

// Path implements common.TabComponent. Inside a webhook's deliveries, esc
// comes back here rather than leaving the repository.
func (w *Webhooks) Path() string {
	switch w.view {
	case deliveriesView:
		return "deliveries"
	case deliveryView:
		return "deliveries/delivery"
	}
	return ""
}

// SpinnerID implements common.TabComponent.
func (w *Webhooks) SpinnerID() int { return -1 }

// StatusBarValue implements common.TabComponent.
func (w *Webhooks) StatusBarValue() string {
	if w.hook != nil && w.view != hooksView {
		return w.hook.URL
	}
	return ""
}

// StatusBarInfo implements common.TabComponent.
func (w *Webhooks) StatusBarInfo() string {
	if w.view == hooksView {
		return fmt.Sprintf("%d webhooks", len(w.hooks))
	}
	return fmt.Sprintf("%d deliveries", len(w.dels))
}

// SetSize implements common.Component.
func (w *Webhooks) SetSize(width, height int) { w.common.SetSize(width, height) }

// CapturingInput implements common.InputCapturer.
func (w *Webhooks) CapturingInput() bool { return w.host.Active() }

// Init implements tea.Model.
func (w *Webhooks) Init() tea.Cmd { return nil }

// ShortHelp implements help.KeyMap.
func (w *Webhooks) ShortHelp() []key.Binding {
	if w.host.Active() {
		return w.host.ShortHelp()
	}
	if !isAdmin(w.access) {
		return nil
	}
	switch w.view {
	case deliveriesView:
		b := []key.Binding{binding("esc", "webhooks")}
		if len(w.dels) > 0 {
			b = append(b, w.common.KeyMap.UpDown, binding("enter", "details"), binding("r", "redeliver"))
		}
		return b
	case deliveryView:
		return []key.Binding{binding("esc", "deliveries"), w.common.KeyMap.UpDown, binding("r", "redeliver")}
	}
	b := []key.Binding{binding("a", "add")}
	if len(w.hooks) > 0 {
		b = append(b, w.common.KeyMap.UpDown, binding("enter", "edit"), binding("l", "deliveries"), binding("x", "delete"))
	}
	return b
}

// FullHelp implements help.KeyMap.
func (w *Webhooks) FullHelp() [][]key.Binding {
	return [][]key.Binding{w.ShortHelp()}
}

func (w *Webhooks) loadHooks() tea.Cmd {
	name := w.repo.Name()
	svc, ctx := w.common.Service, w.common.Context()
	return func() tea.Msg {
		hs, err := svc.Webhooks(ctx, name)
		return webhooksMsg{name: name, hooks: hs, err: err}
	}
}

func (w *Webhooks) loadDeliveries(id int64) tea.Cmd {
	name := w.repo.Name()
	svc, ctx := w.common.Service, w.common.Context()
	return func() tea.Msg {
		ds, err := svc.Deliveries(ctx, name, id)
		return deliveriesMsg{name: name, id: id, dels: ds, err: err}
	}
}

func (w *Webhooks) loadDelivery(id int64, did string) tea.Cmd {
	name := w.repo.Name()
	svc, ctx := w.common.Service, w.common.Context()
	return func() tea.Msg {
		d, err := svc.Delivery(ctx, name, id, did)
		return deliveryMsg{name: name, id: id, del: d, err: err}
	}
}

var eventNames = func() []string {
	evs := webhook.Events()
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.String()
	}
	return out
}()

func splitEvents(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

// hookForm is the form for a new webhook, or for changing h.
func (w *Webhooks) hookForm(h *ops.Webhook) *form.Form {
	title, url, events, ct, active := "Add a webhook", "", "push", "json", true
	secretHint := "signs each payload; optional"
	if h != nil {
		title = fmt.Sprintf("Change webhook %d", h.ID)
		url, events, ct, active = h.URL, strings.Join(h.Events, ","), h.ContentType, h.Active
		secretHint = "leave empty to keep the current secret"
	}
	return form.New(w.common, title,
		form.Text("url", "URL", url).Placeholder("https://example.com/hook").
			Hint("may not point at a private or local address"),
		form.Text("events", "Events", events).Hint("comma separated: "+strings.Join(eventNames, ", ")),
		form.Choice("content_type", "Content type", []string{"json", "form"}, ct),
		form.Secret("secret", "Secret").Hint(secretHint),
		form.Toggle("active", "Active", active),
	)
}

func (w *Webhooks) reloadHooks(flash string) func() tea.Cmd {
	return func() tea.Cmd {
		w.flash = flash
		return w.loadHooks()
	}
}

func (w *Webhooks) add() tea.Cmd {
	return w.host.Open(w.hookForm(nil), func(f *form.Form) error {
		active := f.Bool("active")
		_, err := w.common.Service.CreateWebhook(w.common.Context(), w.repo.Name(), ops.WebhookOptions{
			URL:         f.String("url"),
			Events:      splitEvents(f.String("events")),
			ContentType: f.Choice("content_type"),
			Secret:      f.String("secret"),
			Active:      &active,
		})
		return err
	}, w.reloadHooks("Webhook added"))
}

func (w *Webhooks) edit(h ops.Webhook) tea.Cmd {
	return w.host.Open(w.hookForm(&h), func(f *form.Form) error {
		active := f.Bool("active")
		return w.common.Service.UpdateWebhook(w.common.Context(), w.repo.Name(), h.ID, ops.WebhookOptions{
			URL:         f.String("url"),
			Events:      splitEvents(f.String("events")),
			ContentType: f.Choice("content_type"),
			Secret:      f.String("secret"),
			Active:      &active,
		})
	}, w.reloadHooks("Webhook saved"))
}

func (w *Webhooks) remove(h ops.Webhook) tea.Cmd {
	f := form.Confirm(w.common, fmt.Sprintf("Delete webhook %d (%s)?", h.ID, h.URL))
	return w.host.Open(f, func(*form.Form) error {
		return w.common.Service.DeleteWebhook(w.common.Context(), w.repo.Name(), h.ID)
	}, w.reloadHooks("Webhook deleted"))
}

func (w *Webhooks) redeliver(did string) tea.Cmd {
	id := w.hook.ID
	f := form.Confirm(w.common, "Send delivery "+did+" again?")
	return w.host.Open(f, func(*form.Form) error {
		return w.common.Service.Redeliver(w.common.Context(), w.repo.Name(), id, did)
	}, func() tea.Cmd {
		w.flash = "Sent again"
		w.view = deliveriesView
		return w.loadDeliveries(id)
	})
}

func (w *Webhooks) reset() {
	w.access, w.hooks, w.loaded, w.err, w.flash = "", nil, false, nil, ""
	w.view, w.hook, w.dels, w.del = hooksView, nil, nil, nil
	w.list, w.delList = rowList{}, rowList{}
	w.host.Close()
}

// Update implements tea.Model.
func (w *Webhooks) Update(msg tea.Msg) (common.Model, tea.Cmd) {
	if handled, cmd := w.host.Update(msg); handled {
		return w, cmd
	}
	switch msg := msg.(type) {
	case RepoMsg:
		w.repo = msg
		w.reset()
	case InfoMsg:
		if w.repo == nil || msg.Name != w.repo.Name() || msg.Err != nil {
			break
		}
		w.access = msg.Info.Access
		if isAdmin(w.access) {
			return w, w.loadHooks()
		}
	case webhooksMsg:
		if w.repo == nil || msg.name != w.repo.Name() {
			break
		}
		w.loaded, w.hooks, w.err = true, msg.hooks, msg.err
		rows := make([]string, len(w.hooks))
		for i, h := range w.hooks {
			state := "active"
			if !h.Active {
				state = "inactive"
			}
			rows[i] = fmt.Sprintf("#%-4d %-8s %s  [%s] %s", h.ID, state, h.URL, strings.Join(h.Events, ","), h.ContentType)
		}
		w.list.set(rows)
	case deliveriesMsg:
		if w.repo == nil || msg.name != w.repo.Name() || w.hook == nil || msg.id != w.hook.ID {
			break
		}
		w.dels, w.err = msg.dels, msg.err
		rows := make([]string, len(w.dels))
		for i, d := range w.dels {
			mark := "✗"
			if d.OK() {
				mark = "✓"
			}
			rows[i] = fmt.Sprintf("%s %3d  %-20s %s  %s", mark, d.ResponseStatus, d.Event, humanize.Time(d.CreatedAt), d.ID)
		}
		w.delList.set(rows)
	case deliveryMsg:
		if w.repo == nil || msg.name != w.repo.Name() || w.hook == nil || msg.id != w.hook.ID {
			break
		}
		if msg.err != nil {
			w.err = msg.err
			break
		}
		d := msg.del
		w.del, w.scroll, w.view = &d, 0, deliveryView
	case GoBackMsg:
		switch w.view {
		case deliveryView:
			w.view, w.del = deliveriesView, nil
		case deliveriesView:
			w.view, w.hook, w.dels, w.err = hooksView, nil, nil, nil
		}
	case tea.KeyPressMsg:
		if !isAdmin(w.access) || !w.loaded {
			break
		}
		return w, w.key(msg)
	}
	return w, nil
}

func (w *Webhooks) key(msg tea.KeyPressMsg) tea.Cmd {
	switch w.view {
	case hooksView:
		if w.list.update(w.common, msg) {
			w.flash = ""
			return nil
		}
		var sel *ops.Webhook
		if w.list.cursor < len(w.hooks) {
			h := w.hooks[w.list.cursor]
			sel = &h
		}
		switch {
		case msg.String() == "a":
			w.flash = ""
			return w.add()
		case sel == nil:
		case msg.String() == "x":
			w.flash = ""
			return w.remove(*sel)
		case key.Matches(msg, w.common.KeyMap.Select):
			w.flash = ""
			return w.edit(*sel)
		case key.Matches(msg, w.common.KeyMap.SelectItem):
			w.flash, w.err = "", nil
			w.hook, w.view, w.dels, w.delList = sel, deliveriesView, nil, rowList{}
			return w.loadDeliveries(sel.ID)
		}
	case deliveriesView:
		if w.delList.update(w.common, msg) {
			w.flash = ""
			return nil
		}
		if w.delList.cursor >= len(w.dels) {
			return nil
		}
		d := w.dels[w.delList.cursor]
		switch {
		case msg.String() == "r":
			w.flash = ""
			return w.redeliver(d.ID)
		case key.Matches(msg, w.common.KeyMap.Select), key.Matches(msg, w.common.KeyMap.SelectItem):
			w.flash = ""
			return w.loadDelivery(w.hook.ID, d.ID)
		}
	case deliveryView:
		switch {
		case key.Matches(msg, w.common.KeyMap.Up):
			w.scroll = max(0, w.scroll-1)
		case key.Matches(msg, w.common.KeyMap.Down):
			w.scroll++
		case msg.String() == "r" && w.del != nil:
			return w.redeliver(w.del.ID)
		}
	}
	return nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func (w *Webhooks) deliveryText() string {
	d := w.del
	var b strings.Builder
	fmt.Fprintf(&b, "ID: %s\nEvent: %s\nCreated: %s\n", d.ID, d.Event, humanize.Time(d.CreatedAt))
	fmt.Fprintf(&b, "Request: %s %s\n", d.RequestMethod, d.RequestURL)
	if d.RequestError != "" {
		fmt.Fprintf(&b, "Request error: %s\n", d.RequestError)
	}
	fmt.Fprintf(&b, "Request headers:\n%s\nRequest body:\n%s\n", indent(d.RequestHeaders), indent(d.RequestBody))
	fmt.Fprintf(&b, "Response status: %d\nResponse headers:\n%s\nResponse body:\n%s\n", d.ResponseStatus, indent(d.ResponseHeaders), indent(d.ResponseBody))
	return b.String()
}

// View implements tea.Model.
func (w *Webhooks) View() string {
	st := w.common.Styles.Form
	switch {
	case w.host.Active():
		return w.host.View()
	case w.access == "":
		return st.Hint.Render("loading…")
	case !isAdmin(w.access):
		return st.Hint.Render("Webhooks need admin access; you have " + w.access + ".")
	case w.err != nil:
		return st.Error.Render(w.err.Error())
	case !w.loaded:
		return st.Hint.Render("loading…")
	}

	var body string
	switch w.view {
	case hooksView:
		if len(w.hooks) == 0 {
			body = st.Hint.Render("No webhooks yet. Press a to add one.") + "\n"
		} else {
			body = w.list.view(w.common, w.common.Height-2)
		}
	case deliveriesView:
		title := st.Title.Render(fmt.Sprintf("Deliveries of webhook %d", w.hook.ID))
		if len(w.dels) == 0 {
			body = title + "\n" + st.Hint.Render("Nothing delivered yet.") + "\n"
		} else {
			body = title + "\n" + w.delList.view(w.common, w.common.Height-4)
		}
	case deliveryView:
		lines := strings.Split(w.deliveryText(), "\n")
		h := max(1, w.common.Height-2)
		w.scroll = min(w.scroll, max(0, len(lines)-h))
		end := min(len(lines), w.scroll+h)
		body = strings.Join(lines[w.scroll:end], "\n")
	}
	if w.flash != "" {
		body += "\n" + st.Success.Render(w.flash)
	}
	return body
}
