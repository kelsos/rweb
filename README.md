# rweb

A single-binary TUI/CLI to start, stop and supervise the full **rotki.com** local
development stack, with centrally-managed, agent-opaque secrets.

## What it does

- **Supervises the stack** — docker infra, Django + Huey, the Go backend, Nuxt,
  and the optional Rust `nest` service — started in dependency/health order,
  detached so they survive an rweb crash, with multiplexed logs and a live TUI
  dashboard. Profiles pick which services run.
- **Owns the database lifecycle** — create/migrate/reset, plus `pg_dump`/restore
  backups (migrations are out-of-band, so rweb runs them).
- **Centralizes config + secrets** — an age-encrypted secret store (identity in
  the OS keychain) plus a non-secret `[env.*]` layer, injected into each service
  so a fresh checkout or git worktree needs no `.env` files. `NUXT_PUBLIC_*` are
  treated as public (non-secret).
- **Named environments** — overlay the base config with per-environment values
  and their own secrets, switched stickily or per-run.
- **Git worktree aware** — point a repo at a worktree leaf and switch branches
  per repo, per run.

## Quick start

```sh
make install                              # build + drop rweb on your PATH
rweb init --seed-secrets placeholders     # config + age identity + dev secrets/posture
rweb config set-repo rotkehlchen_web <path>
rweb config set-repo rotki_com       <path>   # a worktree leaf
rweb doctor && rweb env doctor            # verify tools, repos, secrets
rweb up                                   # start the full stack (auto-syncs deps + migrates)
rweb dashboard                            # watch it live; start/stop/restart from here
```

`--seed-secrets placeholders` fills the required secrets with the project's dev
values (`generate` makes random ones) and seeds the dev testnet posture, so the
stack boots with nothing entered by hand. To set things yourself instead, run
plain `rweb init` and see [First-time setup](#first-time-setup-secrets--env) for
which values go in the secret store vs the non-secret env.

`rweb up` is idempotent: already-running services are reattached, not
restarted, so it's safe to re-run. Stop everything rweb manages with
`rweb down` (docker infra is left up); `rweb down --all` stops the docker infra
too (equivalent to `rweb down` + `rweb docker down`).

## Profiles

`rweb up [profile]` selects which services to run (default `full`):

| Profile         | Services                                         |
| --------------- | ------------------------------------------------ |
| `full`          | docker, django, huey, go-dev, nuxt               |
| `web-only`      | go-dev, nuxt                                      |
| `backend-only`  | docker, django, huey                             |
| `review-static` | docker, django, huey, build-web, go-serve        |
| `e2e`           | docker, django, huey, go-dev, nuxt               |

Profiles live in `config.toml` — add or tweak them there.

## Dashboard keys

| Key       | Action                                   |
| --------- | ---------------------------------------- |
| `↑`/`↓`   | select a service                         |
| `s`       | start the selected (stopped) service     |
| `x`       | stop the selected service                |
| `r`       | restart the selected service             |
| `f`       | filter the log pane to the selected service (again to clear) |
| `tab`/`l` | focus the log pane to scroll (again to return) |
| `←`/`→`   | scroll the log pane horizontally (long lines) |
| `q`       | quit                                     |

The dashboard keeps the last few thousand log lines in memory; the full history
is on disk — `rweb logs <svc>` reads the whole file. Each service's log rotates
to `<svc>.log.1` on a fresh start, so the active file holds just the current run.

Config / secrets / DB:

- `rweb init` — scaffold XDG config + generate an age identity (stored in the OS
  keychain, with a `0600` key-file fallback when no keychain is available).
- `rweb doctor` — check tools (`uv`/`pnpm`/`docker`/`make`), repo paths, secret store.
- `rweb config path|show` — inspect configuration.
- `rweb secret` — open the interactive **secret manager TUI** (or
  `secret set|rm|list|edit` non-interactively). Values decrypt only into memory;
  `list` shows names only.
- `rweb env doctor` — diff required keys against the store (names only).
- `rweb db create|migrate|reset|superuser|shell` — Django DB lifecycle
  (migrations are out-of-band; rweb owns them).
- `rweb db backup [--name <n>]` — compressed `pg_dump` (custom format) into the
  backups dir; `db backups` lists them, `db restore [name|latest|path]` reloads
  one (destructive; `-y` to skip the prompt). Backups live under the XDG data dir
  by default, or `db.backup_dir` in config.toml.

Supervisor:

- `rweb up [profile]` — start a profile (default `full`), in dependency order,
  waiting on each service's health probe. Auto-migrates after docker is up
  (`--no-migrate` to skip). The optional Rust `nest` service auto-joins when its
  repo is configured and the profile runs the DB; `--no-nest` skips it.
- `rweb down` — stop rweb-managed services (docker infra left running; `--all`
  stops docker too).
- `rweb status` / `rweb restart <svc>`.
- `rweb logs [svc...] [-f]` — tail one service, or a multiplexed, color-tagged
  view of all tracked services when no name is given.
- `rweb dashboard` (alias `dash`) — interactive TUI: live service table with
  start/stop/restart controls and an embedded follow-along log pane.
- `rweb sync [--force]` — install dependencies (frozen lockfiles) for any repo
  whose lockfile changed; runs automatically on `up` (skip with `--no-sync`).
- `rweb docker up|down` — manage docker infra only.
- `rweb exec django -- <cmd>` — run a manage.py command with env injected.
- `rweb open site|mailpit|traefik|report` — open a service URL.

Services run **detached** in their own process groups, so they survive an rweb
crash; rweb tracks them in a state file and reattaches. A flock prevents
concurrent operations.


## Build

```sh
make build      # -> ./rweb
make install    # -> ~/.local/bin/rweb (no sudo)
make hooks      # enable the pre-commit checks (gofmt/vet/build/test)
```

## Shell completion

```sh
rweb completion install        # detect $SHELL, install for the current user
rweb completion install zsh    # or name the shell explicitly (bash|zsh|fish)
```

Installs (and, when re-run, updates) the completion script in a per-user
directory — `_rweb` on your zsh `$fpath` (e.g. `~/.oh-my-zsh/completions`),
`~/.local/share/bash-completion/completions/rweb`, or
`~/.config/fish/completions/rweb.fish`. For zsh, reload with
`rm -f ~/.zcompdump* && exec zsh`. `rweb completion <shell>` still just prints
the script if you'd rather place it yourself.

## Configuration

Config lives at `$XDG_CONFIG_HOME/rweb/config.toml` (run `rweb config path` to
print it, `rweb config show` for the effective values). `rweb init` writes it
with sensible defaults; the only thing you *must* set is `[repos]`.

Set repo paths from the CLI (expands `~`/relative; `rweb config edit` opens the
file in `$EDITOR`):

```sh
rweb config set-repo rotkehlchen_web ~/dev/rotki/rotkehlchen-web
rweb config set-repo rotki_com       ~/dev/rotki/rotki.com/develop   # worktree leaf
rweb config set-repo nest            ""                              # clear optional repo
```

| Section      | Keys                                                              | Notes                                              |
| ------------ | ----------------------------------------------------------------- | -------------------------------------------------- |
| `[repos]`    | `rotkehlchen_web`, `rotki_com`, `nest`, `rotki_com_e2e`           | Absolute paths to your checkouts. `nest` optional. |
| `[tools]`    | `uv`, `pnpm`, `docker`, `make`                                    | Executable names or paths (default: bare names).   |
| `[ports]`    | `postgres`, `redis`, `django`, `go_dev`, `nuxt`, `nest`, `traefik_https` | Used for health probes + conflict detection. |
| `[secrets]`  | `age_recipient`, `keyring_service`, `keyring_user`                | Managed by `rweb init`; rarely edited by hand.     |
| `[db]`       | `name`, `user`, `host`, `port`                                    | Local Postgres connection (password is a secret).  |
| `[review]`   | `static_dir`, `port`                                              | The `review-static` profile's SSG serve.           |
| `[e2e]`      | `command`, `ui_command`                                           | How the private Playwright suite is invoked.       |
| `[profiles]` | `<name> = { services = [...] }`                                   | Add or tweak the service sets `rweb up` can run.   |
| `[worktrees]`| `<repo> = "<branch-or-dir>"`                                       | Sticky git-worktree selection; managed by `rweb worktree`. |
| `[env.<scope>]`| `<KEY> = "<value>"`                                             | Non-secret env injected per scope; managed by `rweb env set`. |

A `[repos]` path must point at a git **working tree** — a normal checkout *or*
one leaf of a multi-worktree layout (e.g. `rotki.com/{main,develop,feature-*}`),
not the container directory that merely holds them. `rweb doctor` rejects a path
that is not a work tree.

## Worktrees

Some repos (notably rotki.com) keep several git worktrees side by side under one
parent. rweb discovers worktrees from git at runtime, so you can point the stack
at a different worktree **without editing the repo's path**:

```sh
rweb worktree list                 # show every repo's worktrees (* = active)
rweb worktree list rotki_com       # just one repo
rweb worktree use rotki_com develop   # sticky: persists across runs
rweb worktree clear rotki_com         # revert to the configured path
rweb up --worktree rotki_com=feature/x   # ephemeral: this run only, overrides sticky
```

Selection is per-repo and accepts a branch name (`develop`, `feature/reddit-link`)
or a worktree directory's base name. A selection that names a worktree the repo
doesn't have falls back to the configured path with a warning, so plain
single-checkout repos and `--worktree` on a repo without that branch just no-op.

## Named environments

A **named environment** overlays the base (`default`) config: it carries its own
non-secret `[env.*]` values *and* its own secrets (in a sibling
`secrets.<name>.age`), inheriting whatever the base doesn't override. Selection
mirrors worktrees — sticky via `env use`, or per-run via the global `--env` flag.

```sh
rweb env new staging --seed-secrets placeholders  # create, with its own secret store
rweb env use staging                # make it the sticky active env
rweb env current                    # show the active env
rweb env envs                       # list envs (* marks active)
rweb env set --env staging django DOMAIN staging.local
rweb --env staging secret set django DB_PASS       # per-env secret
rweb --env staging up               # run the stack with the staging overlay
rweb env rm-env staging --purge-secrets
```

A fresh `env new` seeds the dev testnet posture into the new env (skip with
`--no-posture`); `--from <env>` clones an existing env instead. `env doctor`,
`env set/rm/list`, and `env import` all honor `--env`, and `env doctor` unions a
named env's own secrets with the base secrets it inherits.

## Non-secret env

Repo `.env` files are gitignored, so a fresh checkout — and every new git
worktree — starts without them. rweb can inject the **non-secret** config itself
so you don't copy `.env` files between worktrees: store the values per scope and
rweb overlays them onto each service's environment at launch.

```sh
rweb env set django DB_HOST localhost
rweb env set nuxt   NUXT_PUBLIC_BASE_URL https://localhost
rweb env list                 # show what's configured (values shown — non-secret)
rweb env rm  django DB_HOST
```

Already have a repo `.env`? Import it in one shot — keys whose names look
sensitive (`PASS`/`SECRET`/`TOKEN`/…) go to the encrypted secret store, the rest
(including every `NUXT_PUBLIC_*`, which is public) go to `[env.*]`:

```sh
rweb env import nuxt packages/website/.env --dry-run   # preview the routing
rweb env import nuxt packages/website/.env             # apply (skips existing)
rweb env import django ../rotkehlchen-web/localtest_env --overwrite
rweb --env staging env import nuxt packages/website/.env  # into a named env
```

The scopes are the same as secrets (`shared`, `django`, `go-backend`, `nuxt`)
and the values are injected into exactly the services in that scope. Precedence,
lowest to highest, is:

```
os.Environ  →  [env.<scope>] (managed baseline)  →  repo .env files (if present)  →  service-computed env  →  secrets
```

So a worktree that still has its own `.env` files keeps overriding the baseline
(unchanged behavior), while a fresh worktree runs entirely off the central
values plus the secret store — nothing to copy. This injects into the process
environment only (rweb does not write `.env` files); the services consume it
directly (`go-dev` reads `os.Getenv`, `nuxt` reads `NUXT_PUBLIC_*` at runtime,
and `build-web` bakes the same `NUXT_PUBLIC_*` values at generate time, so the
`review-static` build is covered too).

**Which goes where:** `rweb env manifest` lists keys in three tiers — required
and optional **secrets** (which belong in the secret store, `rweb secret …`, and
are what `rweb env doctor` validates) and optional **non-secret env** (which
belongs in `[env.*]`). Every `NUXT_PUBLIC_*` value is public (baked into the
client bundle), so it is always non-secret — it lives in `[env.*]`, never the
secret store.
Every *other* key your repo `.env` files define (the non-sensitive config —
`DOMAIN`, `DJANGO_DEBUG`, `NUXT_PUBLIC_BASE_URL`, `NUXT_PUBLIC_TESTING`,
`PROXY_DOMAIN`, …) belongs in `[env.*]` (`rweb env set …`). Don't split a single
key across both: secrets always win, so a value set in both places takes the
secret-store copy.

## Secrets model

Secrets live in `$XDG_CONFIG_HOME/rweb/secrets.age` (age ciphertext). The age
private key lives in the OS keychain, so unlock is automatic — no passphrase
prompt per run, and no plaintext on disk. Headless/CI: set `RWEB_AGE_KEY`.

Secrets are grouped by **scope**, and each scope is overlaid as env onto the
services that use it. Run `rweb env manifest` for the authoritative list of
expected keys, and `rweb env doctor` to diff them against your store (missing
**names** only, never values). For reference, the scopes and their keys:

- **shared** — `POSTGRES_PASSWORD`, `REDIS_PASSWORD`
- **django** (django, huey) — `DJANGO_SECRET_KEY`, `HASHID_FIELD_SALT`,
  `DB_PASS`, `BRAINTREE_MERCHANT_ID`, `BRAINTREE_PUBLIC_KEY`,
  `BRAINTREE_PRIVATE_KEY`, `RECAPTCHA_PUBLIC_KEY`, `RECAPTCHA_PRIVATE_KEY`
- **go-backend** (go-dev, go-serve) — `GOOGLE_CLIENT_SECRET`,
  `MONERIUM_CLIENT_SECRET`
- **nuxt** — *no secrets*: every `NUXT_PUBLIC_*` is baked into the client bundle,
  so it's public (see [Non-secret env](#non-secret-env)).

Connection details (`DB_HOST/PORT/NAME/USER`, `REDIS_HOST`) are **derived** from
`[db]` config and injected automatically — they are neither secrets nor `[env.*]`
entries.

> This list mirrors `internal/secrets/manifest.go` — update both together.

### Which values are shared vs service-specific

A secret's **scope** decides which services receive it — it's overlaid as env
onto every service in that group, and nothing else sees it. The **shared** scope
is special: it's merged into *every* scoped service (so the DB/redis passwords
reach Django and the Go backend, not just docker):

- **shared** → merged into every scoped service. The Rust `nest` service reads
  its own `configuration.yaml`, so it takes no secret scope at all.
- **django** → injected into `django` and `huey`.
- **go-backend** → injected into `go-dev` and `go-serve`.
- **nuxt** → `nuxt` and `build-web` (currently no secrets — all public).

A few values describe the **same real thing** and must agree across scopes even
though they live in different scopes — set them as a pair:

| Concept            | Backend key (scope)                              | Frontend key (scope)                                          |
| ------------------ | ------------------------------------------------ | ------------------------------------------------------------- |
| reCAPTCHA site     | `RECAPTCHA_PUBLIC_KEY` / `_PRIVATE_KEY` (django) | `NUXT_PUBLIC_RECAPTCHA_SITE_KEY` (nuxt)                       |
| Google OAuth app   | `GOOGLE_CLIENT_SECRET` (go-backend)              | `NUXT_PUBLIC_GOOGLE_CLIENT_ID` (nuxt)                         |
| Monerium app       | `MONERIUM_CLIENT_SECRET` (go-backend)            | `NUXT_PUBLIC_MONERIUM_AUTHORIZATION_CODE_FLOW_CLIENT_ID` (nuxt)|

Postgres has **two** passwords that are *not* the same: `POSTGRES_PASSWORD`
(shared) is the superuser, used by rweb to create/dump/restore the DB;
`DB_PASS` (django) is the `rotkehlchen` application-role password Django connects
with. `DB_NAME`/`DB_USER` must match `[db].name`/`[db].user` in `config.toml`.

### Where each value comes from

Values fall into three kinds — only the third group requires anything external:

| Kind | Keys | What to put |
| ---- | ---- | ----------- |
| **You choose (dev-local)** | `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, `DB_PASS`, `DJANGO_SECRET_KEY`, `HASHID_FIELD_SALT` | Any strong random string for local dev. Generate, e.g. `openssl rand -hex 32`. Reuse the same value wherever it must agree (see above). |
| **Auto-derived (don't set)** | `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `REDIS_HOST` | rweb computes these from `[db]` config (and `localhost` for redis) and injects them, so they're neither secrets nor `[env.*]` entries. Change them via `[db]` in `config.toml`, not here. |
| **External provider (use sandbox/test creds)** | `BRAINTREE_MERCHANT_ID` / `_PUBLIC_KEY` / `_PRIVATE_KEY`, `RECAPTCHA_PUBLIC_KEY` / `_PRIVATE_KEY` + `NUXT_PUBLIC_RECAPTCHA_SITE_KEY`, `GOOGLE_CLIENT_SECRET` + `NUXT_PUBLIC_GOOGLE_CLIENT_ID`, `MONERIUM_CLIENT_SECRET` + `NUXT_PUBLIC_MONERIUM_AUTHORIZATION_CODE_FLOW_CLIENT_ID`, `NUXT_PUBLIC_WALLET_CONNECT_PROJECT_ID` | Obtain from each provider's developer console (Braintree sandbox, Google reCAPTCHA admin, Google Cloud OAuth, Monerium partner portal, WalletConnect Cloud). For shared rotki dev credentials, get them from a teammate / the team secret store — do **not** invent these. |

> The actual values are **not** stored in this repo. rweb keeps them encrypted in
> `secrets.age`; pull real shared dev credentials from the team's secret store.

### First-time setup (secrets + env)

Two stores feed the stack: the encrypted **secret store** (sensitive values) and
the non-secret **`[env.*]`** layer (everything else from the repo `.env` files).
Set both once and a fresh checkout — or any new worktree — runs with no `.env`
files to copy.

Fast path — seed the base env's required secrets and dev posture in one shot,
then just point at your repos:

```sh
rweb init --seed-secrets placeholders   # or `generate` for random throwaways
rweb config set-repo rotkehlchen_web <path>
rweb config set-repo rotki_com       <path>
rweb doctor && rweb env doctor          # should pass with no manual secrets
```

`placeholders` uses the project's well-known dev values (`POSTGRES_PASSWORD`/
`DB_PASS=123`, `REDIS_PASSWORD=1234`, …); `generate` makes random ones (keeping
`POSTGRES_PASSWORD` and `DB_PASS` matched so the DB connects). Both also seed the
dev testnet posture into `[env.*]` (skip with `--no-posture`). Or set everything
by hand:

```sh
rweb init                       # one-time: creates the age identity + recipient

# 1. Secrets — set every key from `rweb env manifest`.
rweb env manifest               # the authoritative list, grouped by scope
rweb secret edit                # bulk: edit the decrypted store in $EDITOR, save
#   …or per key (value read hidden from stdin when omitted):
rweb secret set shared POSTGRES_PASSWORD                       # prompts hidden
rweb secret set django DJANGO_SECRET_KEY "$(openssl rand -hex 32)"
rweb env doctor                 # confirms no required secret is missing (names only)

# 2. Non-secret env — the rest of the repo .env config, so worktrees need no files.
rweb env set django DJANGO_DEBUG true
rweb env set django DOMAIN localhost
rweb env set nuxt   NUXT_PUBLIC_BASE_URL https://localhost
rweb env set nuxt   NUXT_PUBLIC_TESTING true
# …copy the remaining non-secret keys from a teammate's .env / the repo .env.example
rweb env list                   # review what's set (values shown — non-secret)

rweb doctor                     # tools, repo work-trees, lockfiles, secret store
```

`rweb env doctor` reports missing secret **names** only and never prints values,
so it's safe to run anytime to check the store is complete before `rweb up`. The
non-secret values you'd otherwise copy from each repo's `.env.example`; rweb just
holds them centrally instead of in per-worktree files.

## License

[MIT](LICENSE) © Konstantinos Paparas
