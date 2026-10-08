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
│   │   ├── advancement.go  # Team advancement logic
│   │   ├── lint.go         # LintConfig: setup-file validation (expr, references)
│   │   ├── filters.go      # Scoreboard filtering
│   │   ├── filter_scoreboard.go
│   │   ├── debug.go        # Module-level debug endpoints
│   │   ├── util.go
│   │   └── ui/p2/          # Embedded templates (views/game/* incl. import_cloud, partials/scorecard*)
│   └── best/
│       ├── init.go         # Module registration ("best", optional)
│       ├── module.go       # BEST:ADMIN permission, scores routes
│       ├── scores.go       # Scores view
│       ├── type.go         # ExternalScores model + max score constants
│       ├── debug.go        # DebugGenerateScores
│       └── ui/p2/views/best/scores.p2
├── pkg/
│   ├── cloud/              # Client for the cloud instance (base URL, auth seam, per-feature files)
│   │   ├── client.go       # Client, New, Do/DoBytes, options (WithBaseURL/WithTimeout/WithAuthenticator/WithBasicAuth)
│   │   ├── auth.go         # Authenticator interface + ready BasicAuth
│   │   ├── teams.go        # RegionInfo/TeamInfo wire shapes, GetRegions/GetRegionTeams
│   │   ├── games.go        # GetGameFile (raw YAML bytes from the public game setup endpoint)
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
│   │       └── static/     # css/theme.css (print), css/scorecard.css, js/mustache.js, js/eventbus.js
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
- **Auth**: `github.com/the-maldridge/authware` with custom `gameday` bcrypt backend (htpasswd backend is also blank-imported in `onsite_serve.go`; `AUTHWARE_BASIC_MECHS` defaults to `"gameday"` if unset)
- **Database**: `*gorm.DB` (via `*db.DB`) passed via `WithDB()` option

**Server bootstrap** (`server.go`):
1. Create Chi router
2. Set up Pongo2 with embedded templates (or `DebugTemplateLoader` when `--debug` is set)
3. Register template filters (`hasPermission`, `hasPermissionExact`)
4. Auto-migrate core tables (`Permission`, `Profile`, `User`)
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
| `game` | onsite, cloud | yes | team | `GAME:ADMIN`, `GAME:SCHEDULE`, `GAME:REFEREE`, `GAME:TIMEKEEPER` |
| `best` | onsite | no | — | `BEST:ADMIN` |

**Mounting** (`onsite_serve.go`): For each resolved module:
1. `w.Mount("/ui/mod/<name>", handle.Router())`
2. `w.AddNavElement(handle.NavList(prefix)...)`
3. `w.AddTemplateLoader(handle.TemplateLoader())`
4. `handle.Migrate()`

**Dependency injection**: Modules access other modules through the `deps modules.ModuleDeps` map in their constructor, e.g. `deps["team"].(game.TeamModule)` in `game/module.go:332`. There is no functional-options layer for modules; constructors take positional `(scope, db, server, deps)` arguments.

**Tie-breakers**: `registry.go` also holds a global tie-breaker registry (`RegisterTieBreaker`/`GetTieBreaker`) for ranking tied teams.

### Navigation

`NavElement` carries a `Weight`; `AddNavElement` keeps the list sorted by descending weight. Current weights: Admin = 99, Team = 80, Game = 79. When adding a new module nav element, pick a weight that preserves the desired left-to-right order.

## Frontend (Bulma CSS)

The entire UI was ported to **Bulma CSS 1.0.4** (commit `616d610`):

- `pkg/web/ui/p2/base.p2` loads Bulma from the jsDelivr CDN (`bulma@1.0.4/css/bulma.min.css`) followed by `/static/css/theme.css`, then includes `fragments/nav.p2`.
- `theme.css` is now minimal (print-only rules); the old hand-rolled theme CSS was removed. Custom layout is done with Bulma utility classes in templates.
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

### Admin UI

- User management: `/admin/user/` (list, create, password reset, delete)
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

### Teams (`modules/team/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `Team` | ID, Name, Number (unique), DivisionID (→ Division), RegionID (→ Region), Disabled | Team record |
| `Division` | ID, Name (unique), Static, NoCompact | Team grouping; `Static` divisions are built-in, others start at ID 100 (`DynamicDivisionStartID`) |
| `Region` | ID, Name (unique) | Geographic grouping of teams |

### Game (`modules/game/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `MatchPlacement` | Round, Match, Phase, Team, Field, Position, State | Team assigned to a match slot |
| `MatchScore` | Score, MatchPlacement, GamePhase, Team | Score record |
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

### BEST (`modules/best/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `ExternalScores` | ID, Team/TeamID, Notebook, Marketing, Poster, Video (float32) | Non-match scores per team |

Max values: Notebook 300, Marketing 250, Poster 100, Video 100 (constants in `type.go`).

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
- **Auth seam**: `Authenticator` is a single-method interface (`Apply(ctx, *http.Request) error`) installed via `WithAuthenticator`, or the `WithBasicAuth` shortcut which wraps the ready `BasicAuth{Username, Password}` (sets `Authorization: Basic ...`). All requests are currently unauthenticated (no credential env vars are wired) — the seam exists so the next onsite→cloud feature can adopt credentials without touching wire code.
- **Wire**: `Do(ctx, method, path, query, out)` builds `base + path + query`, sets `Accept: application/json`, applies the authenticator when set, and decodes the JSON body into `out` (`nil` → discarded). `DoBytes(ctx, method, path, query)` returns the full raw body untouched, for non-JSON payload types (the game setup YAML). Non-2xx responses return `*cloud.Error` (status + trimmed first 512 bytes of body).
- **Per-feature convention**: each cloud feature owns a sibling file in the package with its wire types and methods (e.g. `teams.go` with `RegionInfo`/`TeamInfo` + `GetRegions`/`GetRegionTeams`; `games.go` with `GetGameFile`). The game feature deliberately has no client-side wire types: the raw YAML setup file is the single source of truth and is applied by the same parser/upsert as a local file upload. Add the next feature as a new file, not by growing `client.go`.

### Team Cloud Endpoints (served by a cloud instance)

The `team` module exposes two public read-only JSON endpoints, keyed to the `pkg/cloud` wire shapes:

- `GET /ui/mod/team/cloud/regions` → `[]RegionInfo` (name + team count; includes disabled teams and zero-team regions; ordered by name).
- `GET /ui/mod/team/cloud/teams?region=X` → `[]TeamInfo` (number, name, division, region; unknown region → 404 `{"error":"region not found"}`; omit `region` for every team; ordered by number).

### Cloud Team Import (onsite)

`GET/POST /ui/mod/team/import-cloud` (both `TEAM:ADMIN`-guarded; a "Cloud Import" child sits in the Team nav next to "Bulk Import") lists the cloud regions with per-region counts, and a per-region POST pulls the roster via `Client.GetRegionTeams` and applies it through `upsertTeamRows` — the same upsert the CSV bulk import uses. The upserts on team number, get-or-creates the divisions/regions the rows reference, defaults a blank division to `Open`, and never writes `disabled` (a re-import cannot resurrect a manually-disabled team). Success → 303 to `?imported=N`; a cloud failure renders the import page with an error notification and writes nothing.

Note: `GAMEDAY_CLOUD` is the **base URL of the cloud instance the client dials**; `GAMEDAY_CLOUD_DB` is the **local SQLite file path** `cloud serve` stores its database at. They are distinct — do not conflate.

Also note route registration is scope-exclusive (see Scope-conditional routes above): the factory receives the entrypoint scope and the cloud-scoped module registers the shared management screens plus the cloud pull endpoints, so the cloud JSON endpoints do not exist on onsite instances and the import UI does not exist on cloud instances. The same applies to the game module's cloud endpoints and import UI.

### Game Cloud Screens (served by a cloud instance)

The cloud-scoped `game` module also serves the shared management screens: `GET /ui/mod/game/` (Configuration) is open, and `GET/POST /ui/mod/game/setup` (Setup, `GAME:ADMIN`-guarded) uploads a game setup file through the same `applyGameConfig` as the onsite local upload. The cloud instance is where the game design is authored and maintained before it is pulled to the sites.

### Game Cloud Endpoints (served by a cloud instance)

The `game` module exposes one public endpoint: `GET /ui/mod/game/cloud/setup` returns the configured game as a `text/yaml` document in the same shape the local setup upload accepts (`Field.Positions`, `Game.Phases` with `AdvancementFilters`, `Game.Elements` with `States`/`Values`, and an optional `Clock` section). It is public on purpose: the game file is game design, not sensitive data, and is pulled by other Gameday systems. Runtime state is stripped so a re-import carries the design, not live operations: phase `Active`/`Frozen`/`HideScores` are zeroed, and the clock's runtime `NextMatch`/`NextMatchPhase` pointer is never exported (the clock section is omitted entirely when no clock is configured).

### Cloud Game Import (onsite)

`GET/POST /ui/mod/game/import-cloud` (both `GAME:ADMIN`-guarded; a "Cloud Import" child sits in the Game nav under "Setup") fetches the YAML via `Client.GetGameFile` (the GET pre-fetches so cloud failures surface on the page) and applies it through `applyGameConfig` — the same upsert the local setup file upload uses (positions, phases + advancement filters, elements + states, and the clock when the file carries a `Clock` section). `ScorecardElement` rows are not in the file and are regenerated via `exprForElementState`. Success → 303 to `?imported=1`; a cloud or parse/apply failure renders the import page (or `errors/internal.p2` for parse/apply) with an error and writes nothing.

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
| `GAMEDAY_PORT` | `8080` | Port for `onsite serve` / `cloud serve` |
| `GAMEDAY_MODULES` | — | Comma-separated whitelist of optional modules to load in addition to required ones |
| `LOG_LEVEL` | — | `slog` level (DEBUG, INFO, WARN, ERROR) |
| `GAMEDAY_ADMIN_INIT_PW` | `"GameOn!"` | Default admin password on first boot |
| `AUTHWARE_BASIC_MECHS` | `gameday` | Authware backend selection (set automatically if unset) |

## Testing

- **Location**: `pkg/cloud/client_test.go`, `pkg/db/db_test.go`, `pkg/schedgen/bid_test.go`, `pkg/schedgen/division_test.go`, `pkg/event/bus_test.go`, `pkg/event/handler_test.go`, `pkg/web/error_templates_test.go`, `pkg/web/tmpl_debug_test.go`, `pkg/web/eventbus_test.go`, `pkg/web/users_test.go`, `modules/game/lint_test.go`, `modules/game/advancement_test.go`, `modules/game/clock_test.go`, `modules/game/clock_match_test.go`, `modules/game/cloud_test.go`, `modules/team/team_test.go`, `modules/team/region_test.go`
- **Pattern**: Table-driven / config-driven tests using `schedgen.Config`; in-DB tests build a module via `newTestModule(t, opts...)` (a fresh temp-dir SQLite DB + a `web.Server`, optionally with `web.EventBus`) and exercise handlers or bus events directly
- **Coverage**: BIBD smoke test (v=7, k=3 Steiner Triple System), larger team test (13 teams), degenerate case (teams < block size), division interleaving, error template rendering, debug template loading, advancement filters (roster/scoreboard sourcing, exclude-then-include, tie-breaking), match clock (YAML parse, settings round-trip, start/cancel, auto-stop with hurry/end events, guard against double-start/cancel-when-idle, page rendering), clock-driven match transitions (target selection, start/end/auto-stop/cancel state flow, operator next-match override, flag-off no-op, Scorable-left-alone, stale Running/Queued reset, start rejection paths, transitions panel round-trip), admin team list (pagination, division/region facets, combined filters, filtered pagination, filter state preserved in pager links, no-match empty state), db driver selection (postgres-without-DSN error, unknown-driver error, env-gated Postgres integration test on `GAMEDAY_TEST_PG_DSN`: migrate + upsert round-trip), user deletion (deletes the user, clears the profile's `profile_permissions` join rows, then deletes the profile; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set, where the old profile-first ordering fails with an FK violation), cloud client (JSON decode, query encoding, error vs network-failure distinction, env/default base URL, basic-auth header, regions/teams round-trips, raw-bytes game file round-trip), team cloud import (public regions/teams JSON endpoints incl. unknown-region 404, end-to-end region import with division defaulting, idempotent re-import that updates in place without duplicating and preserves `disabled`, cloud-failure path that writes nothing), game cloud import (end-to-end import via shared `applyGameConfig` incl. advancement filter, regenerated scorecard exprs, and clock section, idempotent re-import, cloud-failure path that writes nothing, public YAML export with runtime state stripped and round-trip parse, cloud-scoped configuration/setup screens incl. setup upload round-trip and onsite-only routes staying unregistered)
- **Run**: `go test ./...`

### Test Fixtures

`test_data/*.yml` — YAML schedule test fixtures for 2-player and 4-player configurations, with/without objectives and divisions. `test_data/*.csv` — team import fixtures (12 and 24-team datasets).

## Conventions

- **Naming**: Go standard (`CamelCase` types, `lowerCamelCase` locals, `PascalCase` exported)
- **Errors**: Custom error types (`UnknownGenerator`, `InvariantViolation`) with `Error() string`
- **Logging**: `log/slog` throughout; no structured logging library
- **HTTP handlers**: `uiView<Name>`, `uiView<Name>Submit` pattern
- **Templates**: `.p2` extension (Pongo2), per-module `ui/p2/views/<module>/` layout, Bulma CSS classes for styling
- **Pagination**: Consistent `Paginate(r)` scope usage
