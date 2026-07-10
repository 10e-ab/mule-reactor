# mule-reactor (Go port)

A Go port of the `mule-reactor` Ruby script in the repository root. Same
purpose, same flags, same behavior: watch Mule project source trees and sync
changed files into the deployed app's directory to trigger hot redeploy.

## Build

```
cd go
go build -o mule-reactor .
```

## Run

```
./mule-reactor --help
./mule-reactor -v --projects-dir <dir> --apps-dir <dir>
```

All options from the Ruby version are accepted. `--symlink-interval` and
`--pom-interval` are no-ops kept for compatibility: the Go version gets native
file events for both cases and does not poll (see PORT-NOTES.md).
