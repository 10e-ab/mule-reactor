# Migrating from the Ruby version

The Go version behaves like the Ruby script for the core job — which
directories are watched, when a change is synced, Maven resource filtering,
pom rebuild-worthiness hashing, `mule-artifact.json` rewriting — but the
defaults are inverted and a few semantics were deliberately improved.

## Defaults are inverted

Ruby's opt-in behaviors are on by default:

| Ruby | Go |
|---|---|
| `-n` / `--notification` | on by default; `--no-notification` to disable |
| `-d` / `--watch-deployments` | on by default; `--no-watch-deployments` to disable |
| `-p` / `--watch-pom` | on by default; `--no-watch-pom` to disable |
| `-s` / `--follow-symlinks` | on by default; `--no-follow-symlinks` to disable |

The old opt-in flags are still **accepted and ignored** (with a startup
warning naming the flag), so existing wrapper scripts keep working.
Combined short flags (`-vndp`) do **not** work — Go's flag parsing requires
them separated (`-v -n -d -p`).

## Flags that are gone

- `--symlink-interval`, `--pom-interval` — nothing polls anymore. The pom
  watcher gets native per-directory events (the `target/` storm from
  `mvn clean package` never reaches it), and symlink targets are watched
  natively.
- `--no-ignore-whitespace`, `--no-ignore-blank-lines` — accepted but
  ignored; see below.

## Whitespace handling is per-file-type

Ruby compared all file types with `diff -w -B` (whitespace and blank lines
ignored everywhere) and offered flags to disable that. The Go version
instead decides by file type: XML and JSON are canonicalized so
formatting-only saves don't redeploy, and **every other file type is
compared exactly** — whitespace can be semantically meaningful in
`.properties` values or DataWeave. If you ran the Ruby version with
`--no-ignore-whitespace` for this reason, that behavior is now built in.

Related nuances:

- JSON saves that only reorder keys are treated as insignificant (Ruby
  preserved key order and synced them).
- An unparseable XML file (e.g. a half-written save) is treated as not
  significant, so the last good deployed copy is kept — same as Ruby.

## Notifications

Ruby requires a `mule-reactor-notifier` script on PATH and always shells
out to it. The Go version resolves delivery at startup and announces the
choice:

1. `MULE_REACTOR_NOTIFIER=/path/to/script` — explicit override
2. `mule-reactor-notifier` on PATH — same contract as Ruby (title and
   message as two arguments, run through a shell)
3. built-in cross-platform notifications — no script needed

## Rebuilds

The build command is configurable via `MULE_REACTOR_BUILD_COMMAND` (Ruby
hardcoded `mvn clean package -DskipTests`). The jar copy requires exactly
one matching `target/<name>*.jar`; Ruby's shell `cp` failed differently on
multiple matches.

## Other behavioral differences

- **`mule-artifact.json` key order**: keys are written alphabetically
  instead of preserving the original order. Same content; Mule parses it
  either way.
- **Ant patterns**: in filtered-resource includes/excludes a bare `**`
  crosses directories (Maven semantics, e.g. `config/**` matches nested
  files); Ruby's fnmatch treated it as a single path segment.
- **Deployment log tail** reports only lines written after startup; Ruby's
  `tail -F` replayed the last 10 lines, producing stale notifications.
- **Event classification**: added vs modified is tracked with a known-files
  set (editors save via create+rename). mtime-only changes (`touch`) are
  ignored.
- **Extra robustness**: a watched `src/main/...` root that is deleted and
  recreated (e.g. by a branch switch) is re-watched automatically, new
  project directories get their pom.xml tracked without a restart, and a
  crashed watcher backend restarts itself instead of going silent.
- **Windows**: the Ruby version's documented limits (shelling out for
  `mvn`, `tail -F` for the log) don't apply — both are native. Windows
  remains untested.
- **macOS watcher backend**: kqueue (one file descriptor per watched
  file/dir) rather than FSEvents. Fine for normal project sizes; a huge
  resources tree could hit fd limits where the Ruby version would not.
