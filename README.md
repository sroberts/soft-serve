# Sunsetting Soft Serve

To continue innovating in this space and supporting our most important work, we’ve sunset Soft Serve. The code will continue to be open source and publicly available. No further security updates will be applied. If you love this project and would like to maintain a fork, please do.

Thank you to everyone who has supported and contributed to Soft Serve over the years. We appreciate you.

***

# Soft Serve

<p>
    <img style="width: 451px" src="https://stuff.charm.sh/soft-serve/soft-serve-header.png?0" alt="A nice rendering of some melting ice cream with the words ‘Charm Soft Serve’ next to it"><br>
    <a href="https://github.com/charmbracelet/soft-serve/releases"><img src="https://img.shields.io/github/release/charmbracelet/soft-serve.svg" alt="Latest Release"></a>
    <a href="https://pkg.go.dev/github.com/charmbracelet/soft-serve?tab=doc"><img src="https://godoc.org/github.com/golang/gddo?status.svg" alt="GoDoc"></a>
    <a href="https://github.com/charmbracelet/soft-serve/actions"><img src="https://github.com/charmbracelet/soft-serve/workflows/build/badge.svg" alt="Build Status"></a>
    <a href="https://nightly.link/charmbracelet/soft-serve/workflows/nightly/main"><img src="https://shields.io/badge/-Nightly%20Builds-orange?logo=hackthebox&logoColor=fff&style=appveyor"/></a>
</p>

A tasty, self-hostable Git server for the command line. 🍦

<picture>
  <source media="(max-width: 750px)" srcset="https://github.com/charmbracelet/soft-serve/assets/42545625/c754c746-dc4c-44a6-9c39-28649264cbf2">
  <source media="(min-width: 750px)" width="750" srcset="https://github.com/charmbracelet/soft-serve/assets/42545625/c754c746-dc4c-44a6-9c39-28649264cbf2">
  <img src="https://github.com/charmbracelet/soft-serve/assets/42545625/c754c746-dc4c-44a6-9c39-28649264cbf2" alt="Soft Serve screencast">
</picture>

- Easy to navigate TUI available over SSH
- Clone repos over SSH, HTTP, or Git protocol
- Git LFS support with both HTTP and SSH backends
- Manage repos with SSH
- Create repos on demand with SSH or `git push`
- Browse repos, files and commits with SSH-accessible UI
- Print files over SSH with or without syntax highlighting and line numbers
- Easy access control
  - SSH authentication using public keys
  - Allow/disallow anonymous access
  - Add collaborators with SSH public keys
  - Repos can be public or private
  - User access tokens

## Where can I see it?

Just run `ssh git.charm.sh` for an example. You can also try some of the following commands:

```bash
# Jump directly to a repo in the TUI
ssh git.charm.sh -t soft-serve

# Print out a directory tree for a repo
ssh git.charm.sh repo tree soft-serve

# Print a specific file
ssh git.charm.sh repo blob soft-serve cmd/soft/main.go

# Print a file with syntax highlighting and line numbers
ssh git.charm.sh repo blob soft-serve cmd/soft/main.go -c -l
```

Or you can use Soft Serve to browse local repositories using `soft browse
[directory]` or running `soft` within a Git repository.

## Installation

Soft Serve is a single binary called `soft`. You can get it from a package
manager:

```bash
# macOS or Linux
brew install charmbracelet/tap/soft-serve

# Windows (with Winget)
winget install charmbracelet.soft-serve

# Arch Linux
pacman -S soft-serve

# Nix
nix-env -iA nixpkgs.soft-serve

# Debian/Ubuntu
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://repo.charm.sh/apt/gpg.key | sudo gpg --dearmor -o /etc/apt/keyrings/charm.gpg
echo "deb [signed-by=/etc/apt/keyrings/charm.gpg] https://repo.charm.sh/apt/ * *" | sudo tee /etc/apt/sources.list.d/charm.list
sudo apt update && sudo apt install soft-serve

# Fedora/RHEL
echo '[charm]
name=Charm
baseurl=https://repo.charm.sh/yum/
enabled=1
gpgcheck=1
gpgkey=https://repo.charm.sh/yum/gpg.key' | sudo tee /etc/yum.repos.d/charm.repo
sudo yum install soft-serve
```

You can also download a binary from the [releases][releases] page. Packages are
available in Alpine, Debian, and RPM formats. Binaries are available for Linux,
macOS, and Windows.

[releases]: https://github.com/charmbracelet/soft-serve/releases

Or just install it with `go`:

```bash
go install github.com/charmbracelet/soft-serve/cmd/soft@latest
```

A [Docker image][docker] is also available.

[docker]: https://github.com/charmbracelet/soft-serve/blob/main/docker.md

## Setting up a server

Make sure `git` is installed, then run `soft serve`. That’s it.

This will create a `data` directory that will store all the repos, ssh keys,
and database.

By default, program configuration is stored within the `data` directory. But,
this can be overridden by setting a custom path to a config file with `SOFT_SERVE_CONFIG_LOCATION`
that is pre-created. If a config file pointed to by `SOFT_SERVE_CONFIG_LOCATION`,
the default location within the `data` dir is used for generating a default config.

To change the default data path use `SOFT_SERVE_DATA_PATH` environment variable.

```sh
SOFT_SERVE_DATA_PATH=/var/lib/soft-serve soft serve
```

When you run Soft Serve for the first time, make sure you have the
`SOFT_SERVE_INITIAL_ADMIN_KEYS` environment variable is set to your ssh
authorized key. Any added key to this variable will be treated as admin with
full privileges.

Using this environment variable, Soft Serve will create a new `admin` user that
has full privileges. You can rename and change the user settings later.

If you'd like a repository to exist the moment the server finishes booting,
with no human logging in to run `repo create`, set `SOFT_SERVE_DEFAULT_REPO`
to a repository name. This is useful for GitOps tools like ArgoCD that need a
git remote to point at as part of their own bootstrap (e.g. from a Helm chart
or Terraform apply):

```sh
SOFT_SERVE_INITIAL_ADMIN_KEYS="$(cat ~/.ssh/id_ed25519.pub)" \
SOFT_SERVE_DEFAULT_REPO=gitops \
  soft serve
```

The repository is created empty and public, exactly as if a human had run
`repo create gitops` with no flags; only its *existence* is guaranteed on
boot, not its reachability, which is still governed entirely by the
`anon-access`/`allow-keyless` settings described below. Booting again against
the same data directory is a no-op if the repository already exists.

Check out [Systemd][systemd] on how to run Soft Serve as a service using
Systemd. Soft Serve packages in our Apt/Yum repositories come with Systemd
service units.

[systemd]: https://github.com/charmbracelet/soft-serve/blob/main/systemd.md

### Server Configuration

Once you start the server for the first time, the settings will be in
`config.yaml` under your data directory. The default `config.yaml` is
self-explanatory and will look like this:

```yaml
# Soft Serve Server configurations

# The name of the server.
# This is the name that will be displayed in the UI.
name: "Soft Serve"

# Log format to use. Valid values are "json", "logfmt", and "text".
log_format: "text"

# The SSH server configuration.
ssh:
  # The address on which the SSH server will listen.
  listen_addr: ":23231"

  # The public URL of the SSH server.
  # This is the address that will be used to clone repositories.
  public_url: "ssh://localhost:23231"

  # The path to the SSH server's private key.
  key_path: "ssh/soft_serve_host"

  # The path to the SSH server's client private key.
  # This key will be used to authenticate the server to make git requests to
  # ssh remotes.
  client_key_path: "ssh/soft_serve_client"

  # How the server's ssh client verifies host keys of ssh remotes it imports
  # or mirrors from, checked against <data_path>/ssh/known_hosts.
  # "accept-new" records a host key on first contact and refuses a changed
  # one (needs OpenSSH 7.6 or newer). "yes" refuses any host that is not
  # already in known_hosts; use it with a pre-seeded known_hosts file.
  client_strict_host_key_checking: "accept-new"

  # The ssh hosts that repositories may be imported from or mirrored. The
  # client key above is presented to every host a user imports from, so list
  # only hosts you trust that key with. Entries are hostnames, IP addresses,
  # or CIDR ranges, each with an optional port. When the list is empty, only
  # server admins may import from or mirror ssh remotes.
  # CIDR entries are checked against DNS only at import or sync time and ssh
  # resolves the name again, so prefer hostnames for anything you do not
  # control. The list is matched against the name given to ssh; your
  # ssh_config still applies to it.
  client_allowed_hosts: []
    # - "git.internal.example"
    # - "gitlab.corp:2222"
    # - "10.0.0.0/8"

  # The maximum number of seconds a connection can take.
  # A value of 0 means no timeout.
  max_timeout: 0

  # The number of seconds a connection can be idle before it is closed.
  idle_timeout: 120

# The Git daemon configuration.
git:
  # The address on which the Git daemon will listen.
  listen_addr: ":9418"

  # The maximum number of seconds a connection can take.
  # A value of 0 means no timeout.
  max_timeout: 0

  # The number of seconds a connection can be idle before it is closed.
  idle_timeout: 3

  # The maximum number of concurrent connections.
  max_connections: 32

# The HTTP server configuration.
http:
  # The address on which the HTTP server will listen.
  listen_addr: ":23232"

  # The path to the TLS private key.
  tls_key_path: ""

  # The path to the TLS certificate.
  tls_cert_path: ""

  # The public URL of the HTTP server.
  # This is the address that will be used to clone repositories.
  # Make sure to use https:// if you are using TLS.
  public_url: "http://localhost:23232"

  # The cross-origin request security options
  cors:
    # The allowed cross-origin headers
    allowed_headers:
      - "Accept"
      - "Accept-Language"
      - "Content-Language"
      - "Content-Type"
      - "Origin"
      - "X-Requested-With"
      - "User-Agent"
      - "Authorization"
      - "Access-Control-Request-Method"
      - "Access-Control-Allow-Origin"

    # The allowed cross-origin URLs
    allowed_origins:
      - "http://localhost:23232" # always allowed
      # - "https://example.com"

    # The allowed cross-origin methods
    allowed_methods:
      - "GET"
      - "HEAD"
      - "POST"
      - "PUT"
      - "OPTIONS"

# The database configuration.
db:
  # The database driver to use.
  # Valid values are "sqlite" and "postgres".
  driver: "sqlite"
  # The database data source name.
  # This is driver specific and can be a file path or connection string.
  # Make sure foreign key support is enabled when using SQLite.
  data_source: "soft-serve.db?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

# Git LFS configuration.
lfs:
  # Enable Git LFS.
  enabled: true
  # Enable Git SSH transfer.
  ssh_enabled: false

# Cron job configuration
jobs:
  mirror_pull: "@every 10m"

# The stats server configuration.
stats:
  # The address on which the stats server will listen.
  listen_addr: ":23233"
# Additional admin keys.
#initial_admin_keys:
#  - "ssh-rsa AAAAB3NzaC1yc2..."

# Override the anon-access and allow-keyless settings below (normally
# managed at runtime via the `settings` command and stored in the
# database). If set, these always take precedence over the database, for
# as long as they remain set -- not just on first run. This is intended for
# scripted, non-production bootstrapping (e.g. spinning up a throwaway
# server for local dev tooling), not for production use.
#anon_access: "admin-access"
#allow_keyless: true

# Repository name to create on boot if it does not already exist.
# Leave empty to disable.
#default_repo: ""
```

You can also use environment variables, to override these settings. All server
settings environment variables start with `SOFT_SERVE_` followed by the setting
name all in uppercase. Here are some examples:

- `SOFT_SERVE_NAME`: The name of the server that will appear in the TUI
- `SOFT_SERVE_SSH_LISTEN_ADDR`: SSH listen address
- `SOFT_SERVE_SSH_KEY_PATH`: SSH host key-pair path
- `SOFT_SERVE_HTTP_LISTEN_ADDR`: HTTP listen address
- `SOFT_SERVE_HTTP_PUBLIC_URL`: HTTP public URL used for cloning
- `SOFT_SERVE_GIT_MAX_CONNECTIONS`: The number of simultaneous connections to git daemon
- `SOFT_SERVE_ANON_ACCESS`: Overrides the `anon-access` setting (see [Authentication](#authentication))
- `SOFT_SERVE_ALLOW_KEYLESS`: Overrides the `allow-keyless` setting (see [Authentication](#authentication))
- `SOFT_SERVE_DEFAULT_REPO`: Repository name to create on boot if missing

#### Database Configuration

Soft Serve supports both SQLite and Postgres for its database. Like all other Soft Serve settings, you can change the database _driver_ and _data source_ using either `config.yaml` or environment variables. The default config uses SQLite as the default database driver.

To use Postgres as your database, first create a Soft Serve database:

```sh
psql -h<hostname> -p<port> -U<user> -c 'CREATE DATABASE soft_serve'
```

Then set the database _data source_ to point to your Postgres database. For instance, if you're running Postgres locally, using the default user `postgres` and using a database name `soft_serve`, you would have this config in your config file or environment variable:

```
db:
  driver: "postgres"
  data_source: "postgres://postgres@localhost:5432/soft_serve?sslmode=disable"
```

Environment variables equivalent:

```sh
SOFT_SERVE_DB_DRIVER=postgres \
SOFT_SERVE_DB_DATA_SOURCE="postgres://postgres@localhost:5432/soft_serve?sslmode=disable" \
soft serve
```

You can specify a database connection password in the _data source_ url. For example, `postgres://myuser:dbpass@localhost:5432/my_soft_serve_db`.

#### LFS Configuration

Soft Serve supports both Git LFS [HTTP](https://github.com/git-lfs/git-lfs/blob/main/docs/api/README.md) and [SSH](https://github.com/git-lfs/git-lfs/blob/main/docs/proposals/ssh_adapter.md) protocols out of the box, there is no need to do any extra set up.

Use the `lfs` config section to customize your Git LFS server.

> **Note**: The pure-SSH transfer is disabled by default.

### Running under Outpost

[Outpost](https://github.com/sroberts/outpost) runs services as native
processes behind their own Tailscale node. Soft Serve follows its service
spec ([`outpost-service-spec.md`](outpost-service-spec.md)), and
[`outpost.toml`](outpost.toml) describes it to `outpost add`.

When Outpost starts Soft Serve (it sets `OUTPOST_STORAGE_DIR`), two things
change:

- **Every listener binds `127.0.0.1`.** SSH, the Git daemon and HTTP default to
  loopback instead of all interfaces, so nothing is reachable except through
  Outpost. These are written to a new `config.yaml` like any other default, and
  a `SOFT_SERVE_*_LISTEN_ADDR` variable still overrides them.
- **HTTP listens on `OUTPOST_PORT`.** It takes precedence over `config.yaml`,
  so a port Outpost reassigns takes effect without editing the file.
  `SOFT_SERVE_HTTP_LISTEN_ADDR`, if set, still wins.

State lives in `data/` inside the storage directory, so `outpost backup` and
`outpost restore` cover it. The healthcheck is `/livez`.

Outpost proxies only HTTP by default. To reach Soft Serve over SSH, forward a
tailnet port to it in the service's manifest (this needs an Outpost with `tcp`
forwards), and tell Soft Serve the addresses it is served at so clone URLs are
right:

```toml
tailscale_name = "soft-serve"
tcp = { 22 = 23231 }   # tailnet port 22 -> SSH on 127.0.0.1:23231
mcp_path = "/mcp"     # the portal routes /mcp/soft-serve here

[env]
SOFT_SERVE_INITIAL_ADMIN_KEYS = "ssh-ed25519 AAAA..."
SOFT_SERVE_SSH_PUBLIC_URL = "ssh://soft-serve.<tailnet>.ts.net"
SOFT_SERVE_HTTP_PUBLIC_URL = "https://soft-serve.<tailnet>.ts.net"
```

Then `git clone ssh://soft-serve.<tailnet>.ts.net/<repo>` and
`ssh soft-serve.<tailnet>.ts.net` work from any device on your tailnet.

Things to know:

- **Who can reach SSH.** With Outpost's login gateway on, only devices whose
  Tailscale login belongs to an Outpost user allowed to open Soft Serve can
  connect (`outpost user tailnet` links a login to a user). Soft Serve still
  checks SSH keys as usual. Every connection reaches Soft Serve from
  `127.0.0.1`, so its logs can't tell callers apart by address.
- **Git over HTTPS goes through the gateway.** Git can't do a browser login, so
  send an Outpost access token with each request:
  `git -c http.extraHeader="Outpost-Token: opat_..." clone https://soft-serve.<tailnet>.ts.net/<repo>`.
  Soft Serve's own HTTP authentication still applies on top.
- **The Git daemon isn't forwarded.** `git://` has no authentication, so it
  stays on loopback. Set `SOFT_SERVE_GIT_ENABLED=false` if you don't want it at
  all.
- **The public URLs are configured, not derived.** Soft Serve uses them in
  places that have no request to read a hostname from, such as webhooks and
  access tokens, so set them in `env` as above. The [web view](#the-web-view-rest-api-and-mcp)
  builds its own links from the request instead.
- **The web view, REST API and MCP use your Outpost login.** Behind the
  gateway, Soft Serve verifies each request's signed `X-Outpost-Identity`
  token and gives you the access of the Soft Serve user with the same name as
  your Outpost user. If the names differ, rename one, for example
  `ssh soft-serve.<tailnet>.ts.net user set-username admin <outpost-name>`;
  until then you see public repositories only, and the page says so. MCP is
  at `/mcp`; add `mcp_path = "/mcp"` to the manifest and Outpost's portal
  serves it at `/mcp/soft-serve` too.

## Server Access

Soft Serve at its core manages your server authentication and authorization. Authentication verifies the identity of a user, while authorization determines their access rights to a repository.

To manage the server users, access, and repos, you can use the SSH command line interface.

Try `ssh localhost -i ~/.ssh/id_ed25519 -o IdentitiesOnly=yes -p 23231 help` for more info. Make sure
you use your key here.

> **Note** The `IdentitiesOnly` option is used to prevent SSH from using any
> other keys in your `~/.ssh` directory. This is useful when you have multiple
> keys, and you want to use a specific key for Soft Serve.

For ease of use, instead of specifying the key, port, and hostname every time
you SSH into Soft Serve, add your own Soft Serve instance entry to your SSH
config. For instance, to use `ssh soft` instead of typing `ssh localhost -i
~/.ssh/id_ed25519 -o IdentitiesOnly=yes -p 23231`, we can define a `soft` entry in our SSH config
file `~/.ssh/config`.

```conf
Host soft
  HostName localhost
  Port 23231
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
```

Now, we can do `ssh soft` to SSH into Soft Serve. Since `git` is also aware of
this config, you can use `soft` as the hostname for your clone commands.

```sh
git clone ssh://soft/dotfiles
# make changes
# add & commit
git push origin main
```

> **Note** The `-i` and `-o` parts will be omitted in the examples below for brevity. You
> can add your server settings to your sshconfig for quicker access.

### Authentication

Everything that needs authentication is done using SSH. Make sure you have
added an entry for your Soft Serve instance in your `~/.ssh/config` file.

By default, Soft Serve gives read-only permission to anonymous connections to
any of the above protocols. This is controlled by two settings `anon-access`
and `allow-keyless`.

- `anon-access`: Defines the access level for anonymous users. Available
  options are `no-access`, `read-only`, `read-write`, and `admin-access`.
  Default is `read-only`.
- `allow-keyless`: Whether to allow connections that doesn't use keys to pass.
  Setting this to `false` would disable access to SSH keyboard-interactive,
  HTTP, and Git protocol connections. Default is `true`.

```sh
$ ssh -p 23231 localhost settings
Manage server settings

Usage:
  ssh -p 23231 localhost settings [command]

Available Commands:
  allow-keyless Set or get allow keyless access to repositories
  anon-access   Set or get the default access level for anonymous users

Flags:
  -h, --help   help for settings

Use "ssh -p 23231 localhost settings [command] --help" for more information about a command.
```

> **Note** These settings can only be changed by admins.

When `allow-keyless` is disabled, connections that don't use SSH Public Key
authentication will get denied. This means cloning repos over HTTP(s) or git://
will get denied.

Meanwhile, `anon-access` controls the access level granted to connections that
use SSH Public Key authentication but are not registered users. The default
setting for this is `read-only`. This will grant anonymous connections that use
SSH Public Key authentication `read-only` access to public repos.

`anon-access` is also used in combination with `allow-keyless` to determine the
access level for HTTP(s) and git:// clone requests.

#### Local/dev bootstrap

`anon-access` and `allow-keyless` are normally admin-only runtime settings,
which means scripting a fully open server from a cold start would otherwise
require booting with an admin key, waiting for the server to come up, then
SSHing in as that admin to run `settings` commands. For local development
tooling (e.g. standing up a throwaway Git remote for something like Argo CD
to pull from in a dev environment), you can instead set the `SOFT_SERVE_ANON_ACCESS`
and `SOFT_SERVE_ALLOW_KEYLESS` environment variables (or the equivalent
`anon_access`/`allow_keyless` `config.yaml` fields) to grant full,
unauthenticated access from the moment the server starts, with no admin key
and no `settings` command required:

```sh
SOFT_SERVE_ANON_ACCESS=admin-access \
SOFT_SERVE_ALLOW_KEYLESS=true \
  soft serve
```

> **Warning** This grants anyone who can reach the server full admin access
> with no authentication at all. It is intended strictly for local/dev use on
> a machine or network you trust — never expose a server configured this way
> to an untrusted network. The server logs a warning banner on startup
> whenever this combination is active.
>
> Unlike the `settings` command, these overrides always take precedence over
> the database for as long as they're set — not just on first run. If you
> also try to tighten access via `ssh soft settings ...` while an override is
> active, the change is saved but has no effect until the override is
> removed; the `settings` command will warn you when this happens.

#### SSH

Soft Serve doesn't allow duplicate SSH public keys for users. A public key can be associated with one user only. This makes SSH authentication simple and straight forward, add your public key to your Soft Serve user to be able to access Soft Serve.

#### HTTP

You can generate user access tokens through the SSH command line interface. Access tokens can have an optional expiration date. Use your access token as the basic auth user to access your Soft Serve repos through HTTP.

```sh
# Create a user token
ssh -p 23231 localhost token create 'my new token'
ss_1234abc56789012345678901234de246d798fghi

# Or with an expiry date
ssh -p 23231 localhost token create --expires-in 1y 'my other token'
ss_98fghi1234abc56789012345678901234de246d7
```

Now you can access to repos that require `read-write` access.

```sh
git clone http://ss_98fghi1234abc56789012345678901234de246d7@localhost:23232/my-private-repo.git my-private-repo
# Make changes and push
```

### Authorization

Soft Serve offers a simple access control. There are four access levels,
no-access, read-only, read-write, and admin-access.

`admin-access` has full control of the server and can make changes to users and repos.

`read-write` access gets full control of repos.

`read-only` can read public repos.

`no-access` denies access to all repos.

## User Management

Admins can manage users and their keys using the `user` command. Once a user is
created and has access to the server, they can manage their own keys and
settings.

To create a new user simply use `user create`:

```sh
# Create a new user
ssh -p 23231 localhost user create beatrice

# Add user keys
ssh -p 23231 localhost user add-pubkey beatrice ssh-rsa AAAAB3Nz...
ssh -p 23231 localhost user add-pubkey beatrice ssh-ed25519 AAAA...

# Create another user with public key
ssh -p 23231 localhost user create frankie '-k "ssh-ed25519 AAAATzN..."'

# Need help?
ssh -p 23231 localhost user help
```

Admins can run `user` and `settings` from their own machine with `soft` too,
using an admin's access token (see [From Your Own Machine](#from-your-own-machine)),
and from the web view's admin pages and the TUI. Admins can't delete their own
account or remove their own admin rights; another admin can.

```sh
soft user create beatrice --key "$(cat beatrice.pub)"
soft user set-admin beatrice true
soft settings anon-access no-access
```

Once a user is created, they get `read-only` access to public repositories.
They can also create new repositories on the server.

Users can manage their keys using the `pubkey` command:

```sh
# List user keys
ssh -p 23231 localhost pubkey list

# Add key
ssh -p 23231 localhost pubkey add ssh-ed25519 AAAA...

# Wanna change your username?
ssh -p 23231 localhost set-username yolo

# To display user info
ssh -p 23231 localhost info
```

The same commands run from your own machine with `soft`, over the HTTP API
with an access token (see [From Your Own Machine](#from-your-own-machine)):

```sh
soft info
soft pubkey add "$(cat ~/.ssh/id_ed25519.pub)"
soft pubkey remove SHA256:...   # a key or its fingerprint
soft token create --expires-in 90d ci
soft token list
soft set-username yolo
```

You can also do all of this from the web view's account page and the TUI.

## Repositories

You can manage repositories using the `repo` command.

```sh
# Run repo help
$ ssh -p 23231 localhost repo help
Manage repositories

Usage:
  ssh -p 23231 localhost repo [command]

Aliases:
  repo, repos, repository, repositories

Available Commands:
  auto-sync    Set or get whether a mirror syncs on the server's schedule
  blob         Print out the contents of file at path
  branch       Manage repository branches
  collab       Manage collaborators
  create       Create a new repository
  delete       Delete a repository
  description  Set or get the description for a repository
  hide         Hide or unhide a repository
  import       Import a new repository from remote
  info         Get information about a repository
  list         List repositories
  mirror       Set or get a repository mirror property
  private      Set or get a repository private property
  project-name Set or get the project name for a repository
  rename       Rename an existing repository
  sync         Sync a mirror from its upstream now
  tag          Manage repository tags
  tree         Print repository tree at path

Flags:
  -h, --help   help for repo

Use "ssh -p 23231 localhost repo [command] --help" for more information about a command.
```

To use any of the above `repo` commands, a user must be a collaborator in the repository. More on this below.

### From Your Own Machine

The `soft` binary runs the same `repo` commands against a server's HTTP API,
with an access token instead of an SSH key. The commands, flags and output are
the ones above, and the server applies the same permissions.

```sh
export SOFT_SERVE_REMOTE=http://localhost:23232
export SOFT_SERVE_TOKEN=$(ssh -p 23231 localhost token create laptop)

soft repo list
soft repo create icecream -d 'Scoops'
soft repo import --mirror --auto-sync=false upstream https://github.com/charmbracelet/soft-serve
soft repo sync upstream
soft repo collab add icecream ana read-write
```

This is handy where SSH can't reach the server, such as behind Outpost's
gateway. `repo blob`, `repo tree` and `repo commit` are SSH-only for now.

### Creating Repositories

To create a repository, first make sure you are a registered user. Use the
`repo create <repo>` command to create a new repository:

```sh
# Create a new repository
ssh -p 23231 localhost repo create icecream

# Create a repo with description
ssh -p 23231 localhost repo create icecream '-d "This is an Ice Cream description"'

# ... and project name
ssh -p 23231 localhost repo create icecream '-d "This is an Ice Cream description"' '-n "Ice Cream"'

# I need my repository private!
ssh -p 23231 localhost repo create icecream -p '-d "This is an Ice Cream description"' '-n "Ice Cream"'

# Help?
ssh -p 23231 localhost repo create -h
```

Or you can add your Soft Serve server as a remote to any existing repo, given
you have write access, and push to remote:

```
git remote add origin ssh://localhost:23231/icecream
```

After you’ve added the remote just go ahead and push. If the repo doesn’t exist
on the server it’ll be created.

```
git push origin main
```

### Nested Repositories

Repositories can be nested too:

```sh
# Create a new nested repository
ssh -p 23231 localhost repo create charmbracelet/icecream

# Or ...
git remote add charm ssh://localhost:23231/charmbracelet/icecream
git push charm main
```

### Forks and Mirrors

You can also *import* repositories from any public remote. Use the `repo import` command.

```sh
ssh -p 23231 localhost repo import soft-serve https://github.com/charmbracelet/soft-serve
```

A plain import is a *hard fork*: once the repository is cloned its remote is
dropped, so nothing ties it to where it came from. It's yours to push to, and
it never pulls from upstream again.

Use `--mirror` or `-m` instead to keep the repository following its remote as
a *pull* mirror. Mirrors sync on the server's `jobs.mirror_pull` schedule
(every 10 minutes by default). To sync a mirror only when you ask, import it
with `--auto-sync=false`, or switch it later:

```sh
# mirror, but only sync on demand
ssh -p 23231 localhost repo import --mirror --auto-sync=false soft-serve https://github.com/charmbracelet/soft-serve

# pull from upstream now
ssh -p 23231 localhost repo sync soft-serve

# put it back on the schedule
ssh -p 23231 localhost repo auto-sync soft-serve true
```

### Deleting Repositories

You can delete repositories using the `repo delete <repo>` command.

```sh
ssh -p 23231 localhost repo delete icecream
```

### Renaming Repositories

Use the `repo rename <old> <new>` command to rename existing repositories.

```sh
ssh -p 23231 localhost repo rename icecream vanilla
```

### Repository Collaborators

Sometimes you want to restrict write access to certain repositories. This can
be achieved by adding a collaborator to your repository.

Use the `repo collab <command> <repo>` command to manage repo collaborators.

```sh
# Add collaborator to soft-serve
ssh -p 23231 localhost repo collab add soft-serve frankie

# Add collaborator with a specific access level
ssh -p 23231 localhost repo collab add soft-serve beatrice read-only

# Remove collaborator
ssh -p 23231 localhost repo collab remove soft-serve beatrice

# List collaborators
ssh -p 23231 localhost repo collab list soft-serve
```

### Repository Metadata

You can also change the repo's description, project name, whether it's private,
etc using the `repo <command>` command.

```sh
# Set description for repo
ssh -p 23231 localhost repo description icecream "This is a new description"

# Hide repo from listing
ssh -p 23231 localhost repo hidden icecream true

# List repository info (branches, tags, description, etc)
ssh -p 23231 localhost repo icecream info
```

To make a repository private, use `repo private <repo> [true|false]`. Private
repos can only be accessed by admins and collaborators.

```sh
ssh -p 23231 localhost repo private icecream true
```

### Repository Branches & Tags

Use `repo branch` and `repo tag` to list, and delete branches or tags. You can
also use `repo branch default` to set or get the repository default branch.

### Repository Tree

To print a file tree for the project, just use the `repo tree` command along with
the repo name as the SSH command to your Soft Serve server:

```sh
ssh -p 23231 localhost repo tree soft-serve
```

You can also specify the sub-path and a specific reference or branch.

```sh
ssh -p 23231 localhost repo tree soft-serve server/config
ssh -p 23231 localhost repo tree soft-serve main server/config
```

From there, you can print individual files using the `repo blob` command:

```sh
ssh -p 23231 localhost repo blob soft-serve cmd/soft/main.go
```

You can add the `-c` flag to enable syntax coloring and `-l` to print line
numbers:

```sh
ssh -p 23231 localhost repo blob soft-serve cmd/soft/main.go -c -l

```

Use `--raw` to print raw file contents. This is useful for dumping binary data.

### Repository webhooks

Soft Serve supports repository webhooks using the `repo webhook` command. You
can create and manage webhooks for different repository events such as _push_,
_collaborators_, and _branch_tag_create_ events.

```
Manage repository webhooks

Usage:
  ssh -p 23231 localhost repo webhook [command]

Aliases:
  webhook, webhooks

Available Commands:
  create      Create a repository webhook
  delete      Delete a repository webhook
  deliveries  Manage webhook deliveries
  list        List repository webhooks
  update      Update a repository webhook

Flags:
  -h, --help   help for webhook
```

## The Web View, REST API and MCP

Soft Serve's HTTP port also serves its repositories three ways, each showing
exactly what the caller may read and allowing what they may change:

- **Web pages** at `/`: repositories (with a filter), files with syntax
  highlighting and each one's latest commit, rendered READMEs, history of
  the repository or of one file or folder, commits with their diffs, blame,
  branches and tags with their dates and release notes, comparing two
  branches, tags or commits, searching the files, and `.tar.gz` and `.zip`
  downloads of any of them. Each history has an Atom feed
  (`/<repo>/-/commits.atom`). Pages are plain HTML with no script. Also:
  - **Managing repositories**, for signed-in users, with the same permissions
    as the `repo` commands over SSH: *New repository* (`/-/new`) creates one
    or imports one as a hard fork or a mirror, with or without auto-sync.
    *Settings* (write access) changes the description, project name,
    visibility, default branch and mirror settings, renames, syncs a mirror
    now, and deletes. *Access* (write access) adds and removes
    collaborators. *Webhooks* (admin access) adds, edits and removes webhooks
    and shows and resends their deliveries. Branches and tags can be deleted
    from their page. Without Outpost's gateway, a change asks the browser to
    sign in with your username and an access token.
  - **Lines and permalinks.** Click a line number to select it, then another
    to select the lines between (`?lines=10-25`). *Permalink* pins the link to
    the commit, so it keeps pointing at the same code.
  - **Go to file** (`/<repo>/-/find?q=brgo`) finds files by part of their path
    or its letters in order.
  - **History search**: commits by message (`?q=`) or author (`?author=`).
  - **Diffs** one collapsible section per file, unified or side by side
    (`?view=split`), with or without whitespace changes (`?w=1`). A file cut
    short, or left out of a diff over 1 MiB, links to its own diff in full.
  - **Branches** say how far ahead of and behind the default branch they are.
  - **Signed commits**: an SSH signature is checked against the SSH keys of
    this server's users. *Verified* names the user whose key signed it.
    Nothing checks it against the author or committer fields. GPG and X.509
    signatures are shown as signed, unverified.
  - **Mirrors** say where they're copied from (credentials taken out) and when
    they last synced.
  - **Insights** (`/<repo>/-/insights`): languages, contributors and commits
    per week for the last year.
  - **Your account** (`/-/me`): your user, SSH keys, access tokens (never
    the tokens themselves) and the repositories you can push to. Add and
    remove keys, create and delete tokens (a new token is shown once), and
    change your username. Behind Outpost, where your web sign-in is matched
    to your username, renaming is left to SSH or `soft set-username`.
  - **Admin** (`/-/admin`, server admins only): create, rename and delete
    users, manage their keys and admin rights, and change the server
    settings.
- **A REST API** under `/v1`, described by `/v1/openapi.json`. Repository
  names can contain slashes, so the name and the action are separated by
  `/-/`:

  ```sh
  curl http://localhost:23232/v1/repos
  curl "http://localhost:23232/v1/repos/icecream/-/tree?ref=main&path=docs"
  curl http://localhost:23232/v1/repos/icecream/-/blob?path=README.md
  curl http://localhost:23232/v1/repos/icecream/-/commits?per_page=10
  curl "http://localhost:23232/v1/repos/icecream/-/commits?path=docs/guide.md"
  curl "http://localhost:23232/v1/repos/icecream/-/compare?base=main&head=feature"
  curl "http://localhost:23232/v1/repos/icecream/-/search?q=scoop"
  curl "http://localhost:23232/v1/repos/icecream/-/commits?q=fix&author=ana"
  curl "http://localhost:23232/v1/repos/icecream/-/files?q=guide"
  curl "http://localhost:23232/v1/repos/icecream/-/commits/abc1234?ignore_whitespace=true"
  curl http://localhost:23232/v1/repos/icecream/-/stats
  curl -H "Authorization: Token $TOKEN" http://localhost:23232/v1/me
  curl -OJ "http://localhost:23232/v1/repos/icecream/-/archive?ref=v1.0&format=zip"
  ```

  The API also manages repositories, with the same operations and the same
  permissions as the `repo` commands over SSH: create and import (fork or
  mirror), settings, rename, delete, mirror sync, deleting branches and tags,
  collaborators, and webhooks with their deliveries. Bodies are JSON. Changes
  a browser says come from another site are refused.

  ```sh
  auth="Authorization: Token $TOKEN"
  curl -H "$auth" -d '{"name":"icecream","description":"Scoops"}' http://localhost:23232/v1/repos
  curl -H "$auth" -d '{"name":"upstream","remote":"https://github.com/charmbracelet/soft-serve","mirror":true,"auto_sync":false}' http://localhost:23232/v1/repos
  curl -H "$auth" -X PATCH -d '{"private":true,"default_branch":"main"}' http://localhost:23232/v1/repos/icecream
  curl -H "$auth" -X POST http://localhost:23232/v1/repos/upstream/-/sync
  curl -H "$auth" -d '{"username":"ana","access":"read-write"}' http://localhost:23232/v1/repos/icecream/-/collaborators
  curl -H "$auth" -X DELETE http://localhost:23232/v1/repos/icecream/-/branches/old-feature
  ```

  Your own account is under `/v1/me`: your SSH keys, access tokens (a new
  token is in the answer to its creation and nowhere else) and username.

  ```sh
  curl -H "$auth" -d "{\"key\":\"$(cat ~/.ssh/id_ed25519.pub)\"}" http://localhost:23232/v1/me/keys
  curl -H "$auth" -d '{"name":"ci","expires_at":"2027-01-01T00:00:00Z"}' http://localhost:23232/v1/me/tokens
  curl -H "$auth" -X DELETE http://localhost:23232/v1/me/tokens/3
  curl -H "$auth" -X PATCH -d '{"username":"yolo"}' http://localhost:23232/v1/me
  ```

  Admins manage users under `/v1/users` and the server settings at
  `/v1/settings`. Anyone else is refused, without learning whether a user
  exists.

  ```sh
  curl -H "$auth" -d '{"username":"beatrice","keys":["ssh-ed25519 AAAA..."]}' http://localhost:23232/v1/users
  curl -H "$auth" -X PATCH -d '{"admin":true}' http://localhost:23232/v1/users/beatrice
  curl -H "$auth" -X PATCH -d '{"anon_access":"no-access"}' http://localhost:23232/v1/settings
  ```

- **MCP** at `/mcp` (Streamable HTTP), with read-only tools to list
  repositories, branches, tags and commits (or search them by message or
  author), read a README, list a directory, find files by name, read and
  blame a file, get a commit's diff and signature, compare two refs, search
  the files, get a repository's stats, say who you are (`whoami`), and (for
  admins) list collaborators. A request that reaches `/mcp` over loopback
  must be addressed to `localhost`, to the host of
  `SOFT_SERVE_HTTP_PUBLIC_URL`, or come through Outpost's login gateway;
  anything else is refused, as a guard against DNS rebinding. Behind a
  reverse proxy or Outpost without its gateway, set the public URL.

Anonymous callers get the server's `anon-access` level. To read as a user,
send a Soft Serve access token (`ssh localhost -p 23231 token create`) as
`Authorization: Token <token>`. Behind Outpost's login gateway, your Outpost
login is used instead (see [Running under Outpost](#running-under-outpost)).

Nothing in a repository is ever served as a page: raw files go out as plain
text, images or downloads, with `nosniff` and a sandbox policy. Files tracked
by Git LFS are shown and served from Soft Serve's LFS store, not as pointers.

Behind Outpost's gateway, pages and the API offer only the SSH clone URL:
git over HTTPS would need a login the gateway doesn't pass through.

## The Soft Serve TUI

<img src="https://stuff.charm.sh/soft-serve/soft-serve-demo-commit.png" width="750" alt="TUI example showing a diff">

Soft Serve TUI browses repos over SSH, and manages them with the same
permissions as the `repo` commands: <kbd>n</kbd> creates a repository and
<kbd>i</kbd> imports one (hard fork or mirror) from the list. A repository's
*Settings*, *Collaborators* and *Webhooks* tabs change it, and <kbd>x</kbd>
deletes a branch or tag. <kbd>a</kbd> opens your account: add and remove
SSH keys, create and delete access tokens, and change your username. Server
admins get <kbd>A</kbd> for users and server settings. You can also use the TUI to browse local
repositories with `soft browse` or running `soft` within a Git repository;
management isn't offered there.

```sh
ssh localhost -p 23231
```

It's also possible to “link” to a specific repo:

```sh
ssh -p 23231 localhost -t soft-serve
```

You can copy text to your clipboard over SSH. For instance, you can press
<kbd>c</kbd> on the highlighted repo in the menu to copy the clone command
[^osc52].

[^osc52]:
    Copying over SSH depends on your terminal support of OSC52. Refer to
    [go-osc52](https://github.com/aymanbagabas/go-osc52) for more information.

## Hooks

Soft Serve supports git server-side hooks `pre-receive`, `update`,
`post-update`, and `post-receive`. This means you can define your own hooks to
run on repository push events. Hooks can be defined as a per-repository hook,
and/or global hooks that run for all repositories.

You can find per-repository hooks under the repository `hooks` directory.

Globs hooks can be found in your `SOFT_SERVE_DATA_PATH` directory under
`hooks`. Defining global hooks is useful if you want to run CI/CD for example.

Here's an example of sending a message after receiving a push event. Create an
executable file `<data path>/hooks/update`:

```sh
#!/bin/sh
#
# An example hook script to echo information about the push
# and send it to the client.

refname="$1"
oldrev="$2"
newrev="$3"

# Safety check
if [ -z "$GIT_DIR" ]; then
        echo "Don't run this script from the command line." >&2
        echo " (if you want, you could supply GIT_DIR then run" >&2
        echo "  $0 <ref> <oldrev> <newrev>)" >&2
        exit 1
fi

if [ -z "$refname" -o -z "$oldrev" -o -z "$newrev" ]; then
        echo "usage: $0 <ref> <oldrev> <newrev>" >&2
        exit 1
fi

# Check types
# if $newrev is 0000...0000, it's a commit to delete a ref.
zero=$(git hash-object --stdin </dev/null | tr '[0-9a-f]' '0')
if [ "$newrev" = "$zero" ]; then
        newrev_type=delete
else
        newrev_type=$(git cat-file -t $newrev)
fi

echo "Hi from Soft Serve update hook!"
echo
echo "RefName: $refname"
echo "Change Type: $newrev_type"
echo "Old SHA1: $oldrev"
echo "New SHA1: $newrev"

exit 0
```

Now, you should get a message after pushing changes to any repository.

## A note about RSA keys

Unfortunately, due to a shortcoming in Go’s `x/crypto/ssh` package, Soft Serve
does not currently support access via new SSH RSA keys: only the old SHA-1
ones will work.

Until we sort this out you’ll either need an SHA-1 RSA key or a key with
another algorithm, e.g. Ed25519. Not sure what type of keys you have?
You can check with the following:

```sh
$ find ~/.ssh/id_*.pub -exec ssh-keygen -l -f {} \;
```

If you’re curious about the inner workings of this problem have a look at:

- https://github.com/golang/go/issues/37278
- https://go-review.googlesource.com/c/crypto/+/220037
- https://github.com/golang/crypto/pull/197

## Contributing

See [contributing][contribute].

[contribute]: https://github.com/charmbracelet/soft-serve/contribute

## Feedback

We’d love to hear your thoughts on this project. Feel free to drop us a note!

- [Twitter](https://twitter.com/charmcli)
- [The Fediverse](https://mastodon.social/@charmcli)
- [Discord](https://charm.sh/chat)

## License

[MIT](https://github.com/charmbracelet/soft-serve/raw/main/LICENSE)

---

Part of [Charm](https://charm.sh).

<a href="https://charm.sh/"><img alt="The Charm logo" src="https://stuff.charm.sh/charm-badge.jpg" width="400"></a>

Charm热爱开源 • Charm loves open source
