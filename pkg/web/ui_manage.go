package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
)

// Managing repositories from the web pages: the SSH repo commands as plain
// HTML forms. Every change goes through the caller's ops.Service, so the
// pages obey the same rules as SSH, the REST API and the TUI; the pages
// only decide which controls to show. Changes are POSTs that redirect back
// to a page (Post/Redirect/Get) carrying a ?done= code for its notice; a
// change that fails renders its form again with the reason. The server
// wraps these pages in cross-origin protection, so another site cannot
// post them with the caller's credentials.
//
//	/-/new                                     create or import a repository
//	/{repo}/-/settings                         change settings
//	/{repo}/-/rename                           rename (POST)
//	/{repo}/-/sync                             sync a mirror now (POST)
//	/{repo}/-/delete                           delete, after confirming
//	/{repo}/-/refs/delete?branch=|tag=         delete a branch or tag, after confirming
//	/{repo}/-/access/add                       add a collaborator (POST)
//	/{repo}/-/access/remove                    remove one (POST)
//	/{repo}/-/webhooks                         list and add webhooks
//	/{repo}/-/webhooks/{id}                    change one, and its deliveries
//	/{repo}/-/webhooks/{id}/delete             remove one, after confirming
//	/{repo}/-/webhooks/{id}/deliveries/{did}   one delivery
//	/{repo}/-/webhooks/{id}/deliveries/{did}/redeliver   send it again (POST)

// maxFormBody bounds a submitted form; the largest is a webhook.
const maxFormBody = 64 << 10

// notices are what a page says after a change, by the code the redirect to
// it carries. Only these fixed texts are ever shown, so a link cannot make
// a page say something of its own.
var notices = map[string]string{
	"created":        "Repository created.",
	"imported":       "Repository imported.",
	"saved":          "Settings saved.",
	"renamed":        "Repository renamed.",
	"deleted":        "Repository deleted.",
	"synced":         "Mirror synced from its upstream.",
	"branch-deleted": "Branch deleted.",
	"tag-deleted":    "Tag deleted.",
	"collab-added":   "Collaborator added.",
	"collab-removed": "Collaborator removed.",
	"hook-created":   "Webhook added.",
	"hook-saved":     "Webhook saved.",
	"hook-deleted":   "Webhook removed.",
	"redelivered":    "Delivery sent again.",

	"key-added":        "SSH key added.",
	"key-removed":      "SSH key removed.",
	"token-deleted":    "Access token deleted.",
	"username-changed": "Username changed.",
}

// withDone adds a notice code to a page's URL.
func withDone(u, code string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "done=" + url.QueryEscape(code)
}

// formRoutes are the repository actions that take a POST.
func isFormAction(action string) bool {
	switch action {
	case "settings", "rename", "sync", "delete", "refs/delete", "access/add", "access/remove", "webhooks":
		return true
	}
	return strings.HasPrefix(action, "webhooks/")
}

// canWrite reports whether an access level lets its holder change a
// repository's settings.
func canWrite(level string) bool {
	return level == access.ReadWriteAccess.String() || level == access.AdminAccess.String()
}

// service is the caller's ops.Service.
func (b *browser) service() *ops.Local {
	return ops.NewLocal(b.be, ops.UserActor(b.user))
}

// readForm parses a submitted form, bounded in size.
func readForm(w http.ResponseWriter, r *http.Request) (url.Values, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		return nil, ops.Invalid("the form could not be read")
	}
	return r.PostForm, nil
}

func checked(f url.Values, name string) bool { return f.Get(name) == "on" }

// opsMessage is what a page tells the caller about an error from the
// service, and the status to answer with.
func (h uiHandler) opsMessage(r *http.Request, b *browser, err error) (int, string) {
	status := opsStatus(err, b.user == nil)
	switch {
	case status == http.StatusInternalServerError:
		log.FromContext(r.Context()).Error("web", "method", r.Method, "path", r.URL.Path, "err", err)
		return status, "Something went wrong on the server."
	case errors.Is(err, proto.ErrUnauthorized) && b.user == nil:
		return status, "Sign in to do that."
	case errors.Is(err, proto.ErrUnauthorized):
		return status, "You don't have permission to do that."
	case status == http.StatusNotFound && !errors.Is(err, proto.ErrUserNotFound):
		return status, "There's nothing here, or you don't have access to it."
	case errors.Is(err, proto.ErrImportFailed):
		return status, "The import failed. Check the remote's address and that this server can reach it."
	case errors.Is(err, proto.ErrMirrorSyncFailed):
		return status, "The sync failed. The upstream may be unreachable."
	}
	return status, sentence(err.Error())
}

// sentence makes an error message read as one: a capital and a full stop.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// opsFail answers a change that failed with an error page, for changes
// with no form of their own to show again.
func (h uiHandler) opsFail(w http.ResponseWriter, r *http.Request, b *browser, p page, err error) {
	status, msg := h.opsMessage(r, b, err)
	if status == http.StatusUnauthorized && h.verifier == nil {
		// Without Outpost's gateway, the browser can sign in with a
		// username and access token.
		w.Header().Set("WWW-Authenticate", `Basic realm="Soft Serve" charset="UTF-8"`)
	}
	h.renderError(w, r, &p, status, msg)
}

// renderForm renders a form page again with the reason a change failed.
func (h uiHandler) renderForm(w http.ResponseWriter, r *http.Request, b *browser, p page, name string, err error, setErr func(string)) {
	status, msg := h.opsMessage(r, b, err)
	if status == http.StatusUnauthorized && h.verifier == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Soft Serve" charset="UTF-8"`)
	}
	setErr(msg)
	h.renderStatus(w, r, status, name, p)
}

// --- New repository ---

type newRepoData struct {
	Name, ProjectName, Description string
	Private, Hidden                bool
	// Source is "", "fork" or "mirror".
	Source   string
	Remote   string
	AutoSync bool
	LFS      bool
	Err      string
}

func (h uiHandler) newRepo(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Title = "New repository"
	d := newRepoData{AutoSync: true}
	if r.Method != http.MethodPost {
		p.Data = &d
		h.render(w, r, "newrepo", p)
		return
	}

	f, err := readForm(w, r)
	if err == nil {
		d = newRepoData{
			Name:        strings.TrimSpace(f.Get("name")),
			ProjectName: f.Get("project_name"),
			Description: f.Get("description"),
			Private:     checked(f, "private"),
			Hidden:      checked(f, "hidden"),
			Source:      f.Get("source"),
			Remote:      strings.TrimSpace(f.Get("remote")),
			AutoSync:    checked(f, "auto_sync"),
			LFS:         checked(f, "lfs"),
		}
		opts := ops.CreateRepo{
			Name:        d.Name,
			ProjectName: d.ProjectName,
			Description: d.Description,
			Private:     d.Private,
			Hidden:      d.Hidden,
		}
		switch d.Source {
		case "":
		case "fork", "mirror":
			if d.Remote == "" {
				err = ops.Invalid("give the address to import from")
				break
			}
			opts.Remote, opts.LFS = d.Remote, d.LFS
			if d.Source == "mirror" {
				opts.Mirror = true
				opts.AutoSync = &d.AutoSync
			}
		default:
			err = ops.Invalid("choose to start empty, fork or mirror")
		}
		if err == nil {
			var repo ops.Repo
			if repo, err = b.service().CreateRepo(r.Context(), opts); err == nil {
				done := "created"
				if opts.Remote != "" {
					done = "imported"
				}
				http.Redirect(w, r, withDone(repoURL(repo.Name, "", "", ""), done), http.StatusSeeOther)
				return
			}
		}
	}
	p.Data = &d
	h.renderForm(w, r, b, p, "newrepo", err, func(m string) { d.Err = m })
}

// --- Settings ---

type settingsData struct {
	Repo ops.Repo
	Err  string
}

// manageRepo is the caller's view of the repository being managed, refused
// unless their access lets them change it.
func (h uiHandler) manageRepo(w http.ResponseWriter, r *http.Request, b *browser, p page, need access.AccessLevel) (ops.Repo, bool) {
	repo, err := b.service().Repo(r.Context(), p.Repo.Name)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return ops.Repo{}, false
	}
	if access.ParseAccessLevel(repo.Access) < need {
		h.opsFail(w, r, b, p, proto.ErrUnauthorized)
		return ops.Repo{}, false
	}
	return repo, true
}

func (h uiHandler) settings(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "settings"
	p.Title = "Settings · " + p.Repo.Name
	repo, ok := h.manageRepo(w, r, b, p, access.ReadWriteAccess)
	if !ok {
		return
	}
	d := &settingsData{Repo: repo}
	p.Data = d
	if r.Method != http.MethodPost {
		h.render(w, r, "settings", p)
		return
	}

	f, err := readForm(w, r)
	if err == nil {
		// Only what changed is sent, so nothing announces a change that
		// did not happen.
		var u ops.RepoUpdate
		if v := f.Get("description"); v != repo.Description {
			u.Description = &v
		}
		if v := f.Get("project_name"); v != repo.ProjectName {
			u.ProjectName = &v
		}
		if v := checked(f, "private"); v != repo.Private {
			u.Private = &v
		}
		if v := checked(f, "hidden"); v != repo.Hidden {
			u.Hidden = &v
		}
		if repo.Mirror {
			if v := checked(f, "mirror"); !v {
				u.Mirror = &v
			} else if repo.AutoSync != nil {
				if v := checked(f, "auto_sync"); v != *repo.AutoSync {
					u.AutoSync = &v
				}
			}
		}
		if v := f.Get("default_branch"); v != "" && v != repo.DefaultBranch {
			u.DefaultBranch = &v
		}
		if err = b.service().UpdateRepo(r.Context(), repo.Name, u); err == nil {
			http.Redirect(w, r, withDone(repoURL(repo.Name, "settings", "", ""), "saved"), http.StatusSeeOther)
			return
		}
	}
	// Show what is there now, which may be part of the change.
	if now, rerr := b.service().Repo(r.Context(), repo.Name); rerr == nil {
		d.Repo = now
	}
	h.renderForm(w, r, b, p, "settings", err, func(m string) { d.Err = m })
}

func (h uiHandler) rename(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, repoURL(p.Repo.Name, "settings", "", ""), http.StatusSeeOther)
		return
	}
	f, err := readForm(w, r)
	if err == nil {
		newName := strings.TrimSpace(f.Get("name"))
		if err = b.service().RenameRepo(r.Context(), p.Repo.Name, newName); err == nil {
			repo, rerr := b.service().Repo(r.Context(), newName)
			target := "/"
			if rerr == nil {
				target = repoURL(repo.Name, "settings", "", "")
			}
			http.Redirect(w, r, withDone(target, "renamed"), http.StatusSeeOther)
			return
		}
	}
	h.settingsFailed(w, r, b, p, err)
}

// settingsFailed shows the settings page with why a change made from it
// failed.
func (h uiHandler) settingsFailed(w http.ResponseWriter, r *http.Request, b *browser, p page, err error) {
	p.Tab = "settings"
	p.Title = "Settings · " + p.Repo.Name
	repo, rerr := b.service().Repo(r.Context(), p.Repo.Name)
	if rerr != nil || !canWrite(repo.Access) {
		h.opsFail(w, r, b, p, err)
		return
	}
	d := &settingsData{Repo: repo}
	p.Data = d
	h.renderForm(w, r, b, p, "settings", err, func(m string) { d.Err = m })
}

func (h uiHandler) syncMirror(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, repoURL(p.Repo.Name, "settings", "", ""), http.StatusSeeOther)
		return
	}
	if err := b.service().SyncMirror(r.Context(), p.Repo.Name); err != nil {
		h.settingsFailed(w, r, b, p, err)
		return
	}
	back := repoURL(p.Repo.Name, "settings", "", "")
	if r.PostFormValue("back") == "summary" {
		back = repoURL(p.Repo.Name, "", "", "")
	}
	http.Redirect(w, r, withDone(back, "synced"), http.StatusSeeOther)
}

// confirmData is a page that asks before something is removed.
type confirmData struct {
	Heading string
	// Message says what will happen; What is the thing, set in code.
	Message, What string
	Action        string
	Fields        map[string]string
	Button        string
	Cancel        string
}

func (h uiHandler) deleteRepo(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	if r.Method != http.MethodPost {
		if _, ok := h.manageRepo(w, r, b, p, access.ReadWriteAccess); !ok {
			return
		}
		p.Tab = "settings"
		p.Title = "Delete " + p.Repo.Name
		p.Data = confirmData{
			Heading: "Delete this repository?",
			Message: "Its history, branches, tags, collaborators and webhooks go with it. This can't be undone.",
			What:    p.Repo.Name,
			Action:  repoURL(p.Repo.Name, "delete", "", ""),
			Button:  "Delete repository",
			Cancel:  repoURL(p.Repo.Name, "settings", "", ""),
		}
		h.render(w, r, "confirm", p)
		return
	}
	if err := b.service().DeleteRepo(r.Context(), p.Repo.Name); err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	http.Redirect(w, r, withDone("/", "deleted"), http.StatusSeeOther)
}

// --- Branches and tags ---

func (h uiHandler) deleteRef(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "refs"
	src := r.URL.Query()
	if r.Method == http.MethodPost {
		f, err := readForm(w, r)
		if err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		src = f
	}
	kind, name := "branch", src.Get("branch")
	if name == "" {
		kind, name = "tag", src.Get("tag")
	}
	if name == "" {
		h.opsFail(w, r, b, p, ops.Invalid("name the branch or tag to delete"))
		return
	}

	if r.Method != http.MethodPost {
		if _, ok := h.manageRepo(w, r, b, p, access.ReadWriteAccess); !ok {
			return
		}
		p.Title = "Delete " + kind + " " + name
		msg := "The tag goes, but not the commits it points to."
		if kind == "branch" {
			msg = "Commits on it that no other branch or tag has will no longer be reachable."
		}
		p.Data = confirmData{
			Heading: "Delete this " + kind + "?",
			Message: msg,
			What:    name,
			Action:  repoURL(p.Repo.Name, "refs/delete", "", ""),
			Fields:  map[string]string{kind: name},
			Button:  "Delete " + kind,
			Cancel:  repoURL(p.Repo.Name, "refs", "", ""),
		}
		h.render(w, r, "confirm", p)
		return
	}

	var err error
	if kind == "branch" {
		err = b.service().DeleteBranch(r.Context(), p.Repo.Name, name)
	} else {
		err = b.service().DeleteTag(r.Context(), p.Repo.Name, name)
	}
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "refs", "", ""), kind+"-deleted"), http.StatusSeeOther)
}

// --- Collaborators ---

// accessLevels are the levels a collaborator can be given, weakest first.
var accessLevels = []string{
	access.ReadOnlyAccess.String(),
	access.ReadWriteAccess.String(),
	access.AdminAccess.String(),
	access.NoAccess.String(),
}

func (h uiHandler) collab(w http.ResponseWriter, r *http.Request, b *browser, p page, op string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, repoURL(p.Repo.Name, "access", "", ""), http.StatusSeeOther)
		return
	}
	f, err := readForm(w, r)
	username := strings.TrimSpace(f.Get("username"))
	if err == nil {
		done := "collab-added"
		if op == "remove" {
			done = "collab-removed"
			err = b.service().RemoveCollaborator(r.Context(), p.Repo.Name, username)
		} else {
			err = b.service().AddCollaborator(r.Context(), p.Repo.Name, username, f.Get("access"))
		}
		if err == nil {
			http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "access", "", ""), done), http.StatusSeeOther)
			return
		}
	}

	p.Tab = "access"
	p.Title = "Access · " + p.Repo.Name
	cs, cerr := b.collaborators(r.Context(), p.Repo.Name)
	if cerr != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	d := &accessData{Collaborators: cs, Username: username, Level: f.Get("access")}
	p.Data = d
	h.renderForm(w, r, b, p, "access", err, func(m string) { d.Err = m })
}

// --- Webhooks ---

type webhooksData struct {
	Hooks  []ops.Webhook
	Events []string
	Form   webhookForm
	Err    string
}

// webhookForm is a webhook as the form shows it.
type webhookForm struct {
	URL         string
	Events      []string
	ContentType string
	Active      bool
}

func (f webhookForm) Has(event string) bool { return slices.Contains(f.Events, event) }

// hookFormArgs is what the shared webhook fields template gets: the form,
// the events to offer, and the webhook's ID, 0 for a new one.
func hookFormArgs(f webhookForm, events []string, id int64) any {
	return struct {
		Form   webhookForm
		Events []string
		Hook   struct{ ID int64 }
	}{Form: f, Events: events, Hook: struct{ ID int64 }{ID: id}}
}

// deleteRefURL is the page that confirms deleting a branch or tag.
func deleteRefURL(name, kind, ref string) string {
	return "/" + name + "/-/refs/delete?" + url.Values{kind: {ref}}.Encode()
}

func eventNames() []string {
	evs := webhook.Events()
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.String()
	}
	return out
}

func readWebhookForm(f url.Values) (webhookForm, ops.WebhookOptions) {
	wf := webhookForm{
		URL:         strings.TrimSpace(f.Get("url")),
		Events:      f["events"],
		ContentType: f.Get("content_type"),
		Active:      checked(f, "active"),
	}
	active := wf.Active
	return wf, ops.WebhookOptions{
		URL:         wf.URL,
		Events:      wf.Events,
		ContentType: wf.ContentType,
		Secret:      f.Get("secret"),
		Active:      &active,
	}
}

func (h uiHandler) webhooks(w http.ResponseWriter, r *http.Request, b *browser, p page, rest string) {
	p.Tab = "webhooks"
	p.Title = "Webhooks · " + p.Repo.Name
	if rest != "" {
		h.webhook(w, r, b, p, strings.Split(rest, "/"))
		return
	}

	svc := b.service()
	d := &webhooksData{Events: eventNames(), Form: webhookForm{ContentType: "json", Active: true, Events: []string{webhook.EventPush.String()}}}
	p.Data = d
	var err error
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			var opts ops.WebhookOptions
			d.Form, opts = readWebhookForm(f)
			if len(opts.Events) == 0 {
				err = ops.Invalid("choose at least one event")
			} else if _, err = svc.CreateWebhook(r.Context(), p.Repo.Name, opts); err == nil {
				http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "webhooks", "", ""), "hook-created"), http.StatusSeeOther)
				return
			}
		}
	}

	hooks, herr := svc.Webhooks(r.Context(), p.Repo.Name)
	if herr != nil {
		h.opsFail(w, r, b, p, herr)
		return
	}
	d.Hooks = hooks
	if err != nil {
		h.renderForm(w, r, b, p, "webhooks", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "webhooks", p)
}

type webhookData struct {
	Hook       ops.Webhook
	Events     []string
	Form       webhookForm
	Deliveries []ops.Delivery
	Err        string
}

type deliveryData struct {
	Hook     ops.Webhook
	Delivery ops.DeliveryDetail
	Err      string
}

// findWebhook is one of the repository's webhooks; the service lists them.
func findWebhook(r *http.Request, svc ops.Service, repo string, id int64) (ops.Webhook, error) {
	hooks, err := svc.Webhooks(r.Context(), repo)
	if err != nil {
		return ops.Webhook{}, err
	}
	for _, hk := range hooks {
		if hk.ID == id {
			return hk, nil
		}
	}
	return ops.Webhook{}, errNotFound
}

func (h uiHandler) webhook(w http.ResponseWriter, r *http.Request, b *browser, p page, parts []string) {
	svc := b.service()
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		h.fail(w, r, p, errNotFound)
		return
	}
	hook, err := findWebhook(r, svc, p.Repo.Name, id)
	if errors.Is(err, errNotFound) {
		h.fail(w, r, p, errNotFound)
		return
	}
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	hookURL := repoURL(p.Repo.Name, "webhooks/"+parts[0], "", "")

	switch {
	case len(parts) == 1:
		h.editWebhook(w, r, b, p, hook, hookURL)

	case len(parts) == 2 && parts[1] == "delete":
		if r.Method != http.MethodPost {
			p.Title = "Remove webhook · " + p.Repo.Name
			p.Data = confirmData{
				Heading: "Remove this webhook?",
				Message: "Events will stop being sent to it, and its deliveries are forgotten.",
				What:    hook.URL,
				Action:  hookURL + "/delete",
				Button:  "Remove webhook",
				Cancel:  hookURL,
			}
			h.render(w, r, "confirm", p)
			return
		}
		if err := svc.DeleteWebhook(r.Context(), p.Repo.Name, id); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "webhooks", "", ""), "hook-deleted"), http.StatusSeeOther)

	case len(parts) == 3 && parts[1] == "deliveries" && r.Method != http.MethodPost:
		del, err := svc.Delivery(r.Context(), p.Repo.Name, id, parts[2])
		if err != nil {
			if errors.Is(err, errNotFound) || ops.IsInvalid(err) {
				h.fail(w, r, p, errNotFound)
				return
			}
			h.opsFail(w, r, b, p, err)
			return
		}
		p.Title = "Delivery · " + p.Repo.Name
		p.Data = deliveryData{Hook: hook, Delivery: del}
		h.render(w, r, "delivery", p)

	case len(parts) == 4 && parts[1] == "deliveries" && parts[3] == "redeliver" && r.Method == http.MethodPost:
		if err := svc.Redeliver(r.Context(), p.Repo.Name, id, parts[2]); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		http.Redirect(w, r, withDone(hookURL, "redelivered"), http.StatusSeeOther)

	default:
		h.fail(w, r, p, errNotFound)
	}
}

func (h uiHandler) editWebhook(w http.ResponseWriter, r *http.Request, b *browser, p page, hook ops.Webhook, hookURL string) {
	svc := b.service()
	p.Title = "Webhook · " + p.Repo.Name
	d := &webhookData{
		Hook:   hook,
		Events: eventNames(),
		Form:   webhookForm{URL: hook.URL, Events: hook.Events, ContentType: hook.ContentType, Active: hook.Active},
	}
	p.Data = d

	var err error
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			var opts ops.WebhookOptions
			d.Form, opts = readWebhookForm(f)
			if len(opts.Events) == 0 {
				err = ops.Invalid("choose at least one event")
			} else if err = svc.UpdateWebhook(r.Context(), p.Repo.Name, hook.ID, opts); err == nil {
				http.Redirect(w, r, withDone(hookURL, "hook-saved"), http.StatusSeeOther)
				return
			}
		}
	}

	dels, derr := svc.Deliveries(r.Context(), p.Repo.Name, hook.ID)
	if derr != nil {
		h.opsFail(w, r, b, p, derr)
		return
	}
	d.Deliveries = dels
	if err != nil {
		h.renderForm(w, r, b, p, "webhook", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "webhook", p)
}
