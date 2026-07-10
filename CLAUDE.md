# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Rules

- Do not include a `Co-Authored-By: Claude ...` line in commit messages.

## What this is

MuleReactor is a hot-deployment tool for Mule applications: it watches Mule project source trees and syncs changed files into a deployed app's directory (under `$MULE_HOME/apps` or `--apps-dir`), triggering Mule's hot redeploy. Everything lives in the single executable Ruby script `mule-reactor` (~1000 lines, no gemspec/Gemfile).

## Environment and commands

- Ruby 3.x only (pinned to 3.4.3 in `.tool-versions`); Ruby 4 is not supported.
- Runtime gems (installed globally, no Bundler): `listen`, `filewatcher`, `diffy` (`open3`, `rexml`, `json` come from stdlib).
- Run: `./mule-reactor --help` for all options. Typical dev invocation: `./mule-reactor -v --projects-dir <dir> --apps-dir <dir>`.
- There is no test suite and no linter. Syntax-check with `ruby -c mule-reactor`. Real verification requires a Mule runtime or Anypoint Studio; `test-listen.rb`, `setup-symlink-test.sh`, `detect-symlinks.rb`, `hybrid-watcher.rb`, `test-watch/`, `test-external/` are untracked manual-testing scratch scripts for the file-watching behavior, not part of the tool.

## Architecture

`run()` at the bottom of `mule-reactor` starts three independent watcher subsystems, then sleeps forever:

1. **Source watcher** (`watch_mule_and_resources_hybrid`): watches `src/main/mule` and `src/main/resources` of each project (current dir and one level down). Uses the Listen gem (fs events); with `-s/--follow-symlinks` it adds a hybrid mode where Filewatcher (polling) covers only symlinks pointing outside the watched tree, since Listen can't follow those. Changed files are copied into the deployed app dir, then `mule-artifact.json` is rewritten (`rebuild_mule_artifact`) to force Mule to redeploy — except for `log4j2.xml` with `monitorInterval`, which Mule reloads on its own.

2. **Pom watcher** (`watch_pom_files`): polls `pom.xml` files with Filewatcher (Listen is too slow for target/ churn during builds). It hashes only rebuild-relevant pom content — dependencies/parent plus, when resource filtering is on, properties/profiles/resources (`pom_file_state`, `pom_rebuild_worthy?`) — so formatting-only edits are ignored. A rebuild-worthy change runs `mvn clean package` and copies the jar to the apps dir with `-p/--watch-pom`, or just prints a stale-app warning without it.

3. **Deployment watcher** (`watch_deployments`, needs `-d` plus `-n`): tails `mule_ee.log` and sends deployment success/failure notifications.

Cross-cutting behaviors to preserve when changing sync logic:

- **Maven resource filtering** (`with_source_file` → `filter_maven_tokens`): files a pom marks as filtered resources get their `${...}` tokens substituted before syncing, mimicking `mvn process-resources` (pom properties, project coordinates, live git values, and a deliberately fixed epoch sentinel for `maven.build.timestamp` to keep output deterministic). If filtering fails, the file is *not* synced — the last good deployed copy is kept. Parsed poms are memoized by path+mtime in `POM_DOCUMENT_CACHE`.
- **Insignificant-change suppression** (`significant_changes?`): XML/JSON are canonicalized and diffed with whitespace/blank-line flags so formatting-only saves don't trigger redeploys. Controlled by the `--no-ignore-*` flags.
- **Resilience**: watcher threads are wrapped so one file's failure never aborts a batch (`handle_file_change_safely`), and monitor threads restart Filewatcher/pom threads if they die.
- **Notifications** shell out to a `mule-reactor-notifier` script expected on PATH (platform examples in `notifiers/`).

Known portability limits: `rebuild_project` and the log tail use shell/`tail -F` and won't work on Windows.
