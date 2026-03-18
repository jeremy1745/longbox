# Mylar vs LongBox Feature Matrix

| Area | Mylar | LongBox (current) | Gap / Notes | Priority |
| --- | --- | --- | --- | --- |
| Series discovery & metadata | ComicVine search, alternate ID sources, auto metadata refresh | Basic ComicVine lookup only | Need refresh jobs & multi-source fallbacks | High |
| Pull list import | Can subscribe to series, auto add upcoming issues, import entire runs | Manual add per series; no backlog autopull | Implement backlog/backfill queue w/ annual handling | High |
| Missing issue detection | Library scan with folder mapping, marks gaps, optional manual file scan | Lacks library awareness | Build NAS scanner (via SSH) + DB sync to skip owned issues | High |
| Annuals / Specials | Automatically detects and files into Annuals/ subfolder | Currently flat drop into series root | Add Annuals/ placement + metadata tags | High |
| Download queue management | Configurable slots, retry policy, snatch history, SAB/NZBGet integration | Basic NZB queue, no max slot or retry rules | Implement 25-slot limit + retry/backoff tracking | High |
| Variant handling | Recognizes variant codes, can prioritize or skip by pattern | Treats variants as unique issues but no policy | Add variant policy (default include; allow filters) | Medium |
| Post-processing | Renaming templates, script hooks, CBR/CBZ packaging | Simple rename only; no scripting | Add hook runner + packaging controls | Medium |
| Notifications | Email/Push for grabs, failures, upcoming issues | None | Decide on notif surface (Discord thread?) | Low |
| UI dashboards | Pull list dashboard, wanted list, history | Basic list views | Expand UI pages post-backend work | Low |

## Immediate next tasks

1. **Finish backlog/backfill design doc** – detail queue schema, annual placement, retry loop.  
2. **Prototype NAS library scan** – mount over SSH, catalog `E:/Comics`, store snapshot JSON.  
3. **Wire queue enforcement** – extend downloader to obey 25-slot cap + auto retries.  
4. **Document local-first inference workflow** – update `AGENTS.md` / `OPERATING_AGREEMENT.md` with Ollama-first, cloud escalation rules.
