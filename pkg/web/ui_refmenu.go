package web

import (
	"context"
	"net/http"
	"net/url"
	gopath "path"
	"strings"
)

// The branch and tag switcher. Every entry is the page you're on with only
// its ref changed, so switching keeps the tab, the file or folder, and any
// search. A path the other ref doesn't have lands on its nearest folder
// that it does have (see nearestDir), rather than on an error.

const (
	menuBranches = 8
	menuTags     = 5
	// switchParam marks a link from the switcher, so a missing path is
	// answered with its nearest folder rather than "not found".
	switchParam = "switch"
	// missingParam names the path that wasn't there, for the notice.
	missingParam = "missing"
)

// RefMenu is what the switcher lists.
type RefMenu struct {
	Current  string
	Branches []RefChoice
	Tags     []RefChoice
	// More is set when there are refs the menu leaves out.
	More   bool
	AllURL string
}

// RefChoice is one entry in the switcher.
type RefChoice struct {
	Name    string
	URL     string
	Current bool
}

// placeActions keep their place across a switch: the rest go to the files
// at the other ref.
var placeActions = map[string]bool{
	"": true, "tree": true, "blob": true, "blame": true, "commits": true,
	"search": true, "insights": true, "find": true,
}

// RefMenu lists the refs to switch to from this page. It's built only when
// the page draws the switcher.
func (p page) RefMenu() *RefMenu {
	if p.refMenu == nil {
		return nil
	}
	return p.refMenu(p.Ref)
}

// refMenuFor returns the RefMenu builder for a request.
func refMenuFor(ctx context.Context, b *browser, repo string, u *url.URL) func(string) *RefMenu {
	return func(current string) *RefMenu {
		refs, err := b.listRefs(ctx, repo, false)
		if err != nil {
			return nil
		}
		m := &RefMenu{Current: current, AllURL: refsURL(repo, u)}
		pick := func(list []RefInfo, max int) []RefChoice {
			out := []RefChoice{}
			for _, ri := range list {
				if len(out) == max {
					m.More = true
					// Keep the current one in view even when it's older.
					if ri.Name == current {
						out[len(out)-1] = RefChoice{Name: ri.Name, URL: switchURL(repo, u, ri.Name), Current: true}
					}
					continue
				}
				out = append(out, RefChoice{Name: ri.Name, URL: switchURL(repo, u, ri.Name), Current: ri.Name == current})
			}
			return out
		}
		m.Branches = pick(refs.Branches, menuBranches)
		m.Tags = pick(refs.Tags, menuTags)
		return m
	}
}

// switchURL is u at another ref. Pages that don't keep their place go to
// the repository's files at that ref.
func switchURL(repo string, u *url.URL, ref string) string {
	action := pageAction(repo, u)
	if u == nil || !placeActions[action] {
		return repoURL(repo, "tree", ref, "")
	}
	q := url.Values{}
	for k, v := range u.Query() {
		switch k {
		case "ref", "page", "lines", switchParam, missingParam, "done", "back":
			continue
		}
		q[k] = v
	}
	q.Set("ref", ref)
	if q.Get("path") != "" && (action == "tree" || action == "blob" || action == "blame") {
		q.Set(switchParam, "1")
	}
	return u.EscapedPath() + "?" + q.Encode()
}

// pageAction is the part of u's path after the repository's "/-/".
func pageAction(repo string, u *url.URL) string {
	if u == nil {
		return ""
	}
	_, action, _ := strings.Cut(strings.Trim(u.Path, "/"), "/-/")
	if strings.Trim(u.Path, "/") == repo {
		return ""
	}
	return action
}

// refsURL is the branches and tags page, remembering the page it was
// opened from so a ref picked there keeps the place too.
func refsURL(repo string, from *url.URL) string {
	base := repoURL(repo, "refs", "", "")
	if from == nil || !placeActions[pageAction(repo, from)] {
		return base
	}
	back := from.EscapedPath()
	if from.RawQuery != "" {
		back += "?" + from.RawQuery
	}
	return base + "?back=" + url.QueryEscape(back)
}

// backURL reads the refs page's back parameter: a page of this repository
// that keeps its place, or nil. Anything else, such as another site, is
// ignored, so the parameter can't turn the page's links into a way off it.
func backURL(repo, back string) *url.URL {
	if back == "" || !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.Contains(back, "\\") {
		return nil
	}
	u, err := url.Parse(back)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return nil
	}
	p := strings.Trim(u.Path, "/")
	if p != repo && !strings.HasPrefix(p, repo+"/-/") {
		return nil
	}
	if !placeActions[pageAction(repo, u)] {
		return nil
	}
	return u
}

// nearestDir answers a switch to a ref that doesn't have path: it sends the
// visitor to the nearest folder above it that the ref does have. It reports
// whether it did.
func nearestDir(w http.ResponseWriter, r *http.Request, b *browser, repo, ref, path string) bool {
	if r.URL.Query().Get(switchParam) != "1" || path == "" {
		return false
	}
	dir := path
	for dir != "" {
		dir = parentDir(dir)
		if _, err := b.tree(r.Context(), repo, ref, dir, false); err == nil {
			to := repoURL(repo, "tree", ref, dir)
			sep := "?"
			if strings.Contains(to, "?") {
				sep = "&"
			}
			http.Redirect(w, r, to+sep+missingParam+"="+url.QueryEscape(path), http.StatusFound)
			return true
		}
	}
	return false
}

// missingNotice says which path the ref didn't have, when a switch landed
// on its folder instead. It names only a path below the folder shown, so a
// crafted link can't put other words on the page.
func missingNotice(r *http.Request, ref, dir string) string {
	missing := r.URL.Query().Get(missingParam)
	if missing == "" || len(missing) > 512 || gopath.Clean(missing) != missing {
		return ""
	}
	if dir != "" && !strings.HasPrefix(missing, dir+"/") {
		return ""
	}
	if dir == "" {
		return missing + " isn't on " + ref + ", and nor is any folder it was in. This is the top of the repository."
	}
	return missing + " isn't on " + ref + ". This is the nearest folder that is."
}

// refsData is the branches and tags page: the refs, and the page it was
// opened from, if any.
type refsData struct {
	Refs
	repo string
	back *url.URL
}

// Link is where picking ref on the refs page goes: back to the page it was
// opened from at that ref, or to the files at it.
func (d refsData) Link(ref string) string {
	if d.back == nil {
		return repoURL(d.repo, "tree", ref, "")
	}
	return switchURL(d.repo, d.back, ref)
}

// Back reports whether the page was opened from another.
func (d refsData) Back() bool { return d.back != nil }
