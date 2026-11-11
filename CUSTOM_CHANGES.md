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
