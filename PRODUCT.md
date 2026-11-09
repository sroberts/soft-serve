# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

One person: the owner of this fork, who runs Soft Serve as a personal Git server behind Outpost on their tailnet. They browse their own repositories in the web view from a laptop and from a phone. AI tools read the same repositories through the MCP server.

Nobody else is a confirmed audience. Anonymous visitors, collaborators and teams are not design targets unless that changes.

## Product Purpose

Soft Serve is a self-hostable Git server. SSH is its main interface: a TUI for browsing, and commands for managing repositories, users, collaborators and tokens. This fork adds a web view, a REST API under `/v1/` and an MCP server, so the owner's repositories can be read from a browser or by an AI tool as well as over SSH. It also lets Soft Serve run under Outpost.

Success means the owner can find and understand anything in their repositories from whichever device is at hand: files, history, diffs, branches and tags, comparisons, search, blame and insights.

## Positioning

Soft Serve is a personal Git server under the owner's control, reached only through their own tailnet and Outpost's login. It is not a hosted forge. Its web view, REST API and MCP server read the same data under the same access rules.

## Operating Context

- Outpost supervises Soft Serve: it binds loopback only, takes its HTTP port from `OUTPOST_PORT`, and answers `/livez` for health (`outpost.toml`, `outpost-service-spec.md`).
- Tailscale and Outpost's login gateway put the pages behind HTTPS and a sign-in. Outpost's identity maps to a Soft Serve user. Access tokens work for the API.
- SSH handles everything the pages don't: creating repos, users, keys, tokens and collaborators.
- The owner uses both a phone and a laptop. Phone browsing is a real use.

## Capabilities and Constraints

- **Same data in pages, API and MCP (confirmed):** anything a page shows is also available from `/v1/` (described in `openapi.json`) and from the MCP tools. A new page feature needs its API and MCP counterparts.
- **Phone use matters (confirmed):** every surface has to work at phone width, including touch.
- **Access rules:** pages follow Soft Serve's access levels. Private and hidden repositories, and anything the viewer can't read, stay out of view.
- **Current state, not commitments:** today the pages are read-only and use no JavaScript, with a strict CSP (`default-src 'none'`). The owner hasn't made either one a rule. Writes from the web, or scripts on the pages, are open decisions.
- Templates and CSS are embedded in the Go binary (`pkg/web/ui_templates`, `pkg/web/ui_static`). Syntax highlighting is Chroma's `github` and `github-dark` styles.
- Releases are built by hand on the fork. Charm's release pipeline and its Docker, Homebrew and package channels aren't available.

## Brand Commitments

**Undecided.** The fork is expected to get its own identity later. Charm's "Soft Serve" name and ice-cream branding come from upstream, and the owner has not committed to keeping them. Don't build new work on Charm's brand assets, and don't invent a replacement identity until the owner sets one.

## Evidence on Hand

- `README.md` is upstream Charm's, including its sunsetting notice. It isn't the fork's own copy.
- The fork has no screenshots, testimonials or users other than the owner. Don't fabricate any.

## Product Principles

1. **One source of truth, three readers.** The web view, REST API and MCP show the same facts under the same access rules.
2. **Works where the owner is.** A page is only finished when it works on a phone as well as a laptop.
3. **Nothing leaks past access.** Pages never reveal a repository, path or credential that the viewer's access doesn't allow, including mirror URLs and signer details.
4. **Complements SSH; doesn't replace it.** The web view is for reading and understanding. Whether management ever moves there is the owner's call.
