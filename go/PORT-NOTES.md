# Porting mule-reactor to Go — investigation notes

**Verdict: the port is feasible and this directory contains a working first version.**
It builds with Go ≥ 1.21, uses a single external dependency
([fsnotify](https://github.com/fsnotify/fsnotify)), and compiles to one static
binary — attractive for the "sites that don't allow ruby" case the Ruby TODO
mentions.

## The two Ruby workarounds are indeed not needed

### Pom watcher: no polling

The Ruby version polls pom.xml with Filewatcher because the Listen gem watches
recursively and drowns in `target/` churn during `mvn clean package`.
fsnotify watches are **per-directory and non-recursive**, so the Go pom
watcher watches only the projects dir and each project root directory and
filters events for `pom.xml`. Events from `target/` are never delivered in
the first place. `--pom-interval` is accepted for CLI compatibility but unused.

### Symlinks: no hybrid polling mode

Listen cannot follow symlinks pointing outside the watched tree, so Ruby runs
a second polling watcher over just those symlinks. In Go, `-s` resolves each
external symlink during the initial walk and adds native fsnotify watches on
the **target** directories/files, keeping a real→logical path map so events
are translated back into the project tree before syncing. Verified working on
macOS. `--symlink-interval` is accepted but unused.

Caveat vs Ruby: symlinks are resolved once at startup (the Ruby version also
detected them only at startup). A symlink created *after* startup inside a
watched dir is seen as a Create event but its external target is followed only
if it appears as a new directory event; retargeted symlinks need a restart —
same practical limitation as the Ruby version.

## Library / feature mapping

| Ruby | Go |
|------|----|
| Listen (fs events) + Filewatcher (polling) | fsnotify only (kqueue/inotify/ReadDirectoryChangesW) |
| REXML + XPath | `encoding/xml` decoded into a small generic `XMLNode` tree (`xmlnode.go`) |
| Diffy (shells out to `diff -wB`) | normalize (strip whitespace / drop blank lines) and compare in-process (`normalizeForDiff`) |
| `JSON.pretty_generate` | `encoding/json` `MarshalIndent` |
| `Open3` git calls | `os/exec` |
| `tail -F` subprocess | native tailer handling rotation/truncation (`deploy.go`) — now Windows-capable |
| `mvn` via shell string | `os/exec` with `cmd.Dir` — no shell, Windows-capable |
| Thread-per-watcher + monitor/restart threads | goroutines; panics are recovered per file/pom so one failure never kills a batch |
| Listen's event batching | 300 ms debounce per watcher, then batch processing |

Behavior kept identical (verified by running against a fake project/apps tree):

- watch `src/main/mule` + `src/main/resources` of the projects dir and one level down
- sync on change, then rewrite `mule-artifact.json` to force redeploy;
  `log4j2.xml` with `monitorInterval` skips the forced redeploy
- formatting-only XML/JSON saves and whitespace/blank-line-only changes do
  not redeploy (`--no-ignore-*` flags preserved), >1 MB files skip comparison
- Maven resource filtering with pom properties, active profiles
  (activeByDefault / file exists/missing), project coordinates with parent
  fallback, nested property resolution, live git values, `${user.name}`, and
  the fixed-epoch `${maven.build.timestamp}` sentinel; filtering failure
  skips the sync and keeps the last good deployed copy
- pom changes hashed on dependencies/parent (+ properties/profiles/resources
  when filtering is on) so formatting-only pom edits are ignored; rebuild
  with `-p`, stale-app warning without
- deployment notifications by tailing `mule_ee.log`, sent via the same
  `mule-reactor-notifier` script on PATH

## Known differences

- **Flags**: Go's `flag` package accepts `-flag` and `--flag` but not combined
  short flags (`-vp` must be `-v -p`). `-h/--help` output is formatted by Go.
- **Event classification**: added vs modified is tracked with a known-files
  set (editors save via create+rename, which raw fsnotify reports as Create;
  Listen normalized this). mtime-only changes (`touch`) arrive as Chmod and
  are ignored.
- **mule-artifact.json key order**: Go marshals map keys alphabetically, so
  the rewritten file has the same content but different key order than Ruby's
  insertion-ordered output. Mule parses it either way.
- **XML canonicalization** internals differ from REXML's pretty printer, but
  both sides of every comparison use the same canonicalizer, so the
  "significant change" answer is the same.
- An unparseable XML file is treated as "no significant change" (sync
  skipped) — this mirrors an accidental-but-useful Ruby behavior where a
  REXML parse error made `significant_changes?` return nil.
- The pom watcher watches project root dirs at startup; a brand-new project
  directory created while running is not picked up (the Ruby version's
  'created' branch was NOT IMPLEMENTED anyway).

## Portability wins over Ruby

`rebuild_project` (no shell) and the log tail (no `tail -F`) are now pure Go,
removing the two documented "won't work on Windows" limits. Windows remains
untested; paths are normalized to forward slashes internally.

## Not yet done

- No verification against a real Mule runtime / Anypoint Studio (same as Ruby —
  the fake-tree smoke test covers sync/filtering/pom logic only).
- No test suite (the Ruby version has none either); `go vet` is clean.
- macOS uses kqueue via fsnotify (one fd per watched file/dir). Fine for
  normal project sizes; a huge resources tree could hit fd limits where
  Listen's FSEvents backend would not.
