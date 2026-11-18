# Review flows

Flows the web-app-review pipeline walks in a browser. Run against a server
seeded with: an admin (signed in by HTTP basic auth: username + access
token), a user `alice`, a repository `icecream` with 3 tags (v0.1–v0.3) and
branches `main`, `feature` and a long `fix/…` name, a webhook on `icecream`,
a nested repository `org/nested-sundae` and a mirror `mirror-cone`.
Check every flow at 1280px and at 390px (phone) wide.

## 1. Home
1. Open `/`.
2. Expect recent commits, then the repository list with a filter. The header
   shows the site name, the signed-in user and an **Admin** pill (admins only).

## 2. Repository navigation (phone layout)
1. Open `/icecream` at 390px.
2. Expect the repository tabs on one row that scrolls sideways, fading at the
   edge, with the branch pill at the end; no horizontal page scroll.
3. Expect History beside **Go to file**, and the clone/push commands wrapping
   rather than overflowing, selectable whole.
4. Open `/icecream/-/tree?path=docs`, then `/icecream/-/blob?path=README.md`.
   Expect the file view and its latest commit.

## 3. History, commit and compare
1. Open `/icecream/-/commits`. Expect 4 commits.
2. Open one commit. Expect its diff; `?view=split` shows side by side.
3. Open `/icecream/-/compare?base=v0.1&head=main`. Expect 2 commits and a diff.

## 4. Branches and tags
1. Open `/icecream/-/refs`.
2. Expect each branch's ahead/behind, each tag's downloads, and **Delete…**
   set apart from the downloads.
3. Click **Delete…** on `feature`, then **Cancel**: a full-size button,
   nothing deleted. Delete it for real: it is gone, with a notice.

## 5. New repository
1. Open `/-/new`.
2. Expect remote, Git LFS and sync options hidden until **Fork** or
   **Mirror** is chosen, then shown, with no "Mirror only:"/"Fork or mirror:"
   prefixes.
3. Create `scratch`. Expect its summary page.

## 6. Repository settings and delete
1. Open `/icecream/-/settings`. Change the description; expect a notice.
2. Open the delete confirmation for `scratch`. Expect it to say what goes
   (branches, tags, commits) and whether a copy exists elsewhere.
3. Submit with the wrong name typed: expect refusal, repository kept.
4. Submit with `scratch` typed: expect it deleted.
5. On `mirror-cone`, expect the question to name the mirror's remote.

## 7. Access and webhooks
1. Open `/icecream/-/access`. Add `alice`, then remove her (Cancel first).
2. Open `/icecream/-/webhooks`. Expect events described in words with the
   event name in small mono beside each.

## 8. Account
1. Open `/-/me`. Expect keys, tokens and repositories.
2. **Remove…**/**Delete…** fill the row's height at 390px; Cancel is a button
   as large as the red one.

## 9. Admin
1. Open `/-/admin`. Expect users and settings.
2. Delete `alice`: expect her username must be typed; a wrong name is refused.
