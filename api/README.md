# proto

`rbln_services.proto` is a copy of the rbln-daemon (RSMD) public v0 API,
kept byte-identical to the daemon's source of truth except for the added
`go_package` option.

## Sync provenance

| | |
| --- | --- |
| Source repo | `ssw-common-tools` |
| Source path | `rbln-smd/proto/public/rbln_smd.proto` |
| Synced from | commit `39805f59c225815b4307c2016330794213b834b2` (branch `dev`, 2026-06-19) |

To re-sync: copy the file from the source path, re-add the `go_package`
option below the `package` statement, update the commit hash above, then
regenerate the bindings