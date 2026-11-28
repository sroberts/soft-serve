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
   edge, then **Manage** (owners only) and the branch pill, both always in
   view; no horizontal page scroll. On touch, a branch's or tag's `.zip` and
   `.tar.gz` are easy to tap apart.
3. Expect **Go to file** across the row at the repository's root, and the
   clone/push commands wrapping with a hanging indent rather than
   overflowing, selectable whole. The download line's ref stays inline.
4. Open `/icecream/-/tree?path=docs`: History beside **Go to file**. Then
   `/icecream/-/blob?path=README.md`: the file view and its latest commit.

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
   prefixes; Remote URL says it's required. Submitting a mirror with no URL
   is refused with "Give the address to import from."
3. Create `scratch`. Expect its summary page.

## 6. Repository settings and delete
1. Open `/icecream/-/settings`. Change the description; expect a notice.
2. Open the delete confirmation for `scratch`. Expect it to say what goes
   (branches, tags, commits) and whether a copy exists elsewhere.
3. Submit with the wrong name typed: expect refusal, repository kept.
4. Submit with `scratch` typed: expect it deleted.
5. On `mirror-cone`, expect the question to name the mirror's remote.

## 7. Access and webhooks
1. Open `/icecream/-/access`. Add `alice`, then remove her: one step, no
   confirmation, and the notice offers **Add them back** at her old level.
   Use it: she's back.
2. Open `/icecream/-/webhooks`. Expect events described in words with the
   event name in small mono beside each.

## 8. Account
1. Open `/-/me`. Expect keys, tokens and repositories.
2. On touch, **Remove…**/**Delete…** are at least 44px tall; on their
   confirmation, Cancel is a button as large as the red one.

## 9. Admin
1. Open `/-/admin`. Expect users and settings.
2. Delete `alice`: expect her username must be typed; a wrong name is refused.

## 10. Issues
1. Open `/icecream/-/issues`. Expect open issues grouped by status (In Review,
   In Progress, Todo, Backlog), each row with its priority, identifier
   (ICE-2), status, title, labels and assignee; the scope tabs, filters and
   **New issue**.
2. Create one from **New issue** with a label that doesn't exist yet: it's
   created, and the issue page says so.
3. On an issue, change status, priority, project and cycle from the side
   panel's menus: each changes in place, and the toast offers Undo. With a
   second user, open **Labels and more** and give an unknown assignee: the
   form stays open and says no user has that name. Fix it and save: the
   activity records each change.
4. Comment, edit the comment, delete it (with a confirmation).
5. Open `/-/issues`: every open issue across the repositories you can read.
6. At 390px, rows wrap under their title and the properties sit above the
   description.

## 11. Projects and cycles
1. From Issues, switch to **Projects**. Add one with a target date before
   its start: refused, and the form keeps what was typed. Fix it and add it.
2. Put two issues in it (one done) from their property forms. The project's
   page lists them by status, with "1 of 2 done · 50%"; the list shows it.
3. Switch to **Cycles**. Add a cycle, then another with no dates: it starts
   the day after the first ends and runs two weeks. A cycle overlapping
   another is refused.
4. Filter the issue list by project and by **Current cycle**.

## 12. Board and shortcuts
1. Open `/icecream/-/issues/board`: a column per status (no Canceled), all
   five visible at 1280px, one at a time with the next peeking in at 390px.
2. Change a card's status from its menu: it moves without the page
   reloading, and a message says so. With script off, the same menu posts
   and comes back to the board.
3. Drag a card to the top of another column with a mouse: it lands there.
4. On the list: press `j`, then `s`; the row's status menu opens with its
   current status focused; arrows move; Escape closes it. `?` shows the
   shortcuts.

## 13. Icons and copy
1. Open `/icecream`. Expect folders tinted, files outlined, a symlink named
   "symlink"; tabs with quiet icons at 1280 and none at 390. Open
   **Clone or download** (chevron turns): each command has a copy button;
   pressing it shows a check and announces "Copied.".
2. Open a commit: the full hash has a copy button beside it; changed files
   show added/modified/deleted/renamed as a mark and a word; each file's
   header has a turning chevron.
3. Open `/icecream/-/tree?ref=v0.1`: the ref pill shows a tag mark; on
   `main` a branch mark.
4. On an issue, each activity entry has a mark on the rail: the new status
   or priority itself for those changes, a bubble for comments.
5. A form error leads with an alert mark; a notice with a check.
