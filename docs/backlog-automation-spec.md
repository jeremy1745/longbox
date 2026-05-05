# LongBox Backlog Automation Spec
*Draft — 2026-03-25*

## 1. Purpose
Deliver an automated backlog workflow so that "Add Series" (or adding a want) can immediately queue every missing issue, respect annual/special handling, and manage SABnzbd downloads without dupes. This closes the parity gap with Mylar’s Wanted/Backlog flow while keeping LongBox’s single-binary ergonomics.

## 2. Goals & Non-goals
### Goals
1. **Auto Wanted Runs** – When a user adds a series (or marks it as Wanted), LongBox computes every missing issue (including variants) and pushes them into a managed queue without manual per-issue clicks.
2. **Library-awareness** – NAS scans inform the queue so only genuinely missing issues are requested.
3. **Annual/Special routing** – Annuals, specials, one-shots automatically land in `Annuals/` (or another user-defined pattern) under each series.
4. **Queue control** – SABnzbd integration enforces a max of **25 concurrent active downloads**, adds retries with exponential backoff, and prioritizes oldest wanted issues first.
5. **Observability** – Users can see backlog status (planned, queued, downloading, failed, completed) with SSE updates and can pause/resume the automation.

### Non-goals (for now)
- Torrent/NZBGet clients (still SAB-only).
- Full arcs/TPB tracking (tracked separately).
- Multi-user permissions or remote script hooks.

## 3. User Stories
1. *"Add Series"* → The dialog includes a checkbox "Queue all missing issues" (default ON). When confirmed, a backlog job appears showing #issues to fetch, their statuses, and ETA.
2. *"Catch up on library"* → From Library view, selecting a series exposes a "Queue missing issues" action. LongBox compares the library scan vs ComicVine metadata to determine the backlog set.
3. *Annual handling* → When an annual downloads, the post-processor moves it to `<series>/Annuals/<series> Annual <year>.cbz` (or user template), even if SAB downloaded to the main folder.
4. *Retries* → If SAB reports failure or NZB age > max, LongBox retries up to configurable attempts (default 3) and logs reason.
5. *Visibility* → A user can open the "Backlog" tab and see: total missing issues, queued, downloading, failed (with retry indicators), and completed.

## 4. Architecture Overview
```
ComicVine metadata + Library scan (NAS) → Backlog Planner → Queue Manager → SABnzbd → Post-Processor
                                                   ↑                 ↓
                                            Library-aware filter  Status/Retry hook
```

1. **Backlog Planner**
   - Input: Series ID, optional issue range (defaults to entire series).
   - Uses ComicVine issue list (already stored) + library scan table to determine `missing_issues`.
   - Emits `backlog_items` (issue_id, variant_id?, priority, reason) into SQLite.

2. **Queue Manager**
   - Watches `backlog_items` where status = `pending`.
   - For each, runs `SearchJob` (existing Newznab/Prowlarr search) to find best NZB (prefers score from existing heuristics).
   - Once NZB chosen, enqueues download via SAB API but only if `active_downloads < max_concurrent` (default 25).
   - Maintains in-memory + persisted counters for active slots.

3. **SAB Hook**
   - Poll SAB’s queue API (matching by `nzb_id`) to know job state.
   - On completion, triggers post-processing pipeline.
   - On failure (SAB status `failed`, `paused`, `deleted`), increments retry_count and requeues with backoff (5m, 15m, 60m) up to configurable max.

4. **Post-Processor Enhancements**
   - Uses ComicVine issue metadata to build final filename.
   - If `issue.type == annual || special`, route to `Annuals/` subfolder (configurable template).
   - Marks issue as `owned` in library table.

## 5. Data Model Updates (SQLite)
### Tables
- `backlog_runs`
  - `id`, `series_id`, `created_at`, `status` (planning, active, paused, completed, failed), `total_issues`, `queued_issues`, `completed_issues`, `failed_issues`.
- `backlog_items`
  - `id`, `backlog_run_id`, `series_id`, `issue_id`, `variant_id NULL`, `status` (pending, searching, queued, downloading, completed, failed, canceled), `retry_count`, `last_error`, `priority`, timestamps.
- `download_jobs` (if not already present)
  - Extend existing table with `backlog_item_id`, `sab_nzo_id`, `attempt`.
- `library_files` (existing) – ensure we store `issue_id`, `variant_id`, `path`, `hash` to prevent dupes.

### Config
Add to `config.yaml` (and env overrides):
```yaml
backlog:
  max_concurrent_downloads: 25
  max_retries: 3
  retry_backoff_minutes: [5, 15, 60]
  annual_subfolder: "Annuals"
  enable_variants: true
```

## 6. Workflow Details
### 6.1 Planner
1. Fetch list of issues for series from DB (populated via ComicVine sync).
2. Join with `library_files` to find existing issues.
3. If `enable_variants`, include variant issues (ComicVine `store_date` or variant flag) and mark them separately.
4. Create `backlog_run` record + insert `backlog_items` for each missing issue.
5. SSE push: `run_created` with counts.

### 6.2 Queue Manager Loop
Runs as dedicated goroutine / job worker.
```
ticker 5s -> 
  if paused => continue
  count active SAB jobs (internal) < max?
    fetch next backlog_item where status = pending ORDER BY priority, issue_number
    mark status=searching
    perform search (Newznab/Prowlarr) -> best NZB
      if none: status=failed, last_error="No NZB", schedule retry in 24h (special state?)
      else submit to SAB -> sab_nzo_id
           status=queued, store sab id, increment active counter
```
- Active counter decremented when SAB job finishes (success/fail).
- If SAB returns immediate error (e.g., authentication), mark failed and surface to UI.

### 6.3 Post-Processing
- Use existing organizer but add:
  - Determine `target_folder` via template: e.g., `{series}/{annual_subfolder}/{series} Annual {year|pad:4}.{format}` when `issue.is_annual`.
  - Extended template tokens: `{annual}`, `{store_year}`, `{variant}`.
- After move, update `library_files` and set backlog_item status = completed.

### 6.4 Retry Logic
- On failure reasons: SAB failure, NZB too old, missing from index, network.
- Transitions:
  - `failed` + `retry_count < max` → schedule by setting `retry_at` on backlog_item. Queue Manager only considers items with `retry_at <= now`.
  - After max retries: status = `error`, require manual action (button: "Retry now" or "Ignore").

### 6.5 Pause / Resume
- `backlog_runs` table stores status. When user hits "Pause", queue manager skips new items but allows active downloads to finish. "Resume" flips status back.
- Per-series pause: backlog run UI shows toggle.

## 7. UI/UX
- **Series detail**: "Queue all missing issues" button showing count.
- **Global Backlog view**: table with columns `[Series, Issue, Status, Retries, Age, Actions]` + summary counts + pause/resume all.
- **Annual indicator**: small badge when issue is annual to show routing.
- **Config screen**: backlog settings (max concurrent, retries, annual folder name, variants toggle).
- **Notifications**: toast/SSE updates when run starts, completes, or hits errors.

## 8. API / Background Endpoints
- `POST /api/backlog/runs` – body `{"series_id":123,"include_variants":true}` → returns run ID.
- `GET /api/backlog/runs` – list with counts.
- `POST /api/backlog/runs/{id}/pause` / `resume`.
- `POST /api/backlog/items/{id}/retry` – manual restart of a failed item.
- SSE channel `backlog` broadcasting events: `run_created`, `item_status_changed`, `run_completed`.

## 9. SABnzbd Integration Notes
- Use SAB’s `/queue` to monitor active count; also map `sab_nzo_id` per backlog item.
- Use category `longbox` (configurable) so we can filter active downloads without counting the user’s other SAB queues.
- When active slots are full, queue manager stops submitting new NZBs but continues polling existing ones.

## 10. Annual Handling Details
- ComicVine issues include `type` (annual, one-shot, special). Map to `issue.is_annual` bool in DB.
- Template default: `<series>/Annuals/<series> Annual {year}.{format}` but allow user to override in config/UI.
- Ensure duplicates: If Annual already exists locally (detected via scan), backlog item should be marked completed immediately.

## 11. Edge Cases / Open Questions
- **Multiple libraries**: initial scope assumes single library root.
- **Variants explosion**: Need guardrail to cap variant queueing (e.g., only cover variants flagged by user?). Maybe config `variant_mode: none|regular|all`.
- **Reconciliation**: If SAB job succeeds but post-processing fails, backlog item sits in `downloading`. Need watchdog to detect stuck states (time > 2h) and surface error.
- **Manual NZB import**: Should manual drag-and-drop mark backlog items automatically? (Future enhancement).

## 12. Implementation Plan (Phased)
1. **Planner + Data Model** – migrations, API to create backlog runs, UI stub.
2. **Queue Manager** – background worker + SAB concurrency enforcement.
3. **Post-Processor upgrades** – annual routing + rename tokens.
4. **UI polish + SSE events**.
5. **Retry/backoff tuning + metrics**.

## 13. Testing Strategy
- Unit tests for planner diff logic (existing files vs missing issues).
- Integration tests using sqlite + mocked SAB + mocked Newznab responses.
- Manual end-to-end: point at sample library, add series, watch backlog queue fill and drain.
- Regression tests for renamer to ensure non-annual issues unaffected.

---
Pending Jeremy review. Once approved, we can start cutting issues / tasks per phase.
