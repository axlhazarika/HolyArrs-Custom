# HolyArrs Custom — custom changes

AXL's personal fork of [HolyArrs/HolyArrs](https://github.com/HolyArrs/HolyArrs). Nothing here goes back upstream.

## Branch model
- `main` mirrors `upstream/main` exactly (fast-forward only). Never commit to `main`.
- `custom` = `main` + small, separate commits, one per change, rebased onto new upstream releases on a set rhythm (git rerere is on).
- Prefer additive changes (new files, config, hooks) over edits to upstream files, to keep rebases clean.
- The `upstream` remote's push URL is `DISABLED`.

## Change table
| Change | Commit | Upstream files touched | Why |
|---|---|---|---|
| Usenet & debrid settings (`internal/connections`, page `/settings/providers`) | branch `custom-connections`: 4ecf27e module, ad3e0df routes/wiring/API docs, 915bddf page, 941efb8 this record, e0df4d6 review fixes, 99740c6 default debrid name, cfaf752 this record's commit list, then the fix that keeps written Decypharr items HolyArrs' after a failed restart | `cmd/holyarrs/modules.go` (1 line), `cmd/holyarrs/openapi.json` (6 operations, regenerate on rebase), `cmd/holyarrs/openapi_test.go` (routePackages, 1 line), `tools/structurecheck/allow.go` (1 line), `internal/core/parts/table.go` (6 routes marked core), `internal/core/parts/table_screens.go` (1 line), `docs/dev/api-routes.md` (regenerate: `go run ./app/backend/tools/apidoc`), `tools/structurecheck/structure_test.go` (Modules, 1 line), `tools/structurecheck/testdata/json-keys.txt` and `route-table.txt` (regenerate with `-update`), `app/frontend/src/lib/core/settings.ts` (1 line), `app/frontend/src/routes/settings/(home)/settings-search.ts` (2 lines) | One owner-only page to add, test and remove Usenet servers (pushed to SABnzbd) and Real-Debrid/AllDebrid accounts (written into Decypharr's config, which then restarts) instead of editing env files |

### Notes for rebasing the Usenet & debrid change
- No migration: entries are one encrypted setting, group `links`, key `custom_connections_entries` (the settings table accepts only its fixed groups). Custom changes should not add numbered migrations: the migrator applies only numbers above the highest one applied, so a custom number either collides with upstream later or makes upstream's later migrations skip.
- **Never hand-merge the generated files.** `openapi.json`, `route-table.txt`, `json-keys.txt` and `docs/dev/api-routes.md` are regenerated on every sync onto a new upstream release, even without a conflict. On conflict, take upstream's file and regenerate (`go test ./app/backend/cmd/holyarrs -run TestOpenAPIGenerated -update`; `go test -tags structure ./app/backend/tools/structurecheck -run 'TestHeaderAndJSONLiteralsUnchanged|TestRouteTableUnchanged' -update`). The six hand-written operation summaries are in the commit that adds them; re-add them if the regenerate drops them.
- The one-line lists (`structure_test.go` Modules, `openapi_test.go` routePackages, `allow.go` layers, `parts/table.go` and `table_screens.go`) are re-added by hand after taking upstream's version.
- Route permissions are set in `cmd/holyarrs/connections_wiring.go`, not in `accounts/scopes.go`.
- UI words: "Connections" is on the build's banned-word list, so the page is called "Usenet & debrid".
- Decypharr is enabled only by `HOLYARRS_DECYPHARR_CONFIG` (path to its config.json) and `HOLYARRS_DECYPHARR_LABEL` (its launchd label); without them debrid accounts stay "Not pushed".
