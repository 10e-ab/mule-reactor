# MuleReactor

MuleReactor is a tool designed to replace and improve the Anypoint Studio
"Build Automatically" feature, enabling faster hot deployment of Mule
applications. It also works fine for other IDEs/editors with a standalone
Mule runtime — or even better, combining Anypoint Studio with an editor like
VIM or Emacs. It listens for file changes in your Mule projects and
automatically deploys the changes, streamlining the development and testing
process.

## Features

- **Real-time deployment**: syncs changes into the deployed app as soon as
  files are modified, triggering Mule's hot redeploy.
- **Zero configuration**: notifications, deployment status watching,
  pom-triggered rebuilds and symlink following all work out of the box.
- **Comprehensive file support**: property files, `log4j2.xml` and other
  resources are handled, and Mule XML configuration files can be added,
  renamed and removed on the fly.
- **Maven resource filtering**: files the pom marks as filtered
  (`<resource><filtering>true</filtering>`) get their `${...}` tokens
  substituted on sync, mirroring `mvn process-resources`. See
  [Maven Resource Filtering](#maven-resource-filtering).
- **Noise suppression**: XML/JSON saves that only change formatting don't
  trigger a redeploy.
- **Editor agnostic, single binary**: one self-contained executable, no
  runtime dependencies.

## Installation

With Go ≥ 1.21 installed:

```
go install github.com/10e-ab/mule-reactor@latest
```

or build from a checkout and put the binary on your PATH:

```
go build -o mule-reactor .
```

Run the tests with `go test ./...`.

Then make sure *Build Automatically* is disabled in Anypoint Studio
(Project → Build Automatically).

## Usage

Open a terminal in your project's root directory (or a directory containing
several Mule projects) and run:

```
mule-reactor
```

Then deploy your application once, normally — run it from Anypoint Studio,
or copy the app jar into the runtime's `apps` directory. From that point,
changes you save are synced into the deployed application and hot deployed
almost instantly.

### Directories

- `--projects-dir <dir>` — where to look for Mule projects (default: the
  current directory; projects one level down are found too)
- `--apps-dir <dir>` — the deployed apps directory (default:
  `$MULE_HOME/apps`)

```
mule-reactor --apps-dir /Applications/AnypointStudio.app/Contents/Eclipse/plugins/org.mule.tooling.server.4.9.ee_.../mule/apps
```

### Default behavior and opting out

Everything is on by default. Opt out with:

- `--no-notification` — no desktop notifications (implies
  `--no-watch-deployments`)
- `--no-watch-deployments` — don't tail the server log for deployment
  success/failure notifications
- `--no-watch-pom` — don't rebuild when a pom changes in a rebuild-worthy
  way; print a stale-app warning instead
- `--no-follow-symlinks` — don't follow symlinks pointing outside the
  project source trees
- `--no-resource-filtering` — sync filtered resource files as-is, without
  `${...}` substitution
- `--no-ignore-formatting` — treat XML/JSON formatting-only changes as
  significant
- `-v` / `--verbose` — verbose output

Two comparison opt-ins relax the exact comparison in all file types, for
workflows with expected whitespace churn:

- `--ignore-whitespace` — ignore whitespace changes within lines (like
  `diff -w`)
- `--ignore-blank-lines` — ignore added/removed blank lines (like `diff -B`)

> **Warning:** with `--ignore-whitespace`, whitespace-only edits inside
> DataWeave scripts and `.properties` values (where whitespace can be
> semantically meaningful, e.g. inside string literals) will NOT be
> deployed — the save is treated as insignificant with no error. Only use
> it if you understand that trade-off.

### Rebuilds

A rebuild-worthy pom change (dependencies, parent — plus properties,
profiles and resources when resource filtering is on) runs
`mvn clean package -DskipTests` in the project root and copies the jar to
the apps dir — with [mvnd](https://github.com/apache/maven-mvnd) used
automatically instead of `mvn` when it is on PATH. Whitespace-only pom
edits are ignored. If several jars match in `target/` (e.g. a build
command without `clean`), the newest one is deployed; a failed build keeps
the previous pom baseline, so the next save retries the rebuild.

Set `MULE_REACTOR_BUILD_COMMAND` to override the build command entirely;
it runs through a shell in the project root, so wrappers, extra flags and
pipes work:

```
MULE_REACTOR_BUILD_COMMAND="mvn clean package -DskipTests -Pdev" mule-reactor
```

The `Running: ...` line printed before each rebuild shows which command
was chosen.

### Notifications

Desktop notifications work out of the box on macOS, Linux and Windows (via
[beeep](https://github.com/gen2brain/beeep)).

To customize delivery — sounds, icons, Slack webhooks, whatever — point
`MULE_REACTOR_NOTIFIER` at a script. It is run through a shell with two
arguments: the title and the message. Example scripts are in `notifiers/`:
the macOS one uses [terminal-notifier](https://github.com/julienXX/terminal-notifier)
(`brew install terminal-notifier`), the GNOME one uses `gdbus`. Test a
script by hand:

```
your-notifier "<title>" "<message>"
```

The active notifier is announced at startup.

### Symbolic links

Symlinks pointing outside the project trees are followed by default: their
targets are watched natively, and changes behind them sync as if they lived
at the symlink's location. This is particularly useful when API
specifications (RAML/OAS) are maintained in separate repositories and
linked into projects — e.g. `src/main/resources/api` being a symlink to an
API-spec checkout. Project directories that are themselves symlinks work
too. Disable with `--no-follow-symlinks`.

### What counts as a significant change

A save only triggers a sync (and redeploy) when it changes something real:

- **XML and JSON** files are canonicalized before comparison, so
  formatting-only changes — indentation, line breaks — don't redeploy.
  Disable with `--no-ignore-formatting`.
- **Every other file type** is compared exactly by default. Whitespace can
  be semantically meaningful in `.properties` values or DataWeave, so it is
  not ignored unless you opt in with `--ignore-whitespace` /
  `--ignore-blank-lines` (see the warning above).
- Files over 1 MB are synced without comparison.
- A `log4j2.xml` with `monitorInterval` set is synced without forcing a
  redeploy — Mule reloads it on its own.

## Maven Resource Filtering

If a project's `pom.xml` declares filtered resources, for example:

```xml
<resources>
  <resource>
    <filtering>true</filtering>
    <directory>src/main/resources</directory>
    <includes>
      <include>mule-application.properties</include>
    </includes>
  </resource>
</resources>
```

then Maven replaces `${...}` tokens in those files at build time — and
syncing the raw source file would hand the runtime unresolved tokens
(breaking, for instance, an `api.raml=resource::...:${raml.version}:...`
reference). MuleReactor detects files covered by a `filtering=true`
resource (honoring `includes`/`excludes`) and substitutes their tokens
before syncing:

- `pom.xml` `<properties>` values (with nested `${...}` references
  resolved), including properties from profiles that are active by default
  or activated by a file `exists`/`missing` condition — profiles requiring
  `-P` flags, settings.xml, JDK, OS or property activation are not
  evaluated
- `project.groupId`, `project.artifactId`, `project.version`,
  `project.name`, `project.basedir` (falling back to the `<parent>` values
  when inherited)
- `maven.build.timestamp`, honoring `maven.build.timestamp.format` —
  substituted with a fixed epoch sentinel (`1970-01-01T00:00:00Z`) rather
  than the current time. MuleReactor didn't build anything, so a real
  timestamp would be a lie — and the sentinel keeps filtered output
  deterministic, so unchanged files diff as identical and don't trigger
  needless redeploys
- Live git values matching what `git-commit-id-maven-plugin` would inject:
  `git.commit.id`, `git.commit.id.abbrev`, `git.branch`, `git.dirty`
- `user.name` (the JVM system property Maven interpolates), resolved from
  `$USER`/`$USERNAME`

Unknown tokens are left untouched (as Maven does) with a warning. Files not
covered by a filtered resource — including binaries like keystores — are
synced byte-for-byte. If filtering fails (e.g. the pom is mid-edit), the
file is not synced and the last good deployed copy is kept. Note that
properties inherited from a parent pom's `<properties>` section are not
resolved (only the parent's coordinates are), since the parent pom is
typically not available on disk.

Changes to the pom itself are detected too: any change to the
`<dependencies>`, `<parent>`, `<properties>`, `<profiles>` or
`<build><resources>` sections triggers a full rebuild — a rebuild is the
one response that is always correct, since a property can feed dependency
versions and filtered resources alike, and only Maven can package new
artifacts into the app. With `--no-watch-pom` a stale-app warning is
printed instead.

With `--no-resource-filtering`, filtered resource files are synced as-is,
tokens included, and the pom watcher stops reacting to
property/profile/resources changes (they can't reach the deployed app
without filtering); only dependency changes trigger a rebuild. Note this
reopens the blind spot where a dependency version fed by a property
(`<version>${some.version}</version>`) changes without triggering a
rebuild.

## Optional Configuration

1. **Set `MULE_HOME`** so you don't have to pass `--apps-dir` every time:

   ```bash
   export MULE_HOME=/Applications/AnypointStudio.app/Contents/Eclipse/plugins/org.mule.tooling.server.4.9.ee_.../mule
   ```

2. **Update `log4j2.xml` for instant logging configuration**: adding a
   `monitorInterval` attribute lets you change logging levels and formats
   without a redeploy:

   ```xml
   <Configuration monitorInterval="10">
   ```

3. **Configure Mule to detect hot deploys faster** by adding
   `-Dmule.launcher.changeCheckInterval=500` to your Run Configuration's VM
   arguments (in Anypoint Studio: Run → Run Configurations… → VM Arguments).

## Limitations

- **Pre-processed resources**: Maven resource filtering is supported — see
  above. Other kinds of build-time resource generation or modification are
  not: the tool syncs the files as they are in your project directory, so
  resources produced by other plugins will not be reflected in the hot
  deployed application.

- **Hot deploy reliability**: hot deployment, by its nature, can sometimes
  fail or lead to unexpected behaviors due to the complexities of
  application state and runtime management. If you encounter odd behavior,
  perform a normal (cold) deployment of your application and let
  MuleReactor handle subsequent hot deployments from that known good state.

- **macOS file descriptors**: the file watcher uses kqueue, which costs one
  file descriptor per watched file/directory. Fine for normal project
  sizes; an enormous resources tree could approach fd limits.

## Previous Ruby version

MuleReactor was originally a Ruby script; this Go implementation replaced
it. The Ruby version is preserved on the
[`ruby` branch](https://github.com/10e-ab/mule-reactor/tree/ruby) (tagged
`ruby-final`) and only receives critical fixes — see
[MIGRATING-FROM-RUBY.md](MIGRATING-FROM-RUBY.md) for the differences.

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open
issues to suggest improvements or add new features.

## License

MuleReactor is released under the MIT License. See the LICENSE file for
more details.
