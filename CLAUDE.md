# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Rules

- Do not include a `Co-Authored-By: Claude ...` line in commit messages.
- Always run gofmt before committing Go code: `gofmt -l .` must report nothing (fix with `gofmt -w`).

## What this is

MuleReactor is a hot-deployment tool for Mule applications: it watches Mule project source trees and syncs changed files into a deployed app's directory (under `$MULE_HOME/apps` or `--apps-dir`), triggering Mule's hot redeploy. It is a Go module (`github.com/10e-ab/mule-reactor`, package `main` at the repo root) producing a single static binary. The original Ruby implementation lives on the `ruby` branch (tagged `ruby-final`, critical fixes only); `MIGRATING-FROM-RUBY.md` documents the behavioral differences.

## Environment and commands

- Build: `go build -o mule-reactor .` — run `./mule-reactor --help` for all options. Typical dev invocation: `./mule-reactor -v --projects-dir <dir> --apps-dir <dir>`.
- Test: `go test ./...` (unit tests with temp-dir fixtures). Tests share the package-global `opts` via the `setOpts` helper, so they must not use `t.Parallel`. The live watcher event flow needs manual verification against a Mule runtime or Anypoint Studio.
- Everything is on by default (notifications, deployment watching, pom rebuilds, symlink following) with `--no-*` opt-outs; Ruby-era opt-in flags are accepted as warned no-ops.

## Architecture

`run()` in `main.go` starts three watcher subsystems, then blocks forever:

1. **Source watcher** (`watch.go`, `sourceWatcher`): fsnotify watches over `src/main/mule` and `src/main/resources` of each project (projects dir and one level down), built recursively via `addTree` since fsnotify is per-directory. Events are debounced (`debounce.go`, 300ms, serialized flushes) and classified against a `known` set (added/modified/removed). External symlinks are followed by watching their resolved targets, with `linkMap` translating real event paths back to logical project paths (file targets watch the parent dir to survive atomic saves; deleted symlinks are detected lazily — kqueue emits no event; transiently deleted targets get recovery pollers). Changed files are copied into the deployed app dir, then `mule-artifact.json` is rewritten (`rebuildMuleArtifact` in `sync.go`) to force a redeploy — except `log4j2.xml` with `monitorInterval`, which Mule reloads on its own. Deleted watched roots are polled for recreation; a dead fsnotify backend restarts the watcher.

2. **Pom watcher** (`pomwatch.go`): fsnotify on the projects dir and each project root only (never `target/`, so build churn can't flood it). Hashes rebuild-relevant pom content — dependencies/parent plus, when resource filtering is on, properties/profiles/resources (`pomStateFor`, `pomRebuildWorthy`) — so formatting-only edits are ignored. A rebuild-worthy change runs the build (`mvn clean package -DskipTests`, with `mvnd` preferred when on PATH, all overridable via `MULE_REACTOR_BUILD_COMMAND`; jar selected by artifactId/newest) and deploys the jar; the baseline only advances on success. `--no-watch-pom` prints a stale-app warning instead.

3. **Deployment watcher** (`deploy.go`): native `tail -F`-style follow of `mule_ee.log`, notifying on deployment success/failure.

Cross-cutting behaviors to preserve when changing sync logic:

- **Maven resource filtering** (`maven.go`: `withSourceFile` → `filterMavenTokens`): files a pom marks as filtered resources get `${...}` tokens substituted before syncing, mimicking `mvn process-resources` (pom properties incl. active profiles and tokenized `<directory>`, project coordinates, live git values, a deliberately fixed epoch sentinel for `maven.build.timestamp`). If filtering fails, the file is *not* synced — the last good deployed copy is kept. Parsed poms are memoized by path+mtime in `pomCache`.
- **Insignificant-change suppression** (`significantChanges` in `sync.go`): XML/JSON are canonicalized before comparison so formatting-only saves don't redeploy; every other file type is compared exactly (whitespace is semantically meaningful in `.properties`/DataWeave). A source that fails to parse while the deployed copy parses is treated as a half-written save and skipped.
- **Resilience**: one file's failure never aborts a batch (`handleFileChangeSafely`, per-pom `recover`), debouncer flushes are serialized and panic-contained, and watchers restart themselves if their event stream dies.
- **Notifications** (`notify.go`): built-in cross-platform notifier (beeep) by default; `MULE_REACTOR_NOTIFIER` or a `mule-reactor-notifier` on PATH shell out instead (script contract: title and message as two args; examples in `notifiers/`).
