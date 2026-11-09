package selection

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
)

var (
	newKey     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new repo"))
	importKey  = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "import repo"))
	accountKey = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "account"))
)

// canManage reports whether the create, import and account keys apply: there is a
// Service and the repository list is showing, not being filtered.
func (s *Selection) canManage() bool {
	return s.common.Service != nil && s.activePane == selectorPane && !s.IsFiltering()
}

// CapturingInput implements common.InputCapturer.
func (s *Selection) CapturingInput() bool { return s.host.Active() }

func repoFields(name string) []*form.Field {
	return []*form.Field{
		form.Text("name", "Name", name).Placeholder("group/project"),
		form.Text("project_name", "Project name", ""),
		form.Text("description", "Description", ""),
		form.Toggle("private", "Private", false),
		form.Toggle("hidden", "Hidden", false),
	}
}

func createOptions(f *form.Form) ops.CreateRepo {
	return ops.CreateRepo{
		Name:        f.String("name"),
		ProjectName: f.String("project_name"),
		Description: f.String("description"),
		Private:     f.Bool("private"),
		Hidden:      f.Bool("hidden"),
	}
}

// created opens the new repository and reloads the list.
func (s *Selection) created(name *string) func() tea.Cmd {
	return func() tea.Cmd {
		n := *name
		return tea.Batch(s.Init(), func() tea.Msg { return common.OpenRepoMsg(n) })
	}
}

func (s *Selection) newRepo() tea.Cmd {
	f := form.New(s.common, "New repository", repoFields("")...)
	var name string
	return s.host.Open(f, func(f *form.Form) error {
		r, err := s.common.Service.CreateRepo(s.common.Context(), createOptions(f))
		name = r.Name
		return err
	}, s.created(&name))
}

const (
	modeFork   = "hard fork"
	modeMirror = "mirror"
)

func (s *Selection) importRepo() tea.Cmd {
	isMirror := func(f *form.Form) bool { return f.Choice("mode") == modeMirror }
	fields := []*form.Field{
		form.Text("remote", "Remote", "").Placeholder("https://github.com/owner/repo.git"),
	}
	fields = append(fields, repoFields("")...)
	fields = append(fields,
		form.Choice("mode", "Keep", []string{modeFork, modeMirror}, modeFork).
			Hint("a hard fork drops the remote; a mirror keeps following it"),
		form.Toggle("auto_sync", "Auto sync", true).When(isMirror).
			Hint("sync on the server's schedule; otherwise only when you sync it"),
		form.Toggle("lfs", "Git LFS", false).Hint("also pull Git LFS objects"),
	)
	f := form.New(s.common, "Import a repository", fields...)
	var name string
	return s.host.Open(f, func(f *form.Form) error {
		opts := createOptions(f)
		opts.Remote = f.String("remote")
		opts.LFS = f.Bool("lfs")
		if isMirror(f) {
			opts.Mirror = true
			auto := f.Bool("auto_sync")
			opts.AutoSync = &auto
		}
		r, err := s.common.Service.CreateRepo(s.common.Context(), opts)
		name = r.Name
		return err
	}, s.created(&name))
}
