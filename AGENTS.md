# Gameday — AGENTS.md

AI agent reference for the Gameday codebase. Covers architecture, patterns, data models, and workflows.

## Project Overview

Gameday is a robotics competition management system. It provides an on-site web application for tournament operations (team management, match scheduling, scorekeeping) and an offline CLI for schedule generation.

- **Module**: `github.com/gizmo-platform/gameday`
- **Go version**: 1.25.0
- **Primary language**: Go

## Directory Structure

```
.
├── main.go                 # Entrypoint → cmd.Entrypoint()
├── go.mod
├── Dockerfile              # Multi-stage Alpine build, CGO + static SQLite
├── internal/
│   └── cmd/                # Cobra CLI commands
│       ├── root.go         # Root command, slog setup (LOG_LEVEL)
│       ├── onsite.go       # `gameday onsite` parent command
│       ├── onsite_serve.go # `gameday onsite serve` — boots DB, web server, modules
│       ├── cloud.go        # `gameday cloud` parent command
│       ├── cloud_serve.go  # `gameday cloud serve` — boots DB, web server, modules (cloud scope)
│       ├── bake.go         # `gameday bake` parent command
│       ├── bake_schedule.go# `gameday bake schedule` — offline schedule generation
│       ├── debug.go        # `gameday debug` parent command
│       ├── debug_game.go   # `gameday debug game`
│       ├── debug_game_genscores.go # Clear + regenerate random scores for a phase
│       ├── debug_game_lint.go # Lint a game setup file (parse, expr, references)
│       ├── debug_best.go   # `gameday debug best` parent command
│       ├── debug_best_genscores.go # Wipe + randomize BEST external scores
│       ├── version.go      # `gameday version`
│       └── util.go         # CLI utilities
├── modules/                # Pluggable web modules
│   ├── type.go             # modules.Web interface definition
│   ├── registry.go         # Module registry, ResolveModules, tie-breaker registry
│   ├── team/
│   │   ├── init.go         # Module registration ("team", required)
│   │   ├── team.go         # Team + Division models, Team CRUD, CSV import, cloud routes, embed ui/*
│   │   ├── cloud.go        # Cloud import handlers, cloud JSON endpoints, upsertTeamRows
│   │   └── ui/p2/views/team/         # list, form_single, form_bulk, import_cloud, divisions/*
│   ├── game/
│   │   ├── init.go         # Module registration ("game", depends on team)
│   │   ├── module.go       # Game state, MatchState enum, MatchPlacement, TeamModule
│   │   ├── schedule.go     # Schedule UI + division-aware generation
│   │   ├── clock.go        # Match clock: models, handlers, timers
│   │   ├── clock_match.go  # Clock-driven match state transitions
│   │   ├── scoreboard.go   # Scoreboard views
│   │   ├── scorecard.go    # Scorecard views
│   │   ├── setup.go        # Game setup (applyGameConfig shared with cloud import)
│   │   ├── cloud.go        # Cloud import handlers + public /cloud/setup YAML export
│   │   ├── export.go       # ExportContestData: portable contest schedule/scorecard export
│   │   ├── push.go         # Cloud push: store endpoint, PushContestData, push UI
│   │   ├── cloud_scorecards.go # Cloud scorecards-by-team lookup page (cloud scope)
│   │   ├── advancement.go  # Team advancement logic
│   │   ├── lint.go         # LintConfig: setup-file validation (expr, references)
│   │   ├── filters.go      # Scoreboard filtering
│   │   ├── filter_scoreboard.go
│   │   ├── debug.go        # Module-level debug endpoints
│   │   ├── util.go
│   │   └── ui/p2/          # Embedded templates (views/game/* incl. import_cloud, partials/scorecard*)
│   ├── best/
│   │   ├── init.go         # Module registration ("best", optional, depends on game)
│   │   ├── module.go       # BEST:ADMIN permission, scores + awards routes, GameModule interface
│   │   ├── scores.go       # Scores view
│   │   ├── awards.go       # Award registry, Scorer, dense ranking engine, awards page
│   │   ├── type.go         # ScoreType + TeamScoreValue models
│   │   ├── debug.go        # DebugGenerateScores
│   │   └── ui/p2/views/best/   # scores.p2, awards.p2
│   └── contest/
│       ├── init.go         # Module registration ("contest", cloud+onsite, required)
│       ├── module.go       # Contest + ContestRoster models, contest CRUD, roster routes, cloud JSON endpoints
│       ├── cloud.go        # Public cloud JSON endpoints (contest list, per-contest roster)
│       ├── onsite.go       # OnsiteModule: "My Contest" screen, selection, auto-import of roster + game
│       └── ui/p2/views/contest/    # list, form, roster, roster_add, my_contest
├── pkg/
│   ├── cloud/              # Client for the cloud instance (base URL, auth seam, per-feature files)
│   │   ├── client.go       # Client, New, Do/DoBytes/DoJSON, options (WithBaseURL/WithTimeout/WithAuthenticator/WithBasicAuth)
│   │   ├── auth.go         # Authenticator interface + ready BasicAuth
│   │   ├── teams.go        # RegionInfo/TeamInfo wire shapes, GetRegions/GetRegionTeams
│   │   ├── games.go        # GetGameFile (raw YAML bytes from the public game setup endpoint)
│   │   ├── contests.go     # ContestInfo wire shape, GetContests/GetContestRoster
│   │   ├── scores.go       # ContestPlacement wire shape, PushContestData
│   │   └── client_test.go
│   ├── db/
│   │   └── db.go           # GORM wrapper (options, SQLite default, Postgres via DSN), Raw(), InsertOrUpdate[T]
│   ├── web/
│   │   ├── server.go       # Server struct, Chi router, Pongo2, Authware, admin routes
│   │   ├── authz.go        # Permission struct, InstallPermission, RequirePermission, GuardRoute
│   │   ├── middleware.go   # ProfileKey context injection, Profile.HasPermission logic
│   │   ├── tmpl.go         # DoTemplate helper, AddTemplateLoader
│   │   ├── tmpl_debug.go   # DebugTemplateLoader (on-disk templates, embedded fallback)
│   │   ├── nav.go          # NavElement (Weight-sorted), NavChild, AddNavElement
│   │   ├── users.go        # User CRUD, authwareBackend (bcrypt gameday backend)
│   │   ├── tokens.go       # APIToken model, token generation, AuthToken (Bearer), token admin handlers
│   │   ├── admin.go        # Admin UI handlers (profile list, permission assignment)
│   │   ├── login.go        # Login/landing views
│   │   ├── option.go       # WithDB, WithTemplateDebug, EventBus options
│   │   ├── util.go         # StrToUint, PageParams, Paginate scope
│   │   ├── error_templates_test.go
│   │   ├── tmpl_debug_test.go
│   │   ├── eventbus_test.go
│   │   └── ui/             # Embedded core web assets
│   │       ├── p2/         # Core templates: base.p2, fullscreen.p2, login.p2,
│   │       │               # fragments/nav.p2, view/admin/*, errors/*
│   │       └── static/     # css/theme.css (print + theme toggle switch), css/scorecard.css, js/mustache.js, js/eventbus.js, js/theme.js
│   ├── event/              # Publish/subscribe event bus + websocket export
│   │   ├── event.go        # Event, Envelope, system type constants
│   │   ├── bus.go          # Bus, New, Options, Publish/Subscribe/Close, replay ring
│   │   ├── handler.go      # ServeWS + Handler (coder/websocket, JSON wire format)
│   │   ├── bus_test.go
│   │   └── handler_test.go
│   ├── schedgen/           # Offline schedule generation
│   │   ├── common.go       # Config, DivisionConfig, Schedule, Round, Match, Location
│   │   │                  # Generator interface, GeneratorFactory, GenerateSchedule
│   │   ├── division.go     # GenerateDivisionSchedule (per-division + interleave)
│   │   ├── bid.go          # Balanced Incomplete Block Design generator
│   │   ├── random.go       # Random seeding generator
│   │   ├── oneshot.go      # One-shot generator
│   │   ├── score.go        # Schedule scoring (ClosestReplay, diversity metrics)
│   │   ├── bid_test.go     # BIBD smoke + larger team tests
│   │   └── division_test.go
│   └── buildinfo/
│       └── buildinfo.go    # Version, Commit, BuildDate (ldflags injection)
└── test_data/              # Schedule fixtures (YAML) + team import CSVs
```

## Architecture

### High-Level Flow

```
CLI (cobra) → Modules (chi routers) → Web Server (pkg/web) → Database (gorm + sqlite)
                ↕
        Schedule Generation (pkg/schedgen, offline "bake" command)
```

### CLI Layer (`internal/cmd/`)

- Uses **Cobra** for command structure.
- `main.go` calls `cmd.Entrypoint()`.
- `root.go` configures `slog` based on `LOG_LEVEL` env var, then executes the root command.
- Five top-level commands:
  - **`onsite`** — Web server operations (`serve` subcommand boots the full app in the `onsite` scope; supports `--debug` template debug mode)
  - **`cloud`** — Web server operations for the cloud entrypoint (`serve` subcommand is almost identical to `onsite serve` but resolves modules in the `cloud` scope, e.g. excludes the onsite-only `best` module; supports `--debug` template debug mode)
  - **`bake`** — Offline schedule generation (`bake schedule <gen> <rounds> <fields> <positions> <teams>`)
  - **`debug`** — Database manipulation tools (`debug game genscores <phase>`, `debug game lint <file>`, `debug best genscores`)
  - **`version`** — Print build info

### Web Server (`pkg/web/`)

- **Router**: Chi v5 (`github.com/go-chi/chi/v5`)
- **Templates**: Pongo2 v6 (`github.com/flosch/pongo2/v6`) loaded from embedded `pkg/web/ui/p2` directory; module template loaders are added at runtime via `AddTemplateLoader`
- **Static files**: Embedded `pkg/web/ui/static` served at `/static/*` via `http.FileServer`
- **Auth**: `github.com/the-maldridge/authware` with custom `gameday` bcrypt backend (htpasswd backend is also blank-imported in `onsite_serve.go`; `AUTHWARE_BASIC_MECHS` defaults to `"gameday"` if unset). The same `gameday` backend also implements `authware.TokenAuthenticator` for Bearer API tokens (`tokens.go`): `AUTHWARE_TOKEN_MECHS` defaults to `"gameday"` if unset, so `Authorization: Bearer <token>` requests are validated against the `api_tokens` table and resolve to the owning user's profile and permissions. Tokens are managed per-user under `/admin/user/{id}/tokens`.
- **Database**: `*gorm.DB` (via `*db.DB`) passed via `WithDB()` option

**Server bootstrap** (`server.go`):
1. Create Chi router
2. Set up Pongo2 with embedded templates (or `DebugTemplateLoader` when `--debug` is set)
3. Register template filters (`hasPermission`, `hasPermissionExact`)
4. Auto-migrate core tables (`Permission`, `Profile`, `User`, `APIToken`)
5. Install `CORE:ADMIN` permission (idempotent)
6. Create default admin user if none exist (uses `GAMEDAY_ADMIN_INIT_PW` or `"GameOn!"`)
7. Add Admin nav element (weight 99)
8. Initialize Authware with `gameday` backend, wire session + profile middleware
9. Mount static files and core routes (`/admin/*`, `/login`, `/logout`, `/ui`)

### Module System (`modules/`)

**Interface** (`modules/type.go`):
```go
type Web interface {
    Router() chi.Router
    Migrate() error
    NavList(string) []web.NavElement
    TemplateLoader() pongo2.TemplateLoader
}
```

**Registration** (`modules/registry.go`): Each module package registers itself from `init()`:
```go
modules.Register(modules.ModuleInfo{
    Name:         "game",
    Scopes:       []string{"onsite", "cloud"},
    Dependencies: []string{"team"},
    Required:     true,
    Factory: func(scope string, db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
        return New(scope, db, server, deps)
    },
})
```

- `ModuleInfo` declares `Name`, `Scopes` (entrypoints the module supports), `Dependencies` (module names that must be created first), `Required` (always loaded when scope matches, regardless of env), and `Factory`.
- `Register` panics on duplicate names or if a required module depends on a non-required one.
- `ResolveModules(scope, db, server)` filters by scope, applies the `GAMEDAY_MODULES` env var (comma-separated whitelist of optional modules), validates the dependency graph, topologically sorts (deterministic via alphabetical queue tie-breaks), and instantiates modules in order (passing the scope to each factory), returning a name-keyed `ModuleDeps` map.
- **Scope-conditional routes**: the scope is threaded factory → `New(scope, ...)` and each module registers routes conditionally on it when building its single router. A process runs exactly one scope, so the route sets are mutually exclusive per instance: the cloud-scoped `team` module registers only the cloud JSON endpoints (not the import handlers) and the cloud-scoped `game` module registers the shared configuration/setup screens plus `GET /cloud/setup` (not the scoreboard, timekeeping, match clock, or import routes). Nav lists follow suit (e.g. the cloud-scoped game module emits only the "Configuration" and "Setup" children; the team module omits the "Cloud Import" child on cloud).

**Current module registry**:

| Name | Scopes | Required | Dependencies | Permissions |
|------|--------|----------|--------------|-------------|
| `team` | onsite, cloud | yes | — | `TEAM:ADMIN` |
| `game` | onsite, cloud | yes | team | `GAME:ADMIN`, `GAME:SCHEDULE`, `GAME:REFEREE`, `GAME:TIMEKEEPER`, `GAME:SYNCHRONIZE` |
| `best` | onsite | no | game | `BEST:ADMIN` |
| `contest` | onsite, cloud | yes | team, game | `CONTEST:ADMIN` |

**Mounting** (`onsite_serve.go`): For each resolved module:
1. `w.Mount("/ui/mod/<name>", handle.Router())`
2. `w.AddNavElement(handle.NavList(prefix)...)`
3. `w.AddTemplateLoader(handle.TemplateLoader())`
4. `handle.Migrate()`

**Startup seeding** (`Migrate`): beyond `AutoMigrate`, each module guarantees its built-in rows on every boot (idempotent get-or-create): the `team` module seeds its static divisions plus the `Open` division, and the `game` module seeds a `Field` named `Field 1`.

**Dependency injection**: Modules access other modules through the `deps modules.ModuleDeps` map in their constructor, e.g. `deps["team"].(game.TeamModule)` in `game/module.go:332`. There is no functional-options layer for modules; constructors take positional `(scope, db, server, deps)` arguments.

**Tie-breakers**: `registry.go` also holds a global tie-breaker registry (`RegisterTieBreaker`/`GetTieBreaker`) for ranking tied teams.

### Navigation

`NavElement` carries a `Weight`; `AddNavElement` keeps the list sorted by descending weight. Current weights: Admin = 99, Team = 80, Game = 79. When adding a new module nav element, pick a weight that preserves the desired left-to-right order.

## Frontend (Bulma CSS)

The entire UI was ported to **Bulma CSS 1.0.4** (commit `616d610`):

- `pkg/web/ui/p2/base.p2` loads Bulma from the jsDelivr CDN (`bulma@1.0.4/css/bulma.min.css`) followed by `/static/css/theme.css`, then `/static/js/theme.js` (dark-mode toggle, applied before first paint), then includes `fragments/nav.p2`.
- `theme.css` holds print rules plus the theme-toggle switch styles (Bulma 1.0.4 ships no `.switch` component, so the switch is hand-rolled in `theme.css` using Bulma CSS variables). Custom layout is done with Bulma utility classes in templates.
- **Dark mode**: the nav bar carries a toggle switch next to the login/logout button (`fragments/nav.p2`, `#theme-toggle`). `js/theme.js` flips Bulma's dark mode by setting `data-theme="light"` or `data-theme="dark"` on `<html>` (per the Bulma dark-mode docs; the attribute is always set, never removed, so the choice overrides the system's `prefers-color-scheme` in both directions) and persists the choice in `localStorage` (`gameday-theme`). The script is portable (no framework, no build step) and is loaded in `<head>` of `base.p2` and `login.p2` so the persisted theme applies before first paint. The switch shows an inline sun SVG (light) or moon SVG (dark).
- `scorecard.css` remains for scorecard-specific styling (large score total, fieldset layout).
- The nav fragment uses Bulma's `navbar` components; page layout uses `section` > `#container` > `{% block content %}`.
- `fullscreen.p2` is an alternate layout used by the scoreboard (no nav).
- Standard error templates live at `pkg/web/ui/p2/errors/` (`internal.p2`, `unauthorized.p2`) and are rendered directly by handlers on failures.

**Template debug mode**: `gameday onsite serve --debug` enables `web.WithTemplateDebug`. In debug mode:
- Core templates load from the on-disk source tree (`pkg/web/ui/p2`) with a fallback to embedded templates, via `web.DebugTemplateLoader(dir, embedded)` (`pkg/web/tmpl_debug.go`), and the `TemplateSet` is in `Debug` mode so templates are re-ingested on every render.
- Each module's `TemplateLoader()` checks `ws.TemplateDebug()` and returns a `DebugTemplateLoader` rooted at its own `ui/p2` directory.

## Authentication & Authorization

### Library
- `github.com/the-maldridge/authware` — auth framework
- `golang.org/x/crypto/bcrypt` — password hashing

### Data Models

**`User`** (`pkg/web/users.go`):
- `ID uint`
- `Login string` (unique)
- `Secret []byte` (bcrypt hash)
- `ProfileID uint` → links to `Profile`

**`Profile`** (`pkg/web/middleware.go`):
- `ID uint`
- `Username string`
- `Permissions []Permission` (many-to-many via `profile_permissions` join table)

**`Permission`** (`pkg/web/authz.go`):
- `ID uint`
- `Module string` (e.g., `CORE`, `GAME`, `TEAM`, `BEST`)
- `Grant string` (e.g., `ADMIN`, `SCHEDULE`, `REFEREE`, `TIMEKEEPER`)

**`APIToken`** (`pkg/web/tokens.go`):
- `ID uint`
- `Label string` (operator-chosen display name)
- `Prefix string` (first 8 chars of the plaintext, for display)
- `Hash string` (unique; SHA-256 hex digest of the plaintext — the plaintext is never stored)
- `CreatedAt time.Time`
- `UserID uint` → links to `User`

A token authenticates as its owner: `AuthToken` resolves the digest to the owning `User`, and the existing profile middleware then applies that user's permissions to the request.

### Permission Hierarchy (`middleware.go:HasPermission`)

Three-tier check:
1. **Exact match** — `Module:Grant` exactly matches test
2. **Module admin** — `Module:ADMIN` covers any grant in that module
3. **Core admin** — `CORE:ADMIN` is universal access

### Template Filters

- `hasPermission(module, grant)` — hierarchical check (respects admin escalation)
- `hasPermissionExact(module, grant)` — exact match only

### Middleware

- `ProfileKey` context key: injects `*Profile` into request context
- `RequirePermission(permission)` — HTTP middleware for route protection
- `GuardRoute(permission, handler)` — wraps a single handler with permission checking (most common pattern in module routes)
- **Denial status codes**: both guards deny with an explicit status — `401` when the request has no `authware.User` (not logged in) and `403` when a user is present but lacks the permission. This matters for API consumers (e.g. the `pkg/cloud` client treats any 2xx as success, so a 200 denial would silently "succeed").
- **Denial body content negotiation**: when the request's `Accept` header includes `application/json` (the `pkg/cloud` client always sends it), the denial body is a JSON object `{"error": "<message>"}` with `Content-Type: application/json` (message: "Please log in to access this resource." for 401, "You do not have permission to access this resource." for 403). Otherwise the guards render the HTML `errors/unauthorized.p2` page. The cloud client's `NewErrorFromBody` parses a JSON `{"error": ...}` body into `Error.Message` (falling back to the trimmed raw body), so the push page's danger notification shows the clean message rather than a block of HTML.

### Admin UI

- User management: `/admin/user/` (list, create, password reset, delete)
- API tokens: `/admin/user/{id}/tokens` (list, create, delete). A token is a random `gd_`-prefixed value; only its SHA-256 hex digest is stored, the plaintext is shown exactly once on the create response (rendered directly, not a redirect), and a short prefix is kept for display. Deleting a user cascades to its tokens (deleted before the user for FK ordering on Postgres).
- Profile permissions: `/admin/profile/` → `/admin/profile/{id}/permissions`
- Permission assignment uses GORM `Association("Permissions").Replace()`

## Key Patterns

### Module Constructors

Module constructors are positional, not option-based; the first argument is the entrypoint scope (`"onsite"` or `"cloud"`), which the module uses to register scope-conditional routes:
```go
func New(scope string, db *db.DB, ws *web.Server, deps modules.ModuleDeps) *Module
```

`web.Server` still uses functional options: `WithDB(d *db.DB)`, `WithTemplateDebug(bool)`.

### Generic DB Helpers

`db.New(opts ...Option)` opens the connection. Options: `db.WithDriver(name)` selects the database type (`db.DriverSQLite` is the default; `db.DriverPostgres` is also supported), `db.WithPath(path)` and `db.WithEnvPath(name)` override the SQLite file path, and `db.WithDSN(dsn)` / `db.WithEnvDSN(name)` override the PostgreSQL DSN. Without options the default is SQLite at `GAMEDAY_DB` (default `gameday.db`). The driver is selected centrally by `GAMEDAY_DB_DRIVER` (default `sqlite`); when set to `postgres` the DSN is read from `GAMEDAY_DB_DSN`, so switching backends is a pure env-var change with no entrypoint or code changes (path-based options are ignored under Postgres). Entrypoints pick their own default path: `onsite serve` uses `db.WithEnvPath("GAMEDAY_ONSITE_DB")` (falls back to `GAMEDAY_DB`), while `cloud serve` defaults to `gameday_cloud.db`, overridable via `GAMEDAY_CLOUD_DB`.

`gorm.G[T](db)` used throughout for type-safe queries:
```go
teams, err := gorm.G[Team](db).Find(ctx)
```

`db.InsertOrUpdate[T](ctx, db, data)` performs upsert via `clause.OnConflict{UpdateAll: true}`.

`*db.DB` embeds `*gorm.DB`; use `.Raw()` to get the bare `*gorm.DB`.

### Embedded Assets

Each module embeds its own UI assets:
```go
//go:embed ui/*
var efs embed.FS
```

Templates live at `ui/p2/views/<module>/*.p2` (and `ui/p2/partials/` for shared fragments); the core server embeds `pkg/web/ui/*`.

### Template Injection (`tmpl.go:DoTemplate`)

Every template call via `DoTemplate` automatically injects:
- `user` — current `authware.User`
- `profile` — current `Profile`
- `navElements` — permission-filtered navigation
- `pageBase` — current request path

### Pagination (`util.go:Paginate`)

GORM scope: `db.Scopes(Paginate(r))` reads `?page=N&page_size=M` from query params (default: page=1, size=10, max=100). `PageParams(r)` returns the same clamped `(page, pageSize)` pair for use outside a GORM scope.

**gorm v1.31 generic gotcha**: `gorm.G[T](...)` re-sessions with `NewDB: true` internally, so WHERE/OFFSET/LIMIT applied to a raw `*gorm.DB` **before** `G[T]` are silently dropped. With the generic API, apply filters and paging through the chain itself: `gorm.G[T](db).Where(...).Offset((page-1)*size).Limit(size).Find(ctx)`.

**Admin team list** (`uiViewListTeams`, `modules/team`): server-side search + facet filters + pagination in one GET form. Query params: `?q=` (name/number `LIKE`), `division_id=`, `region_id=`, `page=`, `page_size=`. The `Count` (total) and paged `Find` share the same chain so they always agree; rows are ordered by `number ASC`. `filtersQS` is the full query minus `page` and is re-emitted on pager links (note: pongo2 HTML-escapes `&` in it, so the href shows `&amp;`). `q` is intentionally a simple `LIKE` (Option A) so it can be swapped for FTS5/typeahead later without a contract change; the roster query `ListTeams` does **not** use these filters.

### Schedule Generator Registry (`schedgen/common.go`)

Generators register themselves at init time:
```go
var generators map[string]GeneratorFactory
func RegisterGenerator(name string, factory GeneratorFactory)
func GenerateSchedule(gen string, cfg Config) (*Schedule, error)
```

`GeneratorConfig` providers (via `RegisterGeneratorConfig`/`GetConfig`) expose `MaxRounds`/`DefaultRounds`/`RoundsDynamic` for UI templating.

## Data Models

### Core (`pkg/web/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `User` | ID, Login (unique), Secret ([]byte), ProfileID | Authentication |
| `Profile` | ID, Username, Permissions ([]Permission) | Display name + permissions |
| `Permission` | ID, Module (string), Grant (string) | Module:Grant access control |
| `APIToken` | ID, Label, Prefix, Hash (unique, SHA-256 hex), CreatedAt, UserID (→ User) | Bearer token for API calls; plaintext never stored |

### Teams (`modules/team/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `Team` | ID, Name, Number (unique), DivisionID (→ Division), RegionID (→ Region), Disabled | Team record |
| `Division` | ID, Name (unique), Static, NoCompact | Team grouping; `Static` divisions are built-in, others start at ID 100 (`DynamicDivisionStartID`) |
| `Region` | ID, Name (unique) | Geographic grouping of teams |

### Game (`modules/game/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `MatchPlacement` | Round, Match, Phase, Team, Field, Position, State, ContestID | Team assigned to a match slot |
| `MatchScore` | Score, MatchPlacement, GamePhase, Team, ContestID | Score record |
| `Config` | Field (ConfigField), Game (Game) | Tournament configuration |
| `ClockSettings` | ID (fixed 1), Duration, Hurry (time.Duration), Transitions (bool), NextMatch (int), NextMatchPhase (uint) | Match clock config singleton (file-sourced `Duration`/`Hurry`/`Transitions` + runtime next-match pointer) |
| `ActiveClock` | ID (fixed 1), StartTime, EndTime, HurryTime (time.Time) | Running clock state singleton; `HurryTime = EndTime − Hurry` |

**MatchState enum**: `Scheduled`, `Queued`, `Running`, `Scorable`, `Complete`, `NoShow`, `Disqualified`

#### Match Clock (`clock.go`, `clock_match.go`)

The match clock's timing is **self-contained**: it owns exactly two singleton rows (`ClockSettings`, `ActiveClock`, both fixed ID=1), and its start/cancel/auto-stop logic does not depend on `MatchPlacement` or schedules. Optionally, when `ClockSettings.Transitions` is enabled, the clock drives the `MatchState` of the match in the active phase (see **Match transitions** below); with the flag off it behaves exactly as before.

- **Config**: the setup file's top-level `clock:` section (`duration`, `hurry`, both `time.Duration` YAML strings, e.g. `"2m30s"`) is persisted to `ClockSettings` on setup submit (`setup.go`). A setup file without `clock:` leaves settings untouched.
- **Routes**: `/ui/mod/game/timekeeping` (`GET` page; `POST start`/`POST cancel`/`POST transitions`/`POST next-match`), all behind `RequirePermission(GAME:TIMEKEEPER)`. Nav entry "Timekeeping" (weight 79, between Schedule and Scorecards). The `/clock` URL is intentionally unused.
- **Start**: valid only when configured (`Duration > 0`, `Hurry < Duration`) and no clock is running. When transitions are enabled the start first resolves a target match and applies the start transitions (below), so a failure there leaves no running clock behind. On success it sets `StartTime = now`, `EndTime = StartTime + Duration`, `HurryTime = EndTime − Hurry`, upserts the row, publishes `game.clock.start`.
- **Match transitions** (`clock_match.go`, behind `Transitions`): the clock drives the match states of the **active phase** (the `GamePhase` with `Active=true`; no active phase → start is rejected, transitions are a no-op). Only `Scheduled`/`Queued`/`Running` are driven; the post-run `Scorable` and the terminal `Complete`/`NoShow`/`Disqualified` are never touched.
  - **Target selection** (`clockMatchTarget`): the current match is the operator's pointer (`NextMatch`, applied only when `NextMatchPhase` matches the active phase) if that match still has a non-terminal placement, otherwise the lowest non-terminal match number; the next match is the lowest non-terminal match after the current one (0 when none).
  - **Start** (`applyClockMatchStart`): current → `Running`, next → `Queued`, and every other stale `Running`/`Queued` placement in the phase → `Scheduled` (one transaction). The pointer then advances to `next` (cleared when there is no match after the current one).
  - **End** (`clockEndMatchTransition`, fired from the end-time timer): the match currently `Running` in the active phase → `Scorable`. Errors are logged and never fail the caller, matching the publish-and-never-fail posture of the auto-stop path.
  - **Cancel** (`clockCancelMatchTransitions`): every `Running`/`Queued` placement in the active phase → `Scheduled`; a missing active phase is a no-op.
  - **Setup file wins**: a setup file's `clock:` section is the source of truth for `Duration`/`Hurry`/`Transitions` on submit (`setup.go`); a re-upload resets those file-sourced fields but preserves the runtime `NextMatch`/`NextMatchPhase` override (set from the timekeeping page). `POST transitions` toggles the flag; `POST next-match` sets or clears (`0`) the pointer.
- **Auto-stop**: on start, two one-shot goroutine timers arm for `HurryTime` and `EndTime`. Each re-checks `activeClockStill` (re-read the row, `StartTime.Equal`) before acting, so a cancelled or replaced run never fires. At `HurryTime` → publish `game.clock.hurry`. At `EndTime` → conditional delete keyed on `EndTime` (auto-stop) + publish `game.clock.end`. Cancel does a conditional delete keyed on `StartTime` + publishes `game.clock.cancel` (payload adds `cancel_time`).
- **Expired = idle**: everywhere the clock is considered running only while `now.Before(EndTime)`, so a stale row left over from a crash or restart is harmless.
- **Events**: all four carry `start_time`/`end_time`/`hurry_time` (RFC3339 JSON); `game.clock.cancel` additionally carries `cancel_time`. Published via `ws.Bus()` when configured (nil bus → no-op).
- **UI**: `partials/clock.p2` is an includable ticking fragment (JS ticks at 100 ms). Contract: `running` (bool), `duration_ms` (duration in **milliseconds**), `hurry_ms`/`end_ms` (epoch **milliseconds**). Display: `M:SS` over one minute, tenths under one minute (`59.8`); red hurry styling once `now >= hurry`; on expiry the readout holds `0.0` for 5 s, then resets to the configured duration. `views/game/timekeeping.p2` renders the fragment plus a Start button (or Cancel button + confirmation modal) gated on `profile | hasPermission:"GAME:TIMEKEEPER"`, and (when configured) a "Clock match transitions" settings panel with the `Transitions` checkbox and the next-match number input. The input shows the match the next run will actually act on (resolved the same way a start resolves it: operator pointer, then lowest non-terminal match via `displayClockNextMatch`), not the raw stored pointer; it shows `0` when the clock has run off the end of the schedule.

#### Phase Score Stats (`schedule.go`, `advancement.go`)

A dedicated per-phase page surfaces the `PhaseScoreStats` values (min, max, average, and the min/max placements with their team, match, and field).

- **Route**: `GET /ui/mod/game/schedule/phases/{id}/stats` (onsite-only, inside the `/schedule` group), guarded by `GAME:ADMIN` via `GuardRoute`.
- **Handler**: `uiViewPhaseScoreStats` loads the phase, calls `PhaseScoreStats`, and renders `views/game/phase_score_stats.p2` with `phase`, `stats`, and absolute back-link paths (`backSchedule` → the per-phase schedule, `backPhases` → the phase list, both built from `m.basePath`).
- **Columns**: the table shows Statistic, Score, Team, Match, and Field. The min and max rows show the source placement's match number and field name (preloaded via `PhaseScoreStats`); the average row shows `-` for Team, Match, and Field.
- **Link**: the phase list page (`phases.p2`) shows a "Stats" button in the Actions cell for each phase that has a schedule, gated on `profile | hasPermission:"GAME:ADMIN"`.
- **Empty phase**: a phase with no scores renders `-` for each row (the template guards on `stats.MinPlacement.Team.ID`).

#### Contest Keying (`module.go`, `export.go`)

Schedule and scorecard rows are keyed to the contest this onsite server is handling, so a site's data can be pushed to the cloud under the right contest (see Cloud Game Push below).

- **Columns**: `MatchPlacement.ContestID` and `MatchScore.ContestID` (both `gorm:"index"`, auto-added by the existing `Migrate()` AutoMigrate). `ContestID = 0` means no contest is selected on the onsite server. `ScorecardValue` carries no `ContestID` of its own; it is reached through `MatchPlacementID` and inherits the placement's contest scope.
- **Source of truth**: the contest module's `OnsiteContest` row (the locally-selected contest). The game module reads it **directly by table name** through a minimal local struct (`onsiteContestRow`, `TableName() = "onsite_contests"`) rather than importing the contest module, because the contest module already depends on the game module and a reverse import would create a dependency cycle. `selectedContestID` is best-effort: it returns the lowest-ID row, or `0` on any error (e.g. a cloud-scope server that never ran the contest module's migration), so a missing table never blocks schedule creation or score entry.
- **Stamping sites** (all three read the placement's or the selected contest's ID at creation time): the schedule preview (`uiViewPhaseSchedulePreview`, `schedule.go`) stamps `ContestID` on every candidate `MatchPlacement`; the scorecard submit (`uiViewScorecardSubmit`, `scorecard.go`) mirrors `placement.ContestID` onto the new `MatchScore`; the debug score generator (`DebugGenerateScores`, `debug.go`) does the same.
- **Export** (`ExportContestData(ctx, contestID)`, `export.go`): returns the contest's placements as portable `ContestPlacement` rows (round, match, phase, team ID + name, field, position, state, score, and scorecard `Values`), ordered by round, match, field, position. Placements without a score or values carry zero values and empty slices; an unknown contest ID yields an empty slice, not an error. This is the source for the cloud push (see Cloud Game Push below).

#### Division Awareness (`module.go`)

`Module.DivisionAware()` reports whether any `GamePhase` row has `DivisionAware: true` (a missing or unreadable phase table is treated as not division aware). It is the single source of truth for whether the game is division aware and drives the BEST module's awards (see BEST below): awards are ranked per division when any phase is division aware, otherwise as a single overall ranking.

#### Advancement Filters (`advancement.go`, `filters.go`)

`runAdvancementFilters` runs a phase's filters in order against one shared candidate pool. An exclude filter's removals join a blocklist that is stripped from the field every later filter sees. Each filter gets an `AdvancementFilterContext`:
- `Roster` is always the **full** enabled roster, so `len(Roster)` in a `SliceExpr` means the same thing as in a phase `Suppress` or filter `When` expression.
- `Remaining` is the roster minus the blocklist, and `Scoreboard` is filtered the same way.
- Filters that select from the roster use `sctx.Pool()` (which returns `Remaining`, or `Roster` when `Remaining` is unset), never `Roster` directly, so a blocked team cannot be re-advanced.

A phase only counts as complete once it has at least one finished placement, so a phase that would schedule zero teams must be suppressed (`Suppress`). Any later filter that reads from it must also be gated off with a matching `When`; otherwise the downstream phase can never be scheduled. `test_data/byte2bite.yml` does this for the Wildcard (`Suppress: "len(Roster) < 8"` paired with `When: "len(Roster) >= 8"` on the Semifinals `WildcardWinner` filter).

### BEST (`modules/best/`)

Onsite-only, optional module. Tracks non-match "BEST" scores per team and computes awards from them.

| Model | Fields | Purpose |
|-------|--------|---------|
| `ScoreType` | ID, Key (unique), Name, Max, Order | Score category (table `best_score_types`) |
| `TeamScoreValue` | ID, TeamID, ScoreTypeID, Value, SetAt | One team's value in one category; unique on (TeamID, ScoreTypeID) (table `best_score_values`) |

`Migrate()` seeds the four default categories (idempotent get-or-create, updates drifted Name/Max/Order): notebook (Max 300, Order 1), marketing (250, 2), poster (100, 3), video (100, 4).

**Awards** (`awards.go`, `GET /ui/mod/best/awards`, `BEST:ADMIN`-guarded; "Awards" nav child):
- **Registry**: `awardDefinitions()` returns the `[]Award` (notebook, marketing, poster, video, in display order). An `Award` is declarative: `Key`, `Name`, `Description`, a `Scorer`, `DivisionAware`, and `Order`. Adding an award is appending one `Award`; a new metric is a new `Scorer` (`Score(ctx, teams) (map[uint]float64, error)`, a team absent from the map is ineligible). The engine, handler, and template are shared by every award. The current scorer, `bestCategoryScorer`, scores a team by its `TeamScoreValue` in one category.
- **Division-awareness**: every award's `DivisionAware` is driven by the game module's `DivisionAware()` method (see Division Awareness above): awards are ranked per division when any `GamePhase` is division aware, otherwise a single overall ranking. The best module reaches the game module through a local `GameModule` interface type-asserted from `deps` (nil-safe: a missing game module falls back to overall ranking), and declares `Dependencies: ["game"]` in `init.go`.
- **Ranking** (`computeAwards`): candidates are grouped by division name when division aware (teams with no division land in an "Unassigned" group; the non-division-aware path is one "Overall" group). Within a group, teams sort by primary score, then the canonical tie-break chain notebook → marketing → poster → video (higher first), then team number (ordering only, never breaks a rank tie). Ranking is dense: co-winners (tied on primary and every tie-break value) share a rank and the next distinct rank is previous + 1. Each group exposes `Podium` (ranks ≤ 3, co-winners included), `Complete` (full ranking), and `More` (Complete extends past the podium).

### Contest (`modules/contest/`)

Dual-scope, required module. The cloud side manages contests (named, associated with a region, carrying a roster of teams); the onsite side exposes a "My Contest" screen where the operator picks which cloud contest this onsite server is handling.

| Model | Fields | Purpose |
|-------|--------|---------|
| `Contest` | ID, Name, RegionID (→ team.Region) | Contest record (cloud) |
| `ContestRoster` | ID, ContestID (→ Contest), TeamID (→ team.Team) | Roster row; unique on (ContestID, TeamID) (cloud) |
| `OnsiteContest` | ID (the cloud contest ID), Name, Region (plain strings) | Local record of the selected contest (onsite) |

**Roster** (`/{id}/roster` routes, read page public, mutations `CONTEST:ADMIN`-guarded):
- The roster is independent of the region: `POST /{id}/roster/init` seeds it from the contest's region (enabled teams only; idempotent, existing rows are skipped) and afterwards add/remove edits it directly.
- `GET|POST /{id}/roster/add` adds any team not already on the roster; `POST /{id}/roster/{team_id}/delete` removes one.
- Deleting a contest cascades to its roster rows.

**Cloud endpoints** (public, served by the cloud `Module`):
- `GET /ui/mod/contest/cloud/contests` → `[]cloud.ContestInfo` (cloud ID, name, region; ordered by name). The ID is carried on purpose: an onsite server adopts the cloud contest ID as its own local contest ID.
- `GET /ui/mod/contest/cloud/contests/{id}/roster` → `[]cloud.TeamInfo` (number, name, division, region) for that contest's roster.

**My Contest (onsite)** (`onsite.go`, `CONTEST:ADMIN`-guarded):
- `GET|POST /ui/mod/contest/` lists the cloud contests (fetched up front so cloud failures surface on the page) and persists the selection: the local `OnsiteContest` row's ID is the chosen cloud contest ID; re-selecting replaces the previous row.
- **Auto-import on selection** (`importContestData`, best-effort, log-and-continue so a transient cloud failure never blocks the selection): (1) if `ListTeams()` shows the local team list is empty, the selected contest's roster is pulled via `Client.GetContestRoster` and upserted through `team.Module.ImportTeams` (the same upsert the cloud/CSV imports use; blank division defaults to `Open`); (2) if no game is loaded (zero `GamePhase` rows), the cloud game file is pulled via `game.Module.ImportGame` (the same `applyGameConfig` as a local upload). The onsite module reaches team/game through local `TeamModule`/`GameModule` interfaces type-asserted from `deps` (nil-safe, mirroring the game module's `TeamModule` pattern), which is why `init.go` declares `Dependencies: ["team", "game"]`.

### Schedule Generation (`pkg/schedgen/`)

| Type | Fields | Purpose |
|------|--------|---------|
| `Config` | Fields, Positions, Teams, Rounds | Scheduler inputs |
| `DivisionConfig` | ID, Fields (pinned, empty = auto), Rounds (0 = global), TeamIndices, NoCompact | Division-aware scheduling inputs |
| `Schedule` | Config, Rounds, score metrics, RoundsDynamic, Interleaved | Generated schedule |
| `Round` | Matches ([]Match), TeamAppearances | Single round |
| `Match` | Placements (map[Location]int), DivisionID | Match with team assignments |
| `Location` | Field (int), Position (int) | Field + position pair |

### Build Info (`pkg/buildinfo/`)

| Variable | Default | Injected via |
|----------|---------|--------------|
| `Version` | `"dev"` | `-ldflags -X ...Version=<ver>` |
| `Commit` | `"UNKNOWN"` | `-ldflags -X ...Commit=<hash>` |
| `BuildDate` | `"UNKNOWN"` | `-ldflags -X ...BuildDate=<date>` |

## Scheduling

### Generators

| Name | Algorithm | Notes |
|------|-----------|-------|
| `BID` | Balanced Incomplete Block Design | Pairwise coverage; `RoundsDynamic=true` |
| `RandomSeeding` | Random team assignment | Simple random placement |
| `OneShot` | One-shot generation | Single-pass algorithm |

### Division-Aware Scheduling

`GenerateDivisionSchedule(generator, cfg, divisions)` (`division.go`):
1. Generates an independent schedule per division.
2. Pins divisions to fields (`DivisionConfig.Fields`) or auto-assigns remaining fields evenly (scarce fields are shared across divisions).
3. Interleaves division schedules round-by-round, remapping local field indices to global pinned fields and tagging each match with its `DivisionID`.
4. Greedily compacts matches that use non-overlapping fields unless the division's `NoCompact` is set.

The result is flagged `Interleaved: true`; per-team round-count validation happens pre-interleave per division. The game module's schedule UI (`schedule.go`) builds `DivisionConfig` values from `team.Division` rows (including per-division `NoCompact`) and calls this.

### CLI Usage

```bash
gameday bake schedule <generator> <rounds> <fields> <positions> <teams>
```

Output rendered via `github.com/jedib0t/go-pretty/v6/table`.

### Scoring Metrics (`score.go`)

`Schedule.Score()` evaluates:
- **ClosestReplay** — minimum match gap between any team's appearances
- **WorstLocationDiversity** — distinct (field, position) pairs per team
- **WorstCompetitorDiversity** — distinct opponent teams per team
- **TeamAvgScore** — aggregated team quality score

`Schedule.Validate()` ensures every team plays the expected number of rounds.

## Event Bus (`pkg/event/`)

`pkg/event` implements a framework-agnostic publish/subscribe event bus used for real-time updates. It has no web/HTTP dependency in the core (only stdlib + `github.com/coder/websocket` for the websocket export).

**Core types** (`event.go`):
- `Event{Type string, Data any}` — what producers publish. Types are dot-separated strings, e.g. `game.match.state` (convention: `<module>.<noun>.<fact>`). Reserved system types: `subscribed`, `close`.
- `Envelope{Seq uint64, TS time.Time, Type string, Data json.RawMessage, Replay bool}` — what consumers receive and the JSON wire format. `Seq` is a monotonically increasing per-bus sequence number; consumers can detect gaps (dropped events) from it.

**Producer API** (`bus.go`):
```go
if ws.Bus() != nil {
    ws.Bus().Publish(ctx, event.Event{Type: "game.match.state", Data: payload})
}
```
`Publish` marshals `Data` to JSON eagerly (fail fast) and never blocks on slow consumers. Returns `event.ErrClosed` if the bus is shut down, `event.ErrBadType` for empty/reserved types.

**Consumer API (Go)**:
```go
ch, _ := ws.Bus().Subscribe([]string{"game.match.state"}) // nil = all types
```
Each subscriber gets a buffered channel (default 256). If a subscriber's buffer is full, events are dropped for that subscriber only (logged via slog); producers and the dispatcher are never blocked.

**Websocket export** (`handler.go`): mounted at `/ui/events` (open by default, like the public scoreboard data endpoint; wrap in `RequirePermission` to restrict). Query params:
- `types` — comma-separated filter (empty = all)
- `replay` — number of recent envelopes to replay on connect (each flagged `replay: true`); backed by an all-types ring buffer (default 256, `event.WithReplaySize(0)` disables)

Every connection receives a `subscribed` envelope first (`{"types":[...],"seq":N}`), then replay (if requested), then live events. On bus shutdown a `close` envelope is sent and the connection closes cleanly.

**Server wiring**: `web.EventBus(bus)` option attaches the bus; `Server.Bus()` returns it (nil if not configured — modules must nil-check). `onsite_serve.go` creates the bus, passes the option, handles `/ui/events` via `bus.Handler()`, and calls `bus.Close()` on shutdown. Modules reach the bus through the `*web.Server` they already hold; no module needs to import `pkg/event` unless it consumes events.

**Browser client** (`pkg/web/ui/static/js/eventbus.js`, served at `/static/js/eventbus.js`):
```js
const bus = createEventBus({ url: '/ui/events', types: ['game.match.state'], replay: 100, onEvent, onStatus });
bus.close();
```
Vanilla WebSocket; pings every 15s, gap-detects via `seq`, reconnects with exponential backoff + jitter.

## Cloud Client (`pkg/cloud/`)

`pkg/cloud` is the HTTP client for talking to the cloud instance. It is stdlib-only (no new `go.mod` dependencies) and is the single place onsite→cloud communication happens; modules never issue outbound HTTP directly.

- **Base URL**: `New(opts ...Option)` reads `GAMEDAY_CLOUD` (fallback `DefaultBaseURL = "https://gameday.gizmoplatform.org"`) and trims trailing slashes. `WithBaseURL` overrides the env var; `WithTimeout` overrides the 10 s default. `Client.BaseURL()` exposes the resolved base.
- **Auth seam**: `Authenticator` is a single-method interface (`Apply(ctx, *http.Request) error`) installed via `WithAuthenticator`, or the `WithBasicAuth` / `WithBearerToken` shortcuts which wrap the ready `BasicAuth{Username, Password}` (sets `Authorization: Basic ...`) and `BearerAuth{Token}` (sets `Authorization: Bearer ...`) types. When no authenticator option is given, `New` falls back to `GAMEDAY_CLOUD_TOKEN`: if set, every request carries `Authorization: Bearer <token>`; an explicit option always wins over the env var.
- **Wire**: `Do(ctx, method, path, query, out)` builds `base + path + query`, sets `Accept: application/json`, applies the authenticator when set, and decodes the JSON body into `out` (`nil` → discarded). `DoBytes(ctx, method, path, query)` returns the full raw body untouched, for non-JSON payload types (the game setup YAML). `DoJSON(ctx, method, path, query, body, out)` is the body-capable variant: it marshals `body` to JSON, sets `Content-Type: application/json` (only when `body` is non-nil), and decodes the response into `out`. Non-2xx responses return `*cloud.Error` (status + trimmed first 512 bytes of body).
- **Per-feature convention**: each cloud feature owns a sibling file in the package with its wire types and methods (e.g. `teams.go` with `RegionInfo`/`TeamInfo` + `GetRegions`/`GetRegionTeams`; `games.go` with `GetGameFile`; `scores.go` with `ContestPlacement` + `PushContestData`). The game feature deliberately has no client-side wire types for the setup file: the raw YAML setup file is the single source of truth and is applied by the same parser/upsert as a local file upload. Add the next feature as a new file, not by growing `client.go`.

### Team Cloud Endpoints (served by a cloud instance)

The `team` module exposes two public read-only JSON endpoints, keyed to the `pkg/cloud` wire shapes:

- `GET /ui/mod/team/cloud/regions` → `[]RegionInfo` (name + team count; includes disabled teams and zero-team regions; ordered by name).
- `GET /ui/mod/team/cloud/teams?region=X` → `[]TeamInfo` (number, name, division, region; unknown region → 404 `{"error":"region not found"}`; omit `region` for every team; ordered by number).

### Cloud Team Import (onsite)

`GET/POST /ui/mod/team/import-cloud` (both `TEAM:ADMIN`-guarded; a "Cloud Import" child sits in the Team nav next to "Bulk Import") lists the cloud regions with per-region counts, and a per-region POST pulls the roster via `Client.GetRegionTeams` and applies it through `upsertTeamRows` — the same upsert the CSV bulk import uses. The upserts on team number, get-or-creates the divisions/regions the rows reference, defaults a blank division to `Open`, and never writes `disabled` (a re-import cannot resurrect a manually-disabled team). Success → 303 to `?imported=N`; a cloud failure renders the import page with an error notification and writes nothing.

Note: `GAMEDAY_CLOUD` is the **base URL of the cloud instance the client dials**; `GAMEDAY_CLOUD_DB` is the **local SQLite file path** `cloud serve` stores its database at. They are distinct — do not conflate.

Also note route registration is scope-exclusive (see Scope-conditional routes above): the factory receives the entrypoint scope and the cloud-scoped module registers the shared management screens plus the cloud pull endpoints, so the cloud JSON endpoints do not exist on onsite instances and the import UI does not exist on cloud instances. The same applies to the game module's cloud endpoints and import UI.

### Game Cloud Screens (served by a cloud instance)

The cloud-scoped `game` module also serves the shared management screens: `GET /ui/mod/game/active-configuration` (Configuration, `GAME:ADMIN`-guarded) displays the active game config, and `GET/POST /ui/mod/game/setup` (Setup, `GAME:ADMIN`-guarded) uploads a game setup file through the same `applyGameConfig` as the onsite local upload. The cloud instance is where the game design is authored and maintained before it is pulled to the sites.

### Game Cloud Endpoints (served by a cloud instance)

The `game` module exposes two cloud endpoints. `GET /ui/mod/game/cloud/setup` returns the configured game as a `text/yaml` document in the same shape the local setup upload accepts (`Field.Positions`, `Game.Phases` with `AdvancementFilters`, `Game.Elements` with `States`/`Values`, and an optional `Clock` section). It is public on purpose: the game file is game design, not sensitive data, and is pulled by other Gameday systems. Runtime state is stripped so a re-import carries the design, not live operations: phase `Active`/`Frozen`/`HideScores` are zeroed, and the clock's runtime `NextMatch`/`NextMatchPhase` pointer is never exported (the clock section is omitted entirely when no clock is configured). `POST /ui/mod/game/cloud/contests/{id}/scores` stores a site's schedule and scorecard data for one contest (see Cloud Game Push below); it is guarded by `GAME:SYNCHRONIZE` (satisfied by `GAME:ADMIN` via module-admin escalation) since it writes to the cloud's stored data.

### Cloud Game Import (onsite)

`GET/POST /ui/mod/game/import-cloud` (both `GAME:ADMIN`-guarded; a "Cloud Import" child sits in the Game nav under "Setup") fetches the YAML via `Client.GetGameFile` (the GET pre-fetches so cloud failures surface on the page) and applies it through `applyGameConfig` — the same upsert the local setup file upload uses (positions, phases + advancement filters, elements + states, and the clock when the file carries a `Clock` section). `ScorecardElement` rows are not in the file and are regenerated via `exprForElementState`. Success → 303 to `?imported=1`; a cloud or parse/apply failure renders the import page (or `errors/internal.p2` for parse/apply) with an error and writes nothing.

Note: `applyGameConfig` inserts each phase **before** its advancement filters. Postgres enforces the `game_phase_id` foreign key on `game_phase_advancement_filters`, so inserting a filter before its phase fails (SQLite does not enforce FKs, which is why the original ordering bug only surfaced on Postgres). Filter IDs are assigned sequentially across phases in YAML order, so re-imports stay idempotent.

### Cloud Game Push (onsite)

`GET/POST /ui/mod/game/push-cloud` (both `GAME:ADMIN`-guarded; a "Cloud Push" child sits in the Game nav) pushes the selected contest's schedule and scorecard data to the cloud instance. The push is a single `POST /ui/mod/game/cloud/contests/{id}/scores` (via `Client.PushContestData`) with a bare `[]cloud.ContestPlacement` array in the body; the contest ID in the URL is authoritative. The cloud endpoint is guarded by `GAME:SYNCHRONIZE` (see Game Cloud Endpoints above), so the cloud instance must have a profile with that grant (or `GAME:ADMIN`) to accept pushes. The cloud endpoint stores the data via delete-then-create in a transaction keyed by the URL contest ID, so a re-push replaces the stored data in place. The onsite push guards on `selectedContestID != 0` (no contest selected → error, nothing sent). Success → 303 to `?pushed=1`; a cloud failure renders the push page with an error notification and is logged via `slog.Error` (with the contest ID). The cloud endpoint writes a JSON `{"error": ...}` body on each failure path (bad contest ID, decode error, transaction error, each also logged via `slog.Error`), so the message round-trips through `cloud.Error.Message` to the onsite push page. The cloud side is store-only this phase (no read-back endpoint).

The wire types live in `pkg/cloud/scores.go` (`ContestPlacement` with `State` as a plain `uint`, since the wire type cannot reference the game module's `MatchState` enum; `ContestScorecardValue`). The onsite `PushContestData` method converts `[]game.ContestPlacement` (from `ExportContestData`) to `[]cloud.ContestPlacement` and calls the client. The cloud storage models (`CloudMatchPlacement`, `CloudScorecardValue`) live in the game module and are migrated in both scopes (empty on onsite, harmless).

### Cloud Scorecards Lookup (cloud)

`GET /ui/mod/game/scorecards` (cloud scope only; a "Scorecards" child sits in the Game nav) is a read-only lookup of the pushed scorecards (`CloudMatchPlacement` + `CloudScorecardValue`), keyed by team. The page is **unguarded** (open to any authenticated user, like the team list) and uses the same filtering mechanism as the team list page: a single `cond`/`args` pair shared by the `Count` and paged `Find` on the `gorm.G[CloudMatchPlacement]` chain, `web.PageParams` paging, a radius-2 `pageWindow`, and `filtersQS` (query minus `page`) preserved in the pager links.

- **Filters** (query params, all optional, combined with AND): `q` searches `team_name LIKE %q% OR CAST(team_id AS TEXT) LIKE %q%`; `contest_id` is an exact facet; `state` is an exact facet on the stored `uint` state.
- **Facet options**: the contest dropdown lists distinct `contest_id` values from the placements, with names read best-effort from the `contests` table via a local `contestNameRow` struct (`TableName() == "contests"`, so the game module never imports `modules/contest`); a missing table or row falls back to `Contest <id>`. The state dropdown lists the package-level `MatchStates` slice.
- **Columns**: Team, Contest (best-effort name from the `contests` table, `Contest <id>` fallback), Round (stored zero-indexed, displayed with `|add:1` in the template, matching the onsite schedule page's one-based display), Match, State (pre-computed `MatchState(p.State).String()`, since pongo2 cannot do the conversion), Score, and a View button linking to the detail page. The per-element scorecard values are deliberately not shown here; they live on the detail page.
- **Detail page**: `GET /ui/mod/game/scorecards/{id}` (also unguarded) renders one pushed scorecard read-only using the existing onsite scorecard template (`views/game/scorecard.p2` → `partials/scorecard.p2`) with `edit_disable: true` (inputs disabled, no Submit button). The cloud stores only the site's own IDs, so the `MatchInfo` the template expects is synthesized from the `CloudMatchPlacement` with best-effort name lookups against the cloud's own game design (`GamePhase`/`Field`/`FieldPosition` by the stored ID); a miss falls back to an ID-based label (`Phase <id>`, `Field <id>`, `Position <id>`, default `Color2` `cccccc`). The element design is the cloud's `GameElement` (preloaded `States`/`States.Values`) and the values are the placement's `CloudScorecardValue` rows keyed `EID_SID` into `ScorecardData`; a pushed element key not in the current design simply renders no widget. A missing placement renders `errors/internal.p2` (HTTP 200, like every `DoTemplate` error path).
- **Chain gotcha**: `gorm.G[T]().Where()` returns `ChainInterface[T]`, not `Interface[T]`, so the shared chain is seeded with a no-op `Scopes(func(*gorm.Statement) {})` to obtain a `ChainInterface[T]` base (`CloudMatchPlacement` has no relations, so `Preload` is not a safe substitute).
- **Template**: `views/game/cloud_scorecards.p2` mirrors `team/list.p2` (GET filter form with no `page` field, total line, empty state, inline Bulma pager).

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/flosch/pongo2/v6` | Templating engine |
| `gorm.io/gorm` + `gorm.io/driver/sqlite` + `gorm.io/driver/postgres` | ORM + SQLite/PostgreSQL drivers |
| `github.com/the-maldridge/authware` | Authentication framework |
| `github.com/coder/websocket` | Websocket server (event bus export) |
| `github.com/jedib0t/go-pretty/v6` | Table rendering (CLI) |
| `golang.org/x/crypto/bcrypt` | Password hashing |
| Bulma CSS 1.0.4 (CDN) | Frontend UI framework |

## Build & Deploy

### Dockerfile

- **Base**: `golang:1.25-alpine` → `alpine` multi-stage build
- **CGO**: Enabled for SQLite (`CGO_ENABLED=1`)
- **Static linking**: `-extldflags "-static"`
- **Entrypoint**: `tini-static` init system
- **Data dir**: `ENV GAMEDAY_DB=/data/gameday.db`
- **Volume**: Expected mount at `/data`

### Build Flags

```bash
ldflags="-X github.com/gizmo-platform/gameday/pkg/buildinfo.Version=1.0.0 \
         -X github.com/gizmo-platform/gameday/pkg/buildinfo.Commit=abc1234 \
         -X github.com/gizmo-platform/gameday/pkg/buildinfo.BuildDate=2026-01-01"
```

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `GAMEDAY_DB` | `gameday.db` | SQLite database path (fallback default for all entrypoints) |
| `GAMEDAY_DB_DRIVER` | `sqlite` | Database driver: `sqlite` or `postgres` (see Generic DB Helpers) |
| `GAMEDAY_DB_DSN` | — | PostgreSQL DSN, used when `GAMEDAY_DB_DRIVER=postgres` (e.g. `postgres://user:pass@host:5432/db?sslmode=...`) |
| `GAMEDAY_ONSITE_DB` | — | SQLite path for `onsite serve` (overrides `GAMEDAY_DB` when set) |
| `GAMEDAY_CLOUD_DB` | `gameday_cloud.db` | SQLite path for `cloud serve` (local DB file; distinct from `GAMEDAY_CLOUD`, which is the base URL the `pkg/cloud` client dials) |
| `GAMEDAY_CLOUD` | `https://gameday.gizmoplatform.org` | Base URL of the cloud instance for the `pkg/cloud` client (see Cloud Client) |
| `GAMEDAY_CLOUD_TOKEN` | — | Bearer token attached to every `pkg/cloud` client request when set; an explicit `WithAuthenticator`/`WithBasicAuth`/`WithBearerToken` option takes precedence |
| `GAMEDAY_PORT` | `8080` | Port for `onsite serve` / `cloud serve` |
| `GAMEDAY_MODULES` | — | Comma-separated whitelist of optional modules to load in addition to required ones |
| `LOG_LEVEL` | — | `slog` level (DEBUG, INFO, WARN, ERROR) |
| `GAMEDAY_ADMIN_INIT_PW` | `"GameOn!"` | Default admin password on first boot |
| `AUTHWARE_BASIC_MECHS` | `gameday` | Authware backend selection (set automatically if unset) |
| `AUTHWARE_TOKEN_MECHS` | `gameday` | Authware token (Bearer) backend selection (set automatically if unset) |

## Testing

- **Location**: `pkg/cloud/client_test.go`, `pkg/db/db_test.go`, `pkg/schedgen/bid_test.go`, `pkg/schedgen/division_test.go`, `pkg/event/bus_test.go`, `pkg/event/handler_test.go`, `pkg/web/error_templates_test.go`, `pkg/web/tmpl_debug_test.go`, `pkg/web/eventbus_test.go`, `pkg/web/users_test.go`, `pkg/web/tokens_test.go`, `modules/game/lint_test.go`, `modules/game/advancement_test.go`, `modules/game/clock_test.go`, `modules/game/clock_match_test.go`, `modules/game/cloud_test.go`, `modules/game/cloud_scorecards_test.go`, `modules/game/contest_key_test.go`, `modules/game/push_test.go`, `modules/game/division_aware_test.go`, `modules/game/byte2bite_test.go`, `modules/team/team_test.go`, `modules/team/region_test.go`, `modules/contest/contest_test.go`, `modules/contest/onsite_test.go`, `modules/best/scores_test.go`, `modules/best/awards_test.go`, `modules/best/advancement_test.go`, `modules/best/final_test.go`, `modules/best/semifinal_test.go`
- **Pattern**: Table-driven / config-driven tests using `schedgen.Config`; in-DB tests build a module via `newTestModule(t, opts...)` (a fresh temp-dir SQLite DB + a `web.Server`, optionally with `web.EventBus`) and exercise handlers or bus events directly
- **Coverage**: BIBD smoke test (v=7, k=3 Steiner Triple System), larger team test (13 teams), degenerate case (teams < block size), division interleaving, error template rendering, debug template loading, advancement filters (roster/scoreboard sourcing, exclude-then-include, tie-breaking, `SliceExpr` `len(Roster)` sees the full roster after an exclude, a `Roster` include does not re-advance blocked teams), byte2bite phase flow (6 teams: Wildcard suppressed and Semifinals schedulable straight from Seeding with all 6; 10 teams: Wildcard takes the 3 non-top-7 teams and Semifinals take 8; 40 teams: Wildcard takes 8 and Semifinals take 16; uses a stub `BESTNotebook` filter), phase score stats (min/max/avg with the MatchPlacement reported for the extremes, empty-phase zero result, lowest-ID tie-break; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set), phase score stats page (admin render with min/max team names, scores, and source Match/Field cells, GAME:ADMIN guard blocks non-admins, back links resolve), default field seeding (Migrate guarantees a `Field 1` row and is idempotent on re-run), match clock (YAML parse, settings round-trip, start/cancel, auto-stop with hurry/end events, guard against double-start/cancel-when-idle, page rendering), clock-driven match transitions (target selection, start/end/auto-stop/cancel state flow, operator next-match override, flag-off no-op, Scorable-left-alone, stale Running/Queued reset, start rejection paths, transitions panel round-trip), admin team list (pagination, division/region facets, combined filters, filtered pagination, filter state preserved in pager links, no-match empty state), db driver selection (postgres-without-DSN error, unknown-driver error, env-gated Postgres integration test on `GAMEDAY_TEST_PG_DSN`: migrate + upsert round-trip), user deletion (deletes the user's API tokens, then the user, clears the profile's `profile_permissions` join rows, then deletes the profile; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set, where the old profile-first ordering fails with an FK violation), API tokens (token authenticator resolves a valid token to the owning user's login and rejects unknown tokens, user delete cascades to the user's tokens, token create stores only the hash and renders the one-time plaintext, end-to-end Bearer auth through the full router: a valid token for the bootstrap admin renders the CORE:ADMIN-guarded user list, a bogus token gets 401, and no credentials get the unauthorized page), game cloud import Postgres FK (multi-phase `byte2bite.yml` fixture with advancement filters imports cleanly on Postgres, where the `game_phase_id` FK is enforced; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set), cloud client (JSON decode, query encoding, error vs network-failure distinction, env/default base URL, basic-auth header, bearer-token header, env-var bearer token (GAMEDAY_CLOUD_TOKEN) with explicit-option precedence, regions/teams round-trips, raw-bytes game file round-trip, JSON error body parsing (the `error` field is extracted into `Error.Message`, non-JSON bodies fall back to the trimmed raw body)), team cloud import (public regions/teams JSON endpoints incl. unknown-region 404, end-to-end region import with division defaulting, idempotent re-import that updates in place without duplicating and preserves `disabled`, cloud-failure path that writes nothing), game cloud import (end-to-end import via shared `applyGameConfig` incl. advancement filter, regenerated scorecard exprs, and clock section, idempotent re-import, cloud-failure path that writes nothing, public YAML export with runtime state stripped and round-trip parse, cloud-scoped configuration/setup screens incl. setup upload round-trip and onsite-only routes staying unregistered), contest (cloud contest list + per-contest roster JSON endpoints, My Contest selection storing the cloud ID and re-select replacing the row, CONTEST:ADMIN guard, selection auto-import: roster imported when the local team list is empty and skipped when teams exist, cloud game imported when no `GamePhase` rows exist and skipped when a game is loaded, all against a fake cloud server), contest keying (schedule preview candidate placements carry the selected contest ID, zero when no contest is selected, scorecard submit mirrors the placement's ContestID onto the MatchScore, ExportContestData returns portable placements with team name, score, and scorecard values for a contest and an empty slice for an unknown contest ID), cloud game push (end-to-end push with fake cloud server verifying path, payload fields, and success redirect, cloud endpoint stores placements + scorecard values keyed by contest ID, idempotent re-push replaces in place, GAME:SYNCHRONIZE guard rejects ungranted and unauthenticated profiles without writing (JSON `{"error": ...}` denial body for `Accept: application/json` requests, HTML page otherwise), no-contest guard shows warning/danger and sends nothing, cloud-failure path shows danger with the cloud's error message, unauthenticated push against a real cloud instance (no token) surfaces the guard's 401 as a clean danger error (the parsed JSON message, not the HTML denial page) instead of a false success, cloud endpoint writes a JSON error body on bad contest ID and malformed body without storing, DoJSON round-trip with Content-Type, nil-body sends no Content-Type, PushContestData client round-trip), cloud scorecards lookup (empty state, list with team/contest/placement columns and a View button, contest facet name from the contests table with fallback, team name/number search, contest and state facets, combined filters, pagination with pager links, filter state preserved in pager links, unguarded page accessible to a bare profile, detail page rendering the existing scorecard template read-only with the pushed values and disabled inputs, detail page for a missing placement rendering the internal error page), game division awareness (`DivisionAware()` false with no phases and with only non-division-aware phases, true once a `DivisionAware: true` phase exists), BEST awards (per-division ranking when a division-aware phase exists, single "Overall" ranking when none does, dense ranking with co-winners sharing a rank, canonical tie-break chain notebook → marketing → poster → video with team number as ordering-only last resort, unassigned-division group, podium/complete split, awards page render and BEST:ADMIN guard)
- **Run**: `go test ./...`

### Test Fixtures

`test_data/*.yml` — YAML schedule test fixtures for 2-player and 4-player configurations, with/without objectives and divisions, plus `byte2bite.yml` (a 4-phase production game setup file with advancement filters and a Wildcard suppression for events under 8 teams, used by the Postgres FK import test and the byte2bite phase-flow tests). `test_data/*.csv` — team import fixtures (12 and 24-team datasets).

## Conventions

- **Naming**: Go standard (`CamelCase` types, `lowerCamelCase` locals, `PascalCase` exported)
- **Errors**: Custom error types (`UnknownGenerator`, `InvariantViolation`) with `Error() string`
- **Logging**: `log/slog` throughout; no structured logging library
- **HTTP handlers**: `uiView<Name>`, `uiView<Name>Submit` pattern
- **Templates**: `.p2` extension (Pongo2), per-module `ui/p2/views/<module>/` layout, Bulma CSS classes for styling
- **Pagination**: Consistent `Paginate(r)` scope usage
