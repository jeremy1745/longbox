# Mylar ↔ LongBox Feature Matrix (2026-03-20)

| Feature Area | Mylar (reference) | LongBox (current) | Gap / Action |
| --- | --- | --- | --- |
| **Library discovery & import** | Watches configured library paths, auto-creates series folders, supports manual import + folder monitoring. | Can scan a root library and build metadata, but no automated watch/import pipeline yet. | Implement scheduled NAS scans + delta detection so new files auto-register; add folder monitor or queued import job. |
| **Weekly pull lists** | Native pull-list builder (Diamond-style), auto-marks wanted issues, integrates stories/arcs. | Uses WalkSoftly release feed + manual want list UI. | Need parity on “auto mark” by watchlist, plus support for arcs / events tracking if desired. |
| **Backlog / gap-filling** | Can mark entire series (or specific ranges) as Wanted; auto-queues NZB/torrent jobs respecting availability. | Currently manual: user adds a series, then individually marks wanted issues; no automatic full-run / backlog queueing. | Build backlog automation: “Add series → queue all missing issues/respect annual folders (Annuals/), limit 25 concurrent NZBs with retries.” |
| **Annuals & special issues** | Treats annuals as normal issues but respects naming; user can control folder format (e.g., Annuals). | No dedicated rule; annuals land alongside regular issues. | Implement automatic redirect to `Annuals/` subfolder per series when ComicVine issue type = annual/special. |
| **Library awareness (already-owned issues)** | Folder scans + metadata matching mark local files as “Downloaded” and prevent duplicate grabs. | Scanner knows about existing files, but download queue doesn’t consult it before sending NZBs. | Integrate scanner results with queue logic so we only request missing issues (including variants). |
| **NZB / download clients** | Supports SABnzbd, NZBGet, TOR clients (transmission, rtorrent, deluge), DDL; ComicRN scripts for PP. | Built-in SABnzbd integration via Newznab/Prowlarr search; no NZBGet/Torrent yet. | Focus requirement: NZB-only. Need to harden SAB path, optionally add NZBGet later; ensure queue management + retries. |
| **Post-processing / renaming** | Highly configurable rename templates, ComicRN/Completed Download Handling, metadata injection. | Has naming template engine and can reorganize library; PP flow is more rudimentary. | Expand rename options (publisher/year tokens), add hooks for external scripts, consider metadata tagging. |
| **Story arcs / TPB / GN tracking** | Dedicated modules for arcs, TPBs, GN monitoring. | Not present. | Decide if arcs/TPB parity matters; if so, add data model + UI after core backlog work. |
| **Metadata providers** | ComicVine with per-issue detail, plus supplemental sources via scripts. | ComicVine only (already implemented). | No action unless we need backups. |
| **Reader experience** | Basic web reader (optional) + metadata editing. | Modern Svelte reader with progress tracking. | Already ahead; just keep stable. |
| **Automation / scripting** | Extensive script hooks, folder monitors, API for remote actions. | Has background jobs + SSE, but limited external API at the moment. | Future: expose REST hooks so automation (mcporter, etc.) can drive LB workflows. |
| **Security / multi-user** | Basic web auth; limited RBAC. | Single-user currently. | Not in scope now. |

## Priority Stack (next builds)

1. **Publish backlog automation spec** (this doc → implement).  
2. **Add annual handling + 25-slot NZB scheduler with retries.**  
3. **Library-aware queueing** (NAS scan feeding the downloader).  
4. **Extended rename / PP hooks** (post-backlog).  
5. **Optional:** arcs/TPB modules once core parity is met.  

## Notes
- Requirements supplied by Jeremy: NZB-only, max 25 concurrent downloads, annuals in `Annuals/`, variants included, auto-retry failures.  
- Library source: `smb://192.168.1.163/Comics` mounted via SSH key `/Users/jeremy/.ssh/longbox_nas`.  
- Spend discipline applies: prefer local development tooling.  
