<script lang="ts">
	import {
		ApiClient,
		type Settings,
		type APIKeyTestResult,
		type OrganizeTemplateResponse,
		type OrganizeTemplatePreviewResponse,
		type RenamePreview,
		type Indexer,
		type IndexerListResponse,
		type IndexerTestResult,
		type DownloadClient,
		type DownloadClientListResponse,
		type DownloadClientTestResult,
		type SlackSettings,
		type SlackTestResult,
	} from '$lib/api/client';
	import { getAuthState, type AuthUser } from '$lib/stores/auth.svelte';

	let settings = $state<Settings | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Library directory state
	let libraryDirInput = $state('');
	let libraryDirSaving = $state(false);
	let libraryDirMessage = $state<string | null>(null);

	// ComicVine API key state
	let apiKeyInput = $state('');
	let saving = $state(false);
	let testing = $state(false);
	let testResult = $state<APIKeyTestResult | null>(null);
	let saveMessage = $state<string | null>(null);

	// Metron credentials state
	let metronUsernameInput = $state('');
	let metronTokenInput = $state('');
	let metronSaving = $state(false);
	let metronSaveMessage = $state<string | null>(null);
	let metronTesting = $state(false);
	let metronTestResult = $state<{ valid: boolean; message: string; burst_remaining?: number; sustained_remaining?: number } | null>(null);

	// File organization state
	let templateInput = $state('');
	let templateLoading = $state(true);
	let templateSaving = $state(false);
	let templateMessage = $state<string | null>(null);
	let previewSamples = $state<RenamePreview[]>([]);
	let previewLoading = $state(false);
	let previewDebounce: ReturnType<typeof setTimeout> | null = null;

	// Indexer state
	let indexers = $state<Indexer[]>([]);
	let indexerEditing = $state<Partial<Indexer> | null>(null);
	let indexerSaving = $state(false);
	let indexerMessage = $state<string | null>(null);
	let indexerTesting = $state<number | null>(null);
	let indexerTestResult = $state<IndexerTestResult | null>(null);

	// Download client state
	let dlClients = $state<DownloadClient[]>([]);
	let dlClientEditing = $state<Partial<DownloadClient> | null>(null);
	let dlClientSaving = $state(false);
	let dlClientMessage = $state<string | null>(null);
	let dlClientTesting = $state<number | null>(null);
	let dlClientTestResult = $state<DownloadClientTestResult | null>(null);

	// Auto scan state
	let autoScanSaving = $state(false);
	let autoScanMessage = $state<string | null>(null);

	// Pull list schedule state
	let pullListSaving = $state(false);
	let pullListMessage = $state<string | null>(null);

	// Auto-search state
	let autoSearchSaving = $state(false);
	let autoSearchMessage = $state<string | null>(null);

	// Missing search state
	let missingSearchSaving = $state(false);
	let missingSearchMessage = $state<string | null>(null);

	// Scan reconciliation state
	let scanReconcileSaving = $state(false);
	let scanReconcileMessage = $state<string | null>(null);

	// Slack notification state
	let slackSettings = $state<SlackSettings | null>(null);
	let slackSaving = $state(false);
	let slackMessage = $state<string | null>(null);
	let slackTokenInput = $state('');
	let slackChannelInput = $state('');
	let slackTesting = $state(false);
	let slackTestResult = $state<SlackTestResult | null>(null);

	// LongBox sidecar metadata state
	let mylarWriting = $state(false);
	let mylarMessage = $state<string | null>(null);

	// Post-processing state
	let postProcessInput = $state('');
	let postProcessSaving = $state(false);
	let postProcessMessage = $state<string | null>(null);

	// Backup state
	let backups = $state<{ name: string; size: number; created_at: string }[]>([]);
	let backupCreating = $state(false);
	let backupMessage = $state<string | null>(null);
	let backupOnStartInput = $state(false);
	let backupRetentionInput = $state(5);
	let backupSettingSaving = $state(false);
	let backupSettingMessage = $state<string | null>(null);

	// Shutdown state
	let shutdownConfirming = $state(false);
	let shutdownTriggered = $state(false);

	// Read-status backfill state
	let readBackfillRunning = $state(false);
	let readBackfillMessage = $state<string | null>(null);

	// Want list prune state
	let pruneWantListRunning = $state(false);
	let pruneWantListMessage = $state<string | null>(null);

	// Dedupe issues state
	let dedupeIssuesRunning = $state(false);
	let dedupeIssuesMessage = $state<string | null>(null);

	// Dedupe series state
	let dedupeSeriesRunning = $state(false);
	let dedupeSeriesMessage = $state<string | null>(null);

	// Folder image state
	let folderImageRunning = $state(false);
	let folderImageMessage = $state<string | null>(null);

	// Backlog reconcile state
	let reconcileRunning = $state(false);
	let reconcileMessage = $state<string | null>(null);

	// Trash orphan files state
	type TrashOrphansResult = {
		scanned: number;
		files_trashed: number;
		bytes_reclaimed: number;
		dry_run: boolean;
		trashed?: string[];
		errors?: string[];
	};
	let orphanRunning = $state(false);
	let orphanPreview = $state<TrashOrphansResult | null>(null);
	let orphanMessage = $state<string | null>(null);

	async function previewOrphans() {
		orphanRunning = true;
		orphanMessage = null;
		try {
			orphanPreview = await ApiClient.post<TrashOrphansResult>('/admin/trash-orphan-files?dry=1');
			if (orphanPreview.scanned === 0) {
				orphanMessage = 'No orphan files (all comic_files have an issue link).';
			}
		} catch (e) {
			orphanMessage = e instanceof Error ? e.message : 'Preview failed';
		} finally {
			orphanRunning = false;
		}
	}

	async function applyTrashOrphans() {
		if (!orphanPreview || orphanPreview.scanned === 0) return;
		const mb = (orphanPreview.bytes_reclaimed / (1024 * 1024)).toFixed(1);
		if (!confirm(`Move ${orphanPreview.scanned} orphan file${orphanPreview.scanned === 1 ? '' : 's'} to the OS recycle bin? Reclaims ~${mb} MB. Reversible from the recycle bin.`)) return;
		orphanRunning = true;
		orphanMessage = null;
		try {
			const r = await ApiClient.post<TrashOrphansResult>('/admin/trash-orphan-files');
			orphanPreview = null;
			const errCount = (r.errors ?? []).length;
			orphanMessage = `Trashed ${r.files_trashed} of ${r.scanned} orphan files, reclaimed ${(r.bytes_reclaimed / (1024 * 1024)).toFixed(1)} MB${errCount > 0 ? ` · ${errCount} errors` : ''}.`;
		} catch (e) {
			orphanMessage = e instanceof Error ? e.message : 'Trash failed';
		} finally {
			orphanRunning = false;
		}
	}

	// Adopt stranded folders state
	type AdoptSubmitResult = { job_id: number; status: string; message: string };
	let adoptRunning = $state(false);
	let adoptMessage = $state<string | null>(null);

	async function adoptStrandedFolders() {
		if (!confirm('Walk every top-level folder under the library, parse SAB-style download folder names for series + issue + year, and reassign the comic files inside to the correct series. Files are NOT moved here — run Reorganize afterwards to consolidate. OK?')) return;
		adoptRunning = true;
		adoptMessage = null;
		try {
			const res = await ApiClient.post<AdoptSubmitResult>('/admin/adopt-folders');
			adoptMessage = `${res.message} (Job #${res.job_id})`;
		} catch (e) {
			adoptMessage = e instanceof Error ? e.message : 'Adopt failed';
		} finally {
			adoptRunning = false;
		}
	}

	// Library reorganize state
	type ReorgPreview = {
		dry_run: boolean;
		moves: number;
		conflicts: number;
		skipped: number;
		previews?: Array<{ file_id: number; current_path: string; new_path: string; status: string; reason?: string }>;
	};
	type ReorgSubmitResult = {
		job_id: number;
		status: string;
		message: string;
	};
	let reorgRunning = $state(false);
	let reorgPreview = $state<ReorgPreview | null>(null);
	let reorgMessage = $state<string | null>(null);

	// On-disk file dedupe state
	type FileDedupeDecision = {
		issue_id: number;
		kept_id: number;
		kept_path: string;
		kept_reason: string;
		trashed?: string[];
	};
	type FileDedupeResult = {
		groups_found: number;
		files_trashed: number;
		bytes_reclaimed: number;
		errors?: string[];
		dry_run: boolean;
		decisions?: FileDedupeDecision[];
	};
	let fileDedupeRunning = $state(false);
	let fileDedupePreview = $state<FileDedupeResult | null>(null);
	let fileDedupeMessage = $state<string | null>(null);

	const auth = getAuthState();

	// User management state
	let users = $state<AuthUser[]>([]);
	let userEditing = $state<{ username: string; password: string } | null>(null);
	let userSaving = $state(false);
	let userMessage = $state<string | null>(null);
	let passwordChanging = $state<number | null>(null);
	let newPasswordInput = $state('');
	let passwordMessage = $state<string | null>(null);

	const defaultTemplate = '{series}/{series} #{number|pad:3}.{format}';

	const dayNames = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

	const variables = [
		{ name: '{series}', desc: 'Series title' },
		{ name: '{sort_series}', desc: 'Sort-friendly title' },
		{ name: '{number}', desc: 'Issue number' },
		{ name: '{title}', desc: 'Issue title' },
		{ name: '{publisher}', desc: 'Publisher name' },
		{ name: '{format}', desc: 'File extension' },
		{ name: '{cover_date}', desc: 'Cover date' },
		{ name: '{store_date}', desc: 'Store date' },
		{ name: '{writers}', desc: 'First writer' },
		{ name: '{artists}', desc: 'First artist' },
	];

	const filters = [
		{ name: 'pad:N', example: '{number|pad:3}', desc: 'Zero-pad to N digits' },
		{ name: 'lower', example: '{series|lower}', desc: 'Lowercase' },
		{ name: 'upper', example: '{series|upper}', desc: 'Uppercase' },
	];

	async function loadSettings() {
		loading = true;
		error = null;
		try {
			settings = await ApiClient.get<Settings>('/settings');
			if (settings?.library_dir) {
				libraryDirInput = settings.library_dir;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load settings';
		} finally {
			loading = false;
		}
	}

	async function saveLibraryDir() {
		if (!libraryDirInput.trim()) return;
		libraryDirSaving = true;
		libraryDirMessage = null;
		try {
			await ApiClient.put<any>('/settings/library-dir', {
				library_dir: libraryDirInput.trim()
			});
			libraryDirMessage = 'Library directory updated! A scan has been started.';
			await loadSettings();
		} catch (e) {
			libraryDirMessage = e instanceof Error ? e.message : 'Failed to save library directory';
		} finally {
			libraryDirSaving = false;
		}
	}

	async function loadTemplate() {
		templateLoading = true;
		try {
			const data = await ApiClient.get<OrganizeTemplateResponse>('/library/organize/template');
			templateInput = data.template;
		} catch {
			templateInput = defaultTemplate;
		} finally {
			templateLoading = false;
		}
	}

	async function saveTemplate() {
		if (!templateInput.trim()) return;
		templateSaving = true;
		templateMessage = null;
		try {
			await ApiClient.put('/library/organize/template', { template: templateInput.trim() });
			templateMessage = 'Template saved successfully!';
		} catch (e) {
			templateMessage = e instanceof Error ? e.message : 'Failed to save template';
		} finally {
			templateSaving = false;
		}
	}

	async function loadPreviewSamples() {
		if (!templateInput.trim()) {
			previewSamples = [];
			return;
		}
		previewLoading = true;
		try {
			const data = await ApiClient.post<OrganizeTemplatePreviewResponse>('/library/organize/preview-template', {
				template: templateInput.trim()
			});
			previewSamples = data.samples || [];
		} catch {
			previewSamples = [];
		} finally {
			previewLoading = false;
		}
	}

	function onTemplateInput() {
		if (previewDebounce) clearTimeout(previewDebounce);
		previewDebounce = setTimeout(() => loadPreviewSamples(), 500);
	}

	function insertVariable(varName: string) {
		templateInput += varName;
		onTemplateInput();
	}

	function basename(path: string): string {
		return path.split('/').pop() || path;
	}

	async function savePostProcessScript() {
		postProcessSaving = true;
		postProcessMessage = null;
		try {
			await ApiClient.put('/settings/post-process-script', { script_path: postProcessInput.trim() });
			postProcessMessage = postProcessInput.trim() ? 'Post-processing script saved!' : 'Post-processing script cleared.';
			await loadSettings();
		} catch (e) {
			postProcessMessage = e instanceof Error ? e.message : 'Failed to save';
		} finally {
			postProcessSaving = false;
		}
	}

	async function loadBackups() {
		try {
			const data = await ApiClient.get<{ backups: typeof backups }>('/admin/backups');
			backups = data.backups || [];
		} catch { /* ignore */ }
	}

	async function createBackup() {
		backupCreating = true;
		backupMessage = null;
		try {
			const data = await ApiClient.post<{ name: string; message: string }>('/admin/backup');
			backupMessage = data.message || 'Backup created!';
			await loadBackups();
		} catch (e) {
			backupMessage = e instanceof Error ? e.message : 'Backup failed';
		} finally {
			backupCreating = false;
		}
	}

	async function deleteBackup(name: string) {
		if (!confirm(`Delete backup ${name}?`)) return;
		try {
			await ApiClient.delete(`/admin/backups/${encodeURIComponent(name)}`);
			backups = backups.filter(b => b.name !== name);
		} catch (e) {
			backupMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function saveBackupSettings() {
		backupSettingSaving = true;
		backupSettingMessage = null;
		try {
			await ApiClient.put('/settings/backup', {
				backup_on_start: backupOnStartInput,
				backup_retention: backupRetentionInput
			});
			backupSettingMessage = 'Backup settings saved!';
		} catch (e) {
			backupSettingMessage = e instanceof Error ? e.message : 'Failed to save';
		} finally {
			backupSettingSaving = false;
		}
	}

	async function dedupeSeries() {
		dedupeSeriesRunning = true;
		dedupeSeriesMessage = null;
		try {
			const result = await ApiClient.post<{
				groups_found: number;
				series_merged: number;
				issues_moved: number;
				issues_consolidated: number;
				files_relinked: number;
				errors?: string[];
			}>('/admin/dedupe-series');
			if (result.groups_found === 0) {
				dedupeSeriesMessage = 'No duplicate series rows found.';
			} else {
				dedupeSeriesMessage =
					`Merged ${result.series_merged} duplicate series across ${result.groups_found} group${result.groups_found === 1 ? '' : 's'} ` +
					`(${result.issues_moved} issues moved, ${result.issues_consolidated} consolidated, ${result.files_relinked} files relinked)`;
				if (result.errors && result.errors.length > 0) {
					dedupeSeriesMessage += ` · Error: ${result.errors[0]}`;
					if (result.errors.length > 1) {
						dedupeSeriesMessage += ` (+${result.errors.length - 1} more)`;
					}
				}
			}
		} catch (e) {
			dedupeSeriesMessage = e instanceof Error ? e.message : 'Dedupe failed';
		} finally {
			dedupeSeriesRunning = false;
		}
	}

	async function dedupeIssues() {
		dedupeIssuesRunning = true;
		dedupeIssuesMessage = null;
		try {
			const result = await ApiClient.post<{
				groups_found: number;
				issues_deleted: number;
				files_relinked: number;
				wants_consolidated: number;
				arc_links_copied: number;
				errors?: string[];
			}>('/admin/dedupe-issues');
			if (result.groups_found === 0) {
				dedupeIssuesMessage = 'No duplicate issue rows found.';
			} else {
				dedupeIssuesMessage =
					`Merged ${result.groups_found} group${result.groups_found === 1 ? '' : 's'} ` +
					`(deleted ${result.issues_deleted}, relinked ${result.files_relinked} file${result.files_relinked === 1 ? '' : 's'})`;
				if (result.errors && result.errors.length > 0) {
					dedupeIssuesMessage += ` · ${result.errors.length} error${result.errors.length === 1 ? '' : 's'} — see server log.`;
				}
			}
		} catch (e) {
			dedupeIssuesMessage = e instanceof Error ? e.message : 'Dedupe failed';
		} finally {
			dedupeIssuesRunning = false;
		}
	}

	async function pruneWantList() {
		pruneWantListRunning = true;
		pruneWantListMessage = null;
		try {
			const result = await ApiClient.post<{ removed: number }>('/admin/prune-want-list');
			pruneWantListMessage = result.removed > 0
				? `Removed ${result.removed} fulfilled want list ${result.removed === 1 ? 'entry' : 'entries'}.`
				: 'No fulfilled entries to prune.';
		} catch (e) {
			pruneWantListMessage = e instanceof Error ? e.message : 'Prune failed';
		} finally {
			pruneWantListRunning = false;
		}
	}

	async function backfillReadStatus() {
		readBackfillRunning = true;
		readBackfillMessage = null;
		try {
			const result = await ApiClient.post<{ promoted: number }>('/admin/backfill-read-status');
			readBackfillMessage = result.promoted > 0
				? `Promoted ${result.promoted} issue${result.promoted === 1 ? '' : 's'} from "Reading" to "Read".`
				: 'No issues to promote — every "Reading" issue has either no recorded progress or is not at the last page.';
		} catch (e) {
			readBackfillMessage = e instanceof Error ? e.message : 'Backfill failed';
		} finally {
			readBackfillRunning = false;
		}
	}

	async function writeLongboxMetadata() {
		mylarWriting = true;
		mylarMessage = null;
		try {
			const result = await ApiClient.post<{ job_id: number; total_series: number; message: string }>(
				'/library/write-longbox-metadata'
			);
			mylarMessage = `${result.message} (${result.total_series} series, Job #${result.job_id})`;
		} catch (e) {
			mylarMessage = e instanceof Error ? e.message : 'Failed to start';
		} finally {
			mylarWriting = false;
		}
	}

	async function writeFolderImages() {
		folderImageRunning = true;
		folderImageMessage = null;
		try {
			const result = await ApiClient.post<{ job_id: number; message: string }>(
				'/library/write-folder-images'
			);
			folderImageMessage = `${result.message} (Job #${result.job_id})`;
		} catch (e) {
			folderImageMessage = e instanceof Error ? e.message : 'Failed to start';
		} finally {
			folderImageRunning = false;
		}
	}

	async function previewReorganize() {
		reorgRunning = true;
		reorgMessage = null;
		reorgResult = null;
		try {
			reorgPreview = await ApiClient.post<ReorgPreview>('/admin/reorganize?dry=1');
			if (reorgPreview.moves === 0 && reorgPreview.conflicts === 0) {
				reorgMessage = 'Library is already organized — nothing to move.';
			}
		} catch (e) {
			reorgMessage = e instanceof Error ? e.message : 'Preview failed';
		} finally {
			reorgRunning = false;
		}
	}

	async function applyReorganize() {
		if (!reorgPreview || reorgPreview.moves === 0) return;
		const msg = reorgPreview.conflicts > 0
			? `Submit reorganize of ${reorgPreview.moves} file${reorgPreview.moves === 1 ? '' : 's'}? ${reorgPreview.conflicts} conflict${reorgPreview.conflicts === 1 ? '' : 's'} will be skipped — review preview first. Runs in the background; track on the Jobs page.`
			: `Submit reorganize of ${reorgPreview.moves} file${reorgPreview.moves === 1 ? '' : 's'} into the canonical "Series (Year)/Series (Year) NNN.ext" layout? Runs in the background — you can navigate away. Track progress in the active-job banner or on the Jobs page.`;
		if (!confirm(msg)) return;
		reorgRunning = true;
		reorgMessage = null;
		try {
			const submit = await ApiClient.post<ReorgSubmitResult>('/admin/reorganize');
			reorgPreview = null;
			reorgMessage = `${submit.message} (Job #${submit.job_id})`;
		} catch (e) {
			reorgMessage = e instanceof Error ? e.message : 'Reorganize failed';
		} finally {
			reorgRunning = false;
		}
	}

	function formatBytes(n: number): string {
		if (n <= 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.floor(Math.log(n) / Math.log(1024));
		return `${(n / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
	}

	async function previewFileDedupe() {
		fileDedupeRunning = true;
		fileDedupeMessage = null;
		try {
			fileDedupePreview = await ApiClient.post<FileDedupeResult>('/admin/dedupe-files?dry=1');
			if (fileDedupePreview.groups_found === 0) {
				fileDedupeMessage = 'No duplicate files found on disk.';
			}
		} catch (e) {
			fileDedupeMessage = e instanceof Error ? e.message : 'Preview failed';
		} finally {
			fileDedupeRunning = false;
		}
	}

	async function applyFileDedupe() {
		if (!fileDedupePreview || fileDedupePreview.groups_found === 0) return;
		const total = fileDedupePreview.decisions?.reduce((n, d) => n + (d.trashed?.length ?? 0), 0) ?? 0;
		if (!confirm(`Move ${total} duplicate file${total === 1 ? '' : 's'} to the OS recycle bin? This is reversible from the system trash.`)) {
			return;
		}
		fileDedupeRunning = true;
		fileDedupeMessage = null;
		try {
			const result = await ApiClient.post<FileDedupeResult>('/admin/dedupe-files');
			fileDedupePreview = result;
			fileDedupeMessage =
				`Trashed ${result.files_trashed} file${result.files_trashed === 1 ? '' : 's'}, ` +
				`reclaimed ${formatBytes(result.bytes_reclaimed)}.` +
				(result.errors && result.errors.length > 0 ? ` ${result.errors.length} error${result.errors.length === 1 ? '' : 's'} — see server log.` : '');
		} catch (e) {
			fileDedupeMessage = e instanceof Error ? e.message : 'Dedupe failed';
		} finally {
			fileDedupeRunning = false;
		}
	}

	async function reconcileBacklog() {
		reconcileRunning = true;
		reconcileMessage = null;
		try {
			const result = await ApiClient.post<{ reconciled: number }>('/admin/reconcile-backlog');
			reconcileMessage = result.reconciled > 0
				? `Marked ${result.reconciled} backlog item${result.reconciled === 1 ? '' : 's'} completed (already owned or grabbed).`
				: 'Nothing to reconcile — no failed/pending items match an owned issue.';
		} catch (e) {
			reconcileMessage = e instanceof Error ? e.message : 'Reconcile failed';
		} finally {
			reconcileRunning = false;
		}
	}

	async function saveMetronCredentials() {
		const username = metronUsernameInput.trim();
		const token = metronTokenInput.trim();
		if (!username || !token) return;
		metronSaving = true;
		metronSaveMessage = null;
		metronTestResult = null;
		try {
			await ApiClient.put('/settings/metron', { username, api_token: token });
			metronTokenInput = '';
			metronSaveMessage = 'Metron credentials saved.';
			await loadSettings();
		} catch (e) {
			metronSaveMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			metronSaving = false;
		}
	}

	async function testMetron() {
		metronTesting = true;
		metronTestResult = null;
		try {
			metronTestResult = await ApiClient.post<{
				valid: boolean;
				message: string;
				burst_remaining?: number;
				sustained_remaining?: number;
			}>('/settings/metron/test');
			if (metronTestResult.valid) {
				await loadSettings();
			}
		} catch (e) {
			metronTestResult = { valid: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			metronTesting = false;
		}
	}

	async function saveAPIKey() {
		if (!apiKeyInput.trim()) return;
		saving = true;
		saveMessage = null;
		testResult = null;
		try {
			const result = await ApiClient.put<any>('/settings/comicvine-api-key', {
				api_key: apiKeyInput.trim()
			});
			apiKeyInput = '';
			saveMessage = 'API key saved successfully!';
			await loadSettings();
		} catch (e) {
			saveMessage = e instanceof Error ? e.message : 'Failed to save API key';
		} finally {
			saving = false;
		}
	}

	async function testAPIKey() {
		testing = true;
		testResult = null;
		try {
			testResult = await ApiClient.post<APIKeyTestResult>('/settings/comicvine-api-key/test');
		} catch (e) {
			testResult = {
				valid: false,
				message: e instanceof Error ? e.message : 'Test failed'
			};
		} finally {
			testing = false;
		}
	}

	// --- Indexer functions ---

	async function loadIndexers() {
		try {
			const data = await ApiClient.get<IndexerListResponse>('/indexers');
			indexers = data.indexers || [];
		} catch { /* ignore */ }
	}

	function newIndexer() {
		indexerEditing = { name: '', url: '', api_key: '', type: 'newznab', priority: 50, categories: '7030' };
		indexerMessage = null;
	}

	function editIndexer(idx: Indexer) {
		indexerEditing = { ...idx, api_key: '' };
		indexerMessage = null;
	}

	async function saveIndexer() {
		if (!indexerEditing) return;
		indexerSaving = true;
		indexerMessage = null;
		try {
			if (indexerEditing.id) {
				const body: Record<string, any> = {};
				if (indexerEditing.name) body.name = indexerEditing.name;
				if (indexerEditing.url) body.url = indexerEditing.url;
				if (indexerEditing.api_key) body.api_key = indexerEditing.api_key;
				if (indexerEditing.type) body.type = indexerEditing.type;
				if (indexerEditing.priority != null) body.priority = indexerEditing.priority;
				if (indexerEditing.categories) body.categories = indexerEditing.categories;
				await ApiClient.put(`/indexers/${indexerEditing.id}`, body);
			} else {
				await ApiClient.post('/indexers', indexerEditing);
			}
			indexerEditing = null;
			await loadIndexers();
		} catch (e) {
			indexerMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			indexerSaving = false;
		}
	}

	async function deleteIndexer(id: number) {
		try {
			await ApiClient.delete(`/indexers/${id}`);
			await loadIndexers();
		} catch (e) {
			indexerMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function testIndexer(id: number) {
		indexerTesting = id;
		indexerTestResult = null;
		try {
			indexerTestResult = await ApiClient.post<IndexerTestResult>(`/indexers/${id}/test`);
		} catch (e) {
			indexerTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			indexerTesting = null;
		}
	}

	// --- Download Client functions ---

	async function loadDlClients() {
		try {
			const data = await ApiClient.get<DownloadClientListResponse>('/download-clients');
			dlClients = data.download_clients || [];
		} catch { /* ignore */ }
	}

	function newDlClient() {
		dlClientEditing = { name: '', url: '', api_key: '', type: 'sabnzbd', category: 'comics', priority: 50 };
		dlClientMessage = null;
	}

	function editDlClient(dc: DownloadClient) {
		dlClientEditing = { ...dc, api_key: '' };
		dlClientMessage = null;
	}

	async function saveDlClient() {
		if (!dlClientEditing) return;
		dlClientSaving = true;
		dlClientMessage = null;
		try {
			if (dlClientEditing.id) {
				const body: Record<string, any> = {};
				if (dlClientEditing.name) body.name = dlClientEditing.name;
				if (dlClientEditing.url) body.url = dlClientEditing.url;
				if (dlClientEditing.api_key) body.api_key = dlClientEditing.api_key;
				if (dlClientEditing.category) body.category = dlClientEditing.category;
				if (dlClientEditing.priority != null) body.priority = dlClientEditing.priority;
				await ApiClient.put(`/download-clients/${dlClientEditing.id}`, body);
			} else {
				await ApiClient.post('/download-clients', dlClientEditing);
			}
			dlClientEditing = null;
			await loadDlClients();
		} catch (e) {
			dlClientMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			dlClientSaving = false;
		}
	}

	async function deleteDlClient(id: number) {
		try {
			await ApiClient.delete(`/download-clients/${id}`);
			await loadDlClients();
		} catch (e) {
			dlClientMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function testDlClient(id: number) {
		dlClientTesting = id;
		dlClientTestResult = null;
		try {
			dlClientTestResult = await ApiClient.post<DownloadClientTestResult>(`/download-clients/${id}/test`);
		} catch (e) {
			dlClientTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			dlClientTesting = null;
		}
	}

	// --- Auto Scan ---

	async function saveAutoScan(field: string, value: any) {
		autoScanSaving = true;
		autoScanMessage = null;
		try {
			await ApiClient.put('/settings/auto-scan', { [field]: value });
			await loadSettings();
			autoScanMessage = 'Setting updated!';
		} catch (e) {
			autoScanMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			autoScanSaving = false;
		}
	}

	// --- Scan reconciliation ---

	async function saveScanReconcile(field: string, value: any) {
		scanReconcileSaving = true;
		scanReconcileMessage = null;
		try {
			await ApiClient.put('/settings/scan-reconcile', { [field]: value });
			await loadSettings();
			scanReconcileMessage = 'Setting updated!';
		} catch (e) {
			scanReconcileMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			scanReconcileSaving = false;
		}
	}

	// --- Pull List Schedule ---

	async function savePullListSchedule(field: string, value: any) {
		pullListSaving = true;
		pullListMessage = null;
		try {
			await ApiClient.put('/settings/pull-list-schedule', { [field]: value });
			await loadSettings();
			pullListMessage = 'Schedule updated!';
		} catch (e) {
			pullListMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			pullListSaving = false;
		}
	}

	// --- Auto-search on add ---

	async function saveAutoSearch(enabled: boolean) {
		autoSearchSaving = true;
		autoSearchMessage = null;
		try {
			await ApiClient.put('/settings/auto-search', { enabled });
			await loadSettings();
			autoSearchMessage = 'Setting updated!';
		} catch (e) {
			autoSearchMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			autoSearchSaving = false;
		}
	}

	// --- Missing search ---

	async function saveMissingSearch(field: string, value: any) {
		missingSearchSaving = true;
		missingSearchMessage = null;
		try {
			await ApiClient.put('/settings/missing-search', { [field]: value });
			await loadSettings();
			missingSearchMessage = 'Setting updated!';
		} catch (e) {
			missingSearchMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			missingSearchSaving = false;
		}
	}

	// --- Slack notification functions ---

	async function loadSlackSettings() {
		try {
			slackSettings = await ApiClient.get<SlackSettings>('/settings/slack');
			slackChannelInput = slackSettings.slack_channel || '';
		} catch { /* ignore */ }
	}

	async function saveSlackSetting(key: string, value: boolean) {
		slackSaving = true;
		slackMessage = null;
		try {
			await ApiClient.put('/settings/slack', { [key]: value });
			await loadSlackSettings();
			slackMessage = 'Setting updated!';
		} catch (e) {
			slackMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			slackSaving = false;
		}
	}

	async function saveSlackToken() {
		if (!slackTokenInput.trim()) return;
		slackSaving = true;
		slackMessage = null;
		try {
			await ApiClient.put('/settings/slack', { slack_bot_token: slackTokenInput.trim() });
			slackTokenInput = '';
			await loadSlackSettings();
			slackMessage = 'Bot token saved!';
		} catch (e) {
			slackMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			slackSaving = false;
		}
	}

	async function saveSlackChannel() {
		slackSaving = true;
		slackMessage = null;
		try {
			await ApiClient.put('/settings/slack', { slack_channel: slackChannelInput.trim() });
			await loadSlackSettings();
			slackMessage = 'Channel saved!';
		} catch (e) {
			slackMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			slackSaving = false;
		}
	}

	async function testSlack() {
		slackTesting = true;
		slackTestResult = null;
		try {
			slackTestResult = await ApiClient.post<SlackTestResult>('/settings/slack/test');
		} catch (e) {
			slackTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			slackTesting = false;
		}
	}

	const slackEventToggles = [
		{ key: 'slack_notify_scan_complete', label: 'Scan Complete', desc: 'When a library scan finishes' },
		{ key: 'slack_notify_metadata_refresh_complete', label: 'Metadata Refresh Complete', desc: 'When a metadata refresh finishes' },
		{ key: 'slack_notify_pull_list_search_complete', label: 'Pull List Search Complete', desc: 'When a pull list search finishes' },
		{ key: 'slack_notify_download_grabbed', label: 'Download Grabbed', desc: 'When an NZB is grabbed from an indexer' },
		{ key: 'slack_notify_download_complete', label: 'Download Complete', desc: 'When a download finishes successfully' },
		{ key: 'slack_notify_download_failed', label: 'Download Failed', desc: 'When a download fails' },
		{ key: 'slack_notify_missing_search_complete', label: 'Missing Search Found', desc: 'When the missing issue search grabs new issues' },
	];

	// --- User management functions ---

	async function loadUsers() {
		try {
			const data = await ApiClient.get<{ users: AuthUser[] }>('/auth/users');
			users = data.users || [];
		} catch { /* ignore */ }
	}

	function newUser() {
		userEditing = { username: '', password: '' };
		userMessage = null;
	}

	async function saveUser() {
		if (!userEditing) return;
		userSaving = true;
		userMessage = null;
		try {
			await ApiClient.post('/auth/users', userEditing);
			userEditing = null;
			await loadUsers();
			userMessage = 'User created successfully!';
		} catch (e) {
			userMessage = e instanceof Error ? e.message : 'Failed to create user';
		} finally {
			userSaving = false;
		}
	}

	async function deleteUser(id: number) {
		userMessage = null;
		try {
			await ApiClient.delete(`/auth/users/${id}`);
			await loadUsers();
		} catch (e) {
			userMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	function startPasswordChange(id: number) {
		passwordChanging = id;
		newPasswordInput = '';
		passwordMessage = null;
	}

	async function savePasswordChange() {
		if (passwordChanging == null || !newPasswordInput) return;
		passwordMessage = null;
		try {
			await ApiClient.put(`/auth/users/${passwordChanging}/password`, { password: newPasswordInput });
			passwordChanging = null;
			newPasswordInput = '';
			passwordMessage = 'Password changed successfully!';
		} catch (e) {
			passwordMessage = e instanceof Error ? e.message : 'Failed to change password';
		}
	}

	async function shutdownServer() {
		shutdownTriggered = true;
		shutdownConfirming = false;
		await ApiClient.shutdownServer();
	}

	$effect(() => {
		loadSettings().then(() => {
			if (settings) {
				postProcessInput = settings.post_process_script || '';
				backupOnStartInput = settings.backup_on_start ?? false;
				backupRetentionInput = settings.backup_retention ?? 5;
			}
		});
		loadTemplate();
		loadIndexers();
		loadDlClients();
		loadSlackSettings();
		loadBackups();
		if (auth.user?.is_admin) {
			loadUsers();
		}
	});
</script>

<div class="space-y-8">
	<div>
		<h1 class="text-3xl font-bold">Settings</h1>
		<p class="text-gray-400 mt-1">Configure LongBox</p>
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else}
		<!-- Library Directory Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Library Directory</h2>
			<p class="text-sm text-gray-400 mb-6">
				The folder where LongBox looks for your comic book files (CBZ, CBR, CB7, PDF).
				All subfolders are scanned automatically.
			</p>

			<!-- Current path display -->
			<div class="mb-6">
				<div class="flex items-center gap-3">
					<span class="text-sm text-gray-400">Current path:</span>
					<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
						{settings?.library_dir || 'Not set'}
					</code>
				</div>
			</div>

			<!-- Directory input -->
			<div class="space-y-3">
				<label for="library-dir" class="block text-sm font-medium text-gray-300">
					Change Library Directory
				</label>
				<div class="flex gap-3">
					<input
						id="library-dir"
						type="text"
						bind:value={libraryDirInput}
						placeholder="/path/to/your/comics"
						class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
							text-gray-100 placeholder-gray-500 font-mono text-sm
							focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
						onkeydown={(e) => e.key === 'Enter' && saveLibraryDir()}
					/>
					<button
						onclick={saveLibraryDir}
						disabled={libraryDirSaving || !libraryDirInput.trim() || libraryDirInput.trim() === settings?.library_dir}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
					>
						{libraryDirSaving ? 'Saving...' : 'Save & Scan'}
					</button>
				</div>
			</div>

			{#if libraryDirMessage}
				<p class="mt-3 text-sm {libraryDirMessage.includes('updated') ? 'text-green-400' : 'text-red-400'}">
					{libraryDirMessage}
				</p>
			{/if}
		</div>

		<!-- File Organization Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">File Organization</h2>
				<a
					href="/settings/organize"
					class="px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-200 text-sm font-medium rounded-lg transition-colors"
				>
					Preview & Organize
				</a>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Define a naming template to organize your comic files into a consistent folder structure.
				Use <code class="text-amber-400">/</code> in the template to create subdirectories.
			</p>

			<!-- Template input -->
			{#if templateLoading}
				<div class="text-gray-400 text-sm">Loading template...</div>
			{:else}
				<div class="space-y-4">
					<div>
						<label for="naming-template" class="block text-sm font-medium text-gray-300 mb-2">
							Naming Template
						</label>
						<div class="flex gap-3">
							<input
								id="naming-template"
								type="text"
								bind:value={templateInput}
								oninput={onTemplateInput}
								placeholder={defaultTemplate}
								class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
									text-gray-100 placeholder-gray-500 font-mono text-sm
									focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
							/>
							<button
								onclick={saveTemplate}
								disabled={templateSaving || !templateInput.trim()}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
									disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
							>
								{templateSaving ? 'Saving...' : 'Save'}
							</button>
						</div>
						{#if templateMessage}
							<p class="mt-2 text-sm {templateMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">
								{templateMessage}
							</p>
						{/if}
					</div>

					<!-- Variable chips -->
					<div>
						<p class="text-xs text-gray-400 mb-2">Click to insert variable:</p>
						<div class="flex flex-wrap gap-1.5">
							{#each variables as v}
								<button
									onclick={() => insertVariable(v.name)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-amber-400 rounded font-mono transition-colors"
									title={v.desc}
								>
									{v.name}
								</button>
							{/each}
						</div>
					</div>

					<!-- Filters reference -->
					<div>
						<p class="text-xs text-gray-400 mb-2">Filters (pipe syntax):</p>
						<div class="flex flex-wrap gap-3 text-xs text-gray-500">
							{#each filters as f}
								<span>
									<code class="text-gray-300">{f.example}</code> — {f.desc}
								</span>
							{/each}
						</div>
					</div>

					<!-- Live preview -->
					{#if previewSamples.length > 0}
						<div class="mt-4 pt-4 border-t border-gray-700">
							<p class="text-xs text-gray-400 mb-2">Preview (sample renames):</p>
							<div class="space-y-2">
								{#each previewSamples as sample}
									<div class="text-xs bg-gray-900/50 rounded p-2 space-y-1">
										<div class="text-gray-500 truncate" title={sample.current_path}>
											{basename(sample.current_path)}
										</div>
										<div class="text-green-400 truncate" title={sample.new_path}>
											&rarr; {sample.new_path.split('/').slice(-2).join('/')}
										</div>
									</div>
								{/each}
							</div>
						</div>
					{:else if previewLoading}
						<div class="mt-4 pt-4 border-t border-gray-700">
							<p class="text-xs text-gray-400">Loading preview...</p>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- LongBox Metadata Sidecars Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">LongBox Metadata Sidecars</h2>
			<p class="text-sm text-gray-400 mb-6">
				Write LongBox-native sidecars to each series folder. Creates a
				<code class="text-amber-400 bg-gray-900 px-1 rounded">longbox-series.json</code> file for tools and a
				<code class="text-amber-400 bg-gray-900 px-1 rounded">longbox-series.txt</code> summary for humans.
				Files are only rewritten when their content changes.
			</p>

			<button
				onclick={writeLongboxMetadata}
				disabled={mylarWriting}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
			>
				{mylarWriting ? 'Starting...' : 'Write LongBox Sidecars'}
			</button>

			{#if mylarMessage}
				<p class="mt-3 text-sm {mylarMessage.includes('Failed') ? 'text-red-400' : 'text-green-400'}">
					{mylarMessage}
				</p>
			{/if}
		</div>

		<!-- ComicVine API Key Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">ComicVine API</h2>
			<p class="text-sm text-gray-400 mb-6">
				Connect to ComicVine to fetch rich metadata for your comics including descriptions,
				creators, cover art, and complete issue lists.
				<a href="https://comicvine.gamespot.com/api/" target="_blank" rel="noopener"
					class="text-amber-400 hover:text-amber-300">Get a free API key</a>.
			</p>

			<!-- Current status -->
			<div class="mb-6 space-y-2">
				<div class="flex items-center gap-3">
					<span class="text-sm text-gray-400">Status:</span>
					{#if settings?.comicvine_api_key_set}
						<span class="inline-flex items-center gap-1.5 text-sm text-green-400">
							<span class="w-2 h-2 bg-green-400 rounded-full"></span>
							Connected
						</span>
					{:else}
						<span class="inline-flex items-center gap-1.5 text-sm text-yellow-400">
							<span class="w-2 h-2 bg-yellow-400 rounded-full"></span>
							Not configured
						</span>
					{/if}
				</div>

				{#if settings?.comicvine_api_key_set}
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Key:</span>
						<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
							{settings.comicvine_api_key_masked}
						</code>
					</div>
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Source:</span>
						<span class="text-sm text-gray-300 capitalize">{settings.comicvine_api_key_source}</span>
					</div>
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Hourly requests remaining:</span>
						<span class="text-sm text-gray-300">{settings.comicvine_hourly_remaining}</span>
					</div>
				{/if}
			</div>

			<!-- API Key input -->
			<div class="space-y-3">
				<label for="api-key" class="block text-sm font-medium text-gray-300">
					{settings?.comicvine_api_key_set ? 'Update API Key' : 'Enter API Key'}
				</label>
				<div class="flex gap-3">
					<input
						id="api-key"
						type="password"
						bind:value={apiKeyInput}
						placeholder="Enter your ComicVine API key"
						class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
							text-gray-100 placeholder-gray-500
							focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
						onkeydown={(e) => e.key === 'Enter' && saveAPIKey()}
					/>
					<button
						onclick={saveAPIKey}
						disabled={saving || !apiKeyInput.trim()}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
					>
						{saving ? 'Saving...' : 'Save'}
					</button>
				</div>
			</div>

			{#if saveMessage}
				<p class="mt-3 text-sm {saveMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">
					{saveMessage}
				</p>
			{/if}

			<!-- Test button -->
			{#if settings?.comicvine_api_key_set}
				<div class="mt-4 pt-4 border-t border-gray-700">
					<button
						onclick={testAPIKey}
						disabled={testing}
						class="px-4 py-2 bg-gray-700 hover:bg-gray-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-200 font-medium rounded-lg transition-colors text-sm"
					>
						{testing ? 'Testing...' : 'Test API Key'}
					</button>

					{#if testResult}
						<div class="mt-3 p-3 rounded-lg {testResult.valid ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
							<p class="text-sm {testResult.valid ? 'text-green-400' : 'text-red-400'}">
								{testResult.message}
							</p>
							{#if testResult.valid && testResult.hourly_remaining !== undefined}
								<p class="text-xs text-gray-400 mt-1">
									{testResult.hourly_remaining} hourly requests remaining
								</p>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- Metron Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Metron API</h2>
			<p class="text-sm text-gray-400 mb-6">
				Secondary metadata source. Far more generous quota (20/min burst, 5000/day)
				than ComicVine. Use as a fallback or as a primary for series Metron has covered.
				<a href="https://metron.cloud/" target="_blank" rel="noopener" class="text-amber-400 hover:text-amber-300">Sign up</a>,
				then create an API token under your user settings.
			</p>

			<div class="mb-6 space-y-2">
				<div class="flex items-center gap-3">
					<span class="text-sm text-gray-400">Status:</span>
					{#if settings?.metron_token_set}
						<span class="inline-flex items-center gap-1.5 text-sm text-green-400">
							<span class="w-2 h-2 bg-green-400 rounded-full"></span>
							Connected
						</span>
					{:else}
						<span class="inline-flex items-center gap-1.5 text-sm text-yellow-400">
							<span class="w-2 h-2 bg-yellow-400 rounded-full"></span>
							Not configured
						</span>
					{/if}
				</div>

				{#if settings?.metron_token_set}
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Username:</span>
						<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
							{settings.metron_username || '(unset)'}
						</code>
					</div>
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Token:</span>
						<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
							{settings.metron_token_masked || '••••••'}
						</code>
					</div>
					{#if settings.metron_burst_remaining > 0 || settings.metron_sustained_remaining > 0}
						<div class="flex items-center gap-3">
							<span class="text-sm text-gray-400">Quota:</span>
							<span class="text-sm text-gray-300">
								{settings.metron_burst_remaining} burst /
								{settings.metron_sustained_remaining} daily remaining
							</span>
						</div>
					{/if}
				{/if}
			</div>

			<div class="space-y-3">
				<label for="metron-username" class="block text-sm font-medium text-gray-300">Username</label>
				<input
					id="metron-username"
					type="text"
					bind:value={metronUsernameInput}
					placeholder="Your metron.cloud username"
					class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-100 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
				/>

				<label for="metron-token" class="block text-sm font-medium text-gray-300">API Token</label>
				<div class="flex gap-3">
					<input
						id="metron-token"
						type="password"
						bind:value={metronTokenInput}
						placeholder={settings?.metron_token_set ? 'Enter a new token to replace' : 'Paste your Metron API token'}
						class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-100 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
						onkeydown={(e) => e.key === 'Enter' && saveMetronCredentials()}
					/>
					<button
						onclick={saveMetronCredentials}
						disabled={metronSaving || !metronUsernameInput.trim() || !metronTokenInput.trim()}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
					>
						{metronSaving ? 'Saving...' : 'Save'}
					</button>
				</div>
			</div>

			{#if metronSaveMessage}
				<p class="mt-3 text-sm {metronSaveMessage.includes('saved') ? 'text-green-400' : 'text-red-400'}">
					{metronSaveMessage}
				</p>
			{/if}

			{#if settings?.metron_token_set}
				<div class="mt-4 pt-4 border-t border-gray-700">
					<button
						onclick={testMetron}
						disabled={metronTesting}
						class="px-4 py-2 bg-gray-700 hover:bg-gray-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-200 font-medium rounded-lg transition-colors text-sm"
					>
						{metronTesting ? 'Testing...' : 'Test Connection'}
					</button>

					{#if metronTestResult}
						<div class="mt-3 p-3 rounded-lg {metronTestResult.valid ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
							<p class="text-sm {metronTestResult.valid ? 'text-green-400' : 'text-red-400'}">
								{metronTestResult.message}
							</p>
							{#if metronTestResult.valid && (metronTestResult.burst_remaining !== undefined || metronTestResult.sustained_remaining !== undefined)}
								<p class="text-xs text-gray-400 mt-1">
									{metronTestResult.burst_remaining ?? '?'} burst /
									{metronTestResult.sustained_remaining ?? '?'} daily remaining
								</p>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- Indexers Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">Indexers</h2>
				<button
					onclick={newIndexer}
					class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
				>
					Add Indexer
				</button>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Configure Usenet indexers (Newznab, NZBHydra2, Prowlarr) to search for comics.
			</p>

			{#if indexerMessage}
				<p class="mb-4 text-sm text-red-400">{indexerMessage}</p>
			{/if}

			{#if indexerTestResult}
				<div class="mb-4 p-3 rounded-lg {indexerTestResult.success ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
					<p class="text-sm {indexerTestResult.success ? 'text-green-400' : 'text-red-400'}">{indexerTestResult.message}</p>
				</div>
			{/if}

			<!-- Indexer edit form -->
			{#if indexerEditing}
				<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Name</label>
							<input type="text" bind:value={indexerEditing.name} placeholder="My Indexer"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Type</label>
							<select bind:value={indexerEditing.type}
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500">
								<option value="newznab">Newznab</option>
								<option value="nzbhydra2">NZBHydra2</option>
								<option value="prowlarr">Prowlarr</option>
							</select>
						</div>
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">URL</label>
						<input type="text" bind:value={indexerEditing.url} placeholder="https://my-indexer.com"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">API Key</label>
						<input type="password" bind:value={indexerEditing.api_key} placeholder={indexerEditing.id ? '(leave blank to keep current)' : 'API key'}
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Categories</label>
							<input type="text" bind:value={indexerEditing.categories} placeholder="7030"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Priority</label>
							<input type="number" bind:value={indexerEditing.priority} min="1" max="100"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
					</div>
					<div class="flex gap-2 pt-2">
						<button onclick={saveIndexer} disabled={indexerSaving}
							class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
							{indexerSaving ? 'Saving...' : 'Save'}
						</button>
						<button onclick={() => indexerEditing = null}
							class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
							Cancel
						</button>
					</div>
				</div>
			{/if}

			<!-- Indexer list -->
			{#if indexers.length > 0}
				<div class="divide-y divide-gray-700">
					{#each indexers as idx (idx.id)}
						<div class="py-3 flex items-center justify-between gap-4">
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-gray-200">{idx.name}</span>
									<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">{idx.type}</span>
									{#if !idx.enabled}
										<span class="text-xs px-1.5 py-0.5 rounded bg-red-900/50 text-red-400">Disabled</span>
									{/if}
								</div>
								<p class="text-xs text-gray-500 mt-0.5 truncate font-mono">{idx.url}</p>
							</div>
							<div class="flex items-center gap-1">
								<button onclick={() => testIndexer(idx.id)} disabled={indexerTesting === idx.id}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									{indexerTesting === idx.id ? '...' : 'Test'}
								</button>
								<button onclick={() => editIndexer(idx)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									Edit
								</button>
								<button onclick={() => deleteIndexer(idx.id)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
									Delete
								</button>
							</div>
						</div>
					{/each}
				</div>
			{:else if !indexerEditing}
				<p class="text-sm text-gray-500">No indexers configured. Add one to enable Usenet search.</p>
			{/if}
		</div>

		<!-- Download Clients Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">Download Clients</h2>
				<button
					onclick={newDlClient}
					class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
				>
					Add Client
				</button>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Configure SABnzbd to download grabbed NZBs.
			</p>

			{#if dlClientMessage}
				<p class="mb-4 text-sm text-red-400">{dlClientMessage}</p>
			{/if}

			{#if dlClientTestResult}
				<div class="mb-4 p-3 rounded-lg {dlClientTestResult.success ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
					<p class="text-sm {dlClientTestResult.success ? 'text-green-400' : 'text-red-400'}">{dlClientTestResult.message}</p>
				</div>
			{/if}

			<!-- Download client edit form -->
			{#if dlClientEditing}
				<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
					<div>
						<label class="block text-xs text-gray-400 mb-1">Name</label>
						<input type="text" bind:value={dlClientEditing.name} placeholder="My SABnzbd"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">URL</label>
						<input type="text" bind:value={dlClientEditing.url} placeholder="http://localhost:8080/sabnzbd"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">API Key</label>
						<input type="password" bind:value={dlClientEditing.api_key} placeholder={dlClientEditing.id ? '(leave blank to keep current)' : 'API key'}
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Category</label>
							<input type="text" bind:value={dlClientEditing.category} placeholder="comics"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Priority</label>
							<input type="number" bind:value={dlClientEditing.priority} min="1" max="100"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
					</div>
					<div class="flex gap-2 pt-2">
						<button onclick={saveDlClient} disabled={dlClientSaving}
							class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
							{dlClientSaving ? 'Saving...' : 'Save'}
						</button>
						<button onclick={() => dlClientEditing = null}
							class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
							Cancel
						</button>
					</div>
				</div>
			{/if}

			<!-- Download client list -->
			{#if dlClients.length > 0}
				<div class="divide-y divide-gray-700">
					{#each dlClients as dc (dc.id)}
						<div class="py-3 flex items-center justify-between gap-4">
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-gray-200">{dc.name}</span>
									<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">{dc.type}</span>
									{#if dc.category}
										<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">cat: {dc.category}</span>
									{/if}
									{#if !dc.enabled}
										<span class="text-xs px-1.5 py-0.5 rounded bg-red-900/50 text-red-400">Disabled</span>
									{/if}
								</div>
								<p class="text-xs text-gray-500 mt-0.5 truncate font-mono">{dc.url}</p>
							</div>
							<div class="flex items-center gap-1">
								<button onclick={() => testDlClient(dc.id)} disabled={dlClientTesting === dc.id}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									{dlClientTesting === dc.id ? '...' : 'Test'}
								</button>
								<button onclick={() => editDlClient(dc)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									Edit
								</button>
								<button onclick={() => deleteDlClient(dc.id)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
									Delete
								</button>
							</div>
						</div>
					{/each}
				</div>
			{:else if !dlClientEditing}
				<p class="text-sm text-gray-500">No download clients configured. Add SABnzbd to enable NZB grabbing.</p>
			{/if}
		</div>

		<!-- Auto Scan Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Automated Library Scan</h2>
			<p class="text-sm text-gray-400 mb-6">
				Automatically scan the library directory for new or changed files on a recurring schedule.
			</p>

			{#if autoScanMessage}
				<p class="mb-4 text-sm {autoScanMessage.includes('updated') || autoScanMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{autoScanMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Automated Scan</p>
						<p class="text-xs text-gray-500 mt-0.5">Scan the library directory for new files on a schedule</p>
					</div>
					<button
						onclick={() => saveAutoScan('enabled', !settings?.auto_scan_enabled)}
						disabled={autoScanSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{settings?.auto_scan_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{settings?.auto_scan_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if settings?.auto_scan_enabled}
					<!-- Interval selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Interval</label>
						<select
							value={settings?.auto_scan_interval ?? 60}
							onchange={(e) => saveAutoScan('interval', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							<option value={15}>Every 15 minutes</option>
							<option value={30}>Every 30 minutes</option>
							<option value={60}>Every hour</option>
							<option value={120}>Every 2 hours</option>
							<option value={360}>Every 6 hours</option>
							<option value={720}>Every 12 hours</option>
							<option value={1440}>Every 24 hours</option>
						</select>
					</div>

					<!-- Last run info -->
					{#if settings?.auto_scan_last_run}
						<div class="flex items-center gap-3 pt-2 border-t border-gray-700">
							<span class="text-sm text-gray-400">Last run:</span>
							<span class="text-sm text-gray-300">{new Date(settings.auto_scan_last_run).toLocaleString()}</span>
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- Scan Reconciliation Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Scan Reconciliation</h2>
			<p class="text-sm text-gray-400 mb-6">
				Controls how a library scan reconciles with what's on disk and with ComicVine.
				Files missing on disk are always pruned from the database. ComicVine is only
				re-fetched per series when its last sync is older than the TTL below.
			</p>

			{#if scanReconcileMessage}
				<p class="mb-4 text-sm {scanReconcileMessage.includes('updated') || scanReconcileMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{scanReconcileMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Auto-queue backlog when new gaps appear</p>
						<p class="text-xs text-gray-500 mt-0.5">After CV refresh during a scan, automatically create a backlog run for any series that has new missing issues.</p>
					</div>
					<button
						onclick={() => saveScanReconcile('auto_queue_backlog', !settings?.scan_auto_queue_backlog)}
						disabled={scanReconcileSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{settings?.scan_auto_queue_backlog ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{settings?.scan_auto_queue_backlog ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				<div class="flex items-center gap-4">
					<label for="scan-cv-ttl" class="text-sm text-gray-300 w-40">CV refresh TTL</label>
					<select
						id="scan-cv-ttl"
						value={settings?.scan_cv_refresh_ttl_hours ?? 24}
						onchange={(e) => saveScanReconcile('cv_refresh_ttl_hours', parseInt((e.target as HTMLSelectElement).value))}
						class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
					>
						<option value={1}>1 hour</option>
						<option value={6}>6 hours</option>
						<option value={12}>12 hours</option>
						<option value={24}>24 hours</option>
						<option value={48}>48 hours</option>
						<option value={168}>1 week</option>
						<option value={720}>30 days</option>
					</select>
					<span class="text-xs text-gray-500">Skip CV re-fetch when a series was synced within this window.</span>
				</div>
			</div>
		</div>

		<!-- Pull List Automation Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Pull List Automation</h2>
			<p class="text-sm text-gray-400 mb-6">
				Automatically search for and download missing issues from tracked series on a weekly schedule.
			</p>

			{#if pullListMessage}
				<p class="mb-4 text-sm {pullListMessage.includes('updated') || pullListMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{pullListMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<!-- Enable toggle -->
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Weekly Search</p>
						<p class="text-xs text-gray-500 mt-0.5">Automatically search indexers for wanted issues</p>
					</div>
					<button
						onclick={() => savePullListSchedule('enabled', !settings?.pull_list_enabled)}
						disabled={pullListSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{settings?.pull_list_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{settings?.pull_list_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if settings?.pull_list_enabled}
					<!-- Day selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Day</label>
						<select
							value={settings?.pull_list_day ?? 3}
							onchange={(e) => savePullListSchedule('day', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							{#each dayNames as name, i}
								<option value={i}>{name}</option>
							{/each}
						</select>
					</div>

					<!-- Hour selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Hour</label>
						<select
							value={settings?.pull_list_hour ?? 6}
							onchange={(e) => savePullListSchedule('hour', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							{#each Array.from({length: 24}, (_, i) => i) as hour}
								<option value={hour}>{hour === 0 ? '12 AM' : hour < 12 ? `${hour} AM` : hour === 12 ? '12 PM' : `${hour - 12} PM`}</option>
							{/each}
						</select>
					</div>

					<!-- Last run info -->
					{#if settings?.pull_list_last_run}
						<div class="flex items-center gap-3 pt-2 border-t border-gray-700">
							<span class="text-sm text-gray-400">Last run:</span>
							<span class="text-sm text-gray-300">{settings.pull_list_last_run}</span>
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- Auto-Search on Add Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Auto-Search</h2>
			<p class="text-sm text-gray-400 mb-6">
				Automatically search indexers and grab NZBs when an issue is added to the want list.
			</p>

			{#if autoSearchMessage}
				<p class="mb-4 text-sm {autoSearchMessage.includes('updated') || autoSearchMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{autoSearchMessage}
				</p>
			{/if}

			<div class="flex items-center justify-between">
				<div>
					<p class="text-sm font-medium text-gray-200">Search on Want List Add</p>
					<p class="text-xs text-gray-500 mt-0.5">When adding an issue to the want list, immediately search and grab</p>
				</div>
				<button
					onclick={() => saveAutoSearch(!settings?.auto_search_on_add)}
					disabled={autoSearchSaving}
					class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
						{settings?.auto_search_on_add ? 'bg-amber-500' : 'bg-gray-600'}"
				>
					<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
						{settings?.auto_search_on_add ? 'translate-x-6' : 'translate-x-1'}"></span>
				</button>
			</div>
		</div>

		<!-- Missing Issue Search Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Missing Issue Search</h2>
			<p class="text-sm text-gray-400 mb-6">
				Periodically search indexers for missing issues in tracked series. Fills gaps (e.g. missing #10 between #8 and #11) as NZBs become available.
			</p>

			{#if missingSearchMessage}
				<p class="mb-4 text-sm {missingSearchMessage.includes('updated') || missingSearchMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{missingSearchMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<!-- Enable toggle -->
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Missing Search</p>
						<p class="text-xs text-gray-500 mt-0.5">Automatically search for wanted issues on a recurring interval</p>
					</div>
					<button
						onclick={() => saveMissingSearch('enabled', !settings?.missing_search_enabled)}
						disabled={missingSearchSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{settings?.missing_search_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{settings?.missing_search_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if settings?.missing_search_enabled}
					<!-- Interval selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Interval</label>
						<select
							value={settings?.missing_search_interval ?? 10}
							onchange={(e) => saveMissingSearch('interval', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							<option value={5}>Every 5 minutes</option>
							<option value={10}>Every 10 minutes</option>
							<option value={15}>Every 15 minutes</option>
							<option value={30}>Every 30 minutes</option>
							<option value={60}>Every hour</option>
						</select>
					</div>

					<!-- Last run info -->
					{#if settings?.missing_search_last_run}
						<div class="flex items-center gap-3 pt-2 border-t border-gray-700">
							<span class="text-sm text-gray-400">Last run:</span>
							<span class="text-sm text-gray-300">{new Date(settings.missing_search_last_run).toLocaleString()}</span>
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- Notifications Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Notifications</h2>
			<p class="text-sm text-gray-400 mb-6">
				Send notifications to a Slack channel when key events occur.
			</p>

			{#if slackMessage}
				<p class="mb-4 text-sm {slackMessage.includes('updated') || slackMessage.includes('saved') || slackMessage.includes('Saved') || slackMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{slackMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<!-- Global enable toggle -->
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Slack Notifications</p>
						<p class="text-xs text-gray-500 mt-0.5">Send event notifications to a Slack channel</p>
					</div>
					<button
						onclick={() => saveSlackSetting('slack_enabled', !slackSettings?.slack_enabled)}
						disabled={slackSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{slackSettings?.slack_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{slackSettings?.slack_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if slackSettings?.slack_enabled}
					<!-- Bot Token -->
					<div class="space-y-2">
						<label class="text-sm font-medium text-gray-300">Bot Token</label>
						<div class="flex gap-2">
							<input
								type="password"
								bind:value={slackTokenInput}
								placeholder={slackSettings?.slack_token_set ? '••••••••••••' : 'xoxb-...'}
								class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 placeholder:text-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
							<button
								onclick={saveSlackToken}
								disabled={slackSaving || !slackTokenInput.trim()}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:text-gray-400 text-gray-900 text-sm font-semibold rounded transition-colors"
							>
								Save
							</button>
						</div>
						{#if slackSettings?.slack_token_set}
							<p class="text-xs text-green-400">Configured</p>
						{/if}
					</div>

					<!-- Channel -->
					<div class="space-y-2">
						<label class="text-sm font-medium text-gray-300">Channel</label>
						<div class="flex gap-2">
							<input
								type="text"
								bind:value={slackChannelInput}
								placeholder="#longbox or C01234567"
								class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 placeholder:text-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
							<button
								onclick={saveSlackChannel}
								disabled={slackSaving}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:text-gray-400 text-gray-900 text-sm font-semibold rounded transition-colors"
							>
								Save
							</button>
						</div>
					</div>

					<!-- Test button + per-event toggles -->
					{#if slackSettings?.slack_token_set && slackSettings?.slack_channel}
						<div class="flex items-center gap-3">
							<button
								onclick={testSlack}
								disabled={slackTesting}
								class="px-4 py-2 bg-gray-600 hover:bg-gray-500 disabled:bg-gray-700 disabled:text-gray-500 text-gray-100 text-sm rounded transition-colors"
							>
								{slackTesting ? 'Sending...' : 'Send Test Message'}
							</button>
							{#if slackTestResult}
								<span class="text-sm {slackTestResult.success ? 'text-green-400' : 'text-red-400'}">
									{slackTestResult.message}
								</span>
							{/if}
						</div>

						<!-- Per-event toggles -->
						<div class="pt-4 border-t border-gray-700 space-y-3">
							<p class="text-sm font-medium text-gray-300">Event Notifications</p>
							{#each slackEventToggles as toggle}
								<div class="flex items-center justify-between">
									<div>
										<p class="text-sm text-gray-200">{toggle.label}</p>
										<p class="text-xs text-gray-500 mt-0.5">{toggle.desc}</p>
									</div>
									<button
										onclick={() => saveSlackSetting(toggle.key, !(slackSettings?.toggles?.[toggle.key] ?? true))}
										disabled={slackSaving}
										class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
											{(slackSettings?.toggles?.[toggle.key] ?? true) ? 'bg-amber-500' : 'bg-gray-600'}"
									>
										<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
											{(slackSettings?.toggles?.[toggle.key] ?? true) ? 'translate-x-6' : 'translate-x-1'}"></span>
									</button>
								</div>
							{/each}
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- User Management Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">User Management</h2>
				{#if auth.user?.is_admin}
					<button
						onclick={newUser}
						class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
					>
						Add User
					</button>
				{/if}
			</div>

			{#if !auth.authEnabled}
				<p class="text-sm text-gray-400 mb-4">
					Authentication is currently disabled. Create an admin account to enable it.
				</p>
				<a
					href="/setup"
					class="inline-block px-4 py-2 bg-amber-500 hover:bg-amber-600 text-gray-900 font-semibold rounded-lg transition-colors text-sm"
				>
					Enable Authentication
				</a>
			{:else if auth.user?.is_admin}
				<p class="text-sm text-gray-400 mb-6">
					Manage user accounts. Only admins can add or remove users.
				</p>

				{#if userMessage}
					<p class="mb-4 text-sm {userMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">{userMessage}</p>
				{/if}

				{#if passwordMessage}
					<p class="mb-4 text-sm {passwordMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">{passwordMessage}</p>
				{/if}

				<!-- New user form -->
				{#if userEditing}
					<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Username</label>
							<input type="text" bind:value={userEditing.username} placeholder="username"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Password</label>
							<input type="password" bind:value={userEditing.password} placeholder="minimum 8 characters"
								autocomplete="new-password"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div class="flex gap-2 pt-2">
							<button onclick={saveUser} disabled={userSaving || !userEditing.username.trim() || !userEditing.password}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
								{userSaving ? 'Creating...' : 'Create User'}
							</button>
							<button onclick={() => userEditing = null}
								class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
								Cancel
							</button>
						</div>
					</div>
				{/if}

				<!-- User list -->
				{#if users.length > 0}
					<div class="divide-y divide-gray-700">
						{#each users as u (u.id)}
							<div class="py-3">
								<div class="flex items-center justify-between gap-4">
									<div class="flex-1 min-w-0">
										<div class="flex items-center gap-2">
											<span class="font-medium text-gray-200">{u.username}</span>
											{#if u.is_admin}
												<span class="text-xs px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-400">Admin</span>
											{/if}
										</div>
									</div>
									<div class="flex items-center gap-1">
										<button onclick={() => startPasswordChange(u.id)}
											class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
											Password
										</button>
										{#if u.id !== auth.user?.id}
											<button onclick={() => deleteUser(u.id)}
												class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
												Delete
											</button>
										{/if}
									</div>
								</div>

								<!-- Inline password change -->
								{#if passwordChanging === u.id}
									<div class="mt-3 flex gap-2">
										<input type="password" bind:value={newPasswordInput} placeholder="New password (min 8 chars)"
											autocomplete="new-password"
											class="flex-1 px-3 py-1.5 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
										<button onclick={savePasswordChange} disabled={!newPasswordInput || newPasswordInput.length < 8}
											class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded text-sm transition-colors">
											Save
										</button>
										<button onclick={() => passwordChanging = null}
											class="px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded text-sm transition-colors">
											Cancel
										</button>
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			{:else}
				<p class="text-sm text-gray-400">Authentication is enabled. Contact an admin to manage users.</p>
			{/if}
		</div>

		<!-- Post-Processing Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Post-Processing</h2>
			<p class="text-sm text-gray-400 mb-4">
				Run a script after each download is imported. The script receives metadata via environment variables:
				<code class="text-amber-400">LONGBOX_FILE_PATH</code>,
				<code class="text-amber-400">LONGBOX_SERIES</code>,
				<code class="text-amber-400">LONGBOX_ISSUE_NUMBER</code>,
				<code class="text-amber-400">LONGBOX_COMICVINE_ID</code>.
			</p>
			<div class="flex gap-2">
				<input
					type="text"
					bind:value={postProcessInput}
					placeholder="/path/to/script.sh"
					class="flex-1 px-4 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-200
						placeholder-gray-500 focus:outline-none focus:border-amber-500 text-sm font-mono"
				/>
				<button
					onclick={savePostProcessScript}
					disabled={postProcessSaving}
					class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
						text-gray-900 font-semibold rounded-lg transition-colors text-sm"
				>
					{postProcessSaving ? 'Saving...' : 'Save'}
				</button>
			</div>
			{#if postProcessMessage}
				<p class="text-sm mt-2 text-green-400">{postProcessMessage}</p>
			{/if}
		</div>

		<!-- Database Backup Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Database Backup</h2>
			<div class="space-y-4">
				<div class="flex items-center gap-4">
					<label class="flex items-center gap-2">
						<input type="checkbox" bind:checked={backupOnStartInput}
							class="w-4 h-4 rounded bg-gray-700 border-gray-600 text-amber-500 focus:ring-amber-500" />
						<span class="text-sm text-gray-300">Backup on startup</span>
					</label>
					<div class="flex items-center gap-2">
						<label class="text-sm text-gray-400">Keep last</label>
						<input type="number" bind:value={backupRetentionInput} min="1" max="50"
							class="w-16 px-2 py-1 bg-gray-700 border border-gray-600 rounded text-gray-200 text-sm
								focus:outline-none focus:border-amber-500" />
						<span class="text-sm text-gray-400">backups</span>
					</div>
					<button
						onclick={saveBackupSettings}
						disabled={backupSettingSaving}
						class="px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-200 text-sm rounded-lg transition-colors"
					>
						{backupSettingSaving ? 'Saving...' : 'Save Settings'}
					</button>
				</div>
				{#if backupSettingMessage}
					<p class="text-sm text-green-400">{backupSettingMessage}</p>
				{/if}
				<div class="flex items-center gap-2">
					<button
						onclick={createBackup}
						disabled={backupCreating}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
							text-gray-900 font-semibold rounded-lg transition-colors text-sm"
					>
						{backupCreating ? 'Creating...' : 'Create Backup Now'}
					</button>
					{#if backupMessage}
						<span class="text-sm text-green-400">{backupMessage}</span>
					{/if}
				</div>
				{#if backups.length > 0}
					<div class="space-y-1">
						{#each backups as backup (backup.name)}
							<div class="flex items-center justify-between p-2 bg-gray-700/50 rounded text-sm">
								<span class="text-gray-300 font-mono">{backup.name}</span>
								<div class="flex items-center gap-3">
									<span class="text-gray-500">{(backup.size / (1024 * 1024)).toFixed(1)} MB</span>
									<a
										href="/api/v1/admin/backups/{encodeURIComponent(backup.name)}/download"
										class="text-amber-400 hover:text-amber-300"
									>
										Download
									</a>
									<button
										onclick={() => deleteBackup(backup.name)}
										class="text-gray-500 hover:text-red-400 transition-colors"
									>
										Delete
									</button>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>

		<!-- OPDS Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">OPDS Server</h2>
			<p class="text-sm text-gray-400 mb-3">
				LongBox includes an OPDS catalog server for mobile reader apps like Panels, Chunky, or any OPDS-compatible reader.
			</p>
			<div class="bg-gray-700/50 rounded-lg p-4">
				<p class="text-sm text-gray-300 mb-2">OPDS Catalog URL:</p>
				<code class="text-amber-400 text-sm font-mono">{typeof window !== 'undefined' ? window.location.origin : ''}/opds/</code>
				<p class="text-xs text-gray-500 mt-2">Add this URL in your OPDS reader app to browse and download comics from your library.</p>
			</div>
		</div>

		<!-- Maintenance Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Maintenance</h2>
			<p class="text-sm text-gray-400 mb-4">
				One-shot operations for healing pre-existing data after feature changes.
			</p>
			<div class="space-y-3">
				<div class="flex items-center justify-between gap-4">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Backfill Read Status</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Promotes any issue currently flagged "Reading" whose recorded progress
							is at or past the last page to "Read". Safe to run repeatedly.
						</p>
					</div>
					<button
						onclick={backfillReadStatus}
						disabled={readBackfillRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{readBackfillRunning ? 'Running…' : 'Run Backfill'}
					</button>
				</div>
				{#if readBackfillMessage}
					<p class="text-sm text-amber-300/90">{readBackfillMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Prune Fulfilled Want List</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Removes any Wanted entry whose corresponding issue has a file in the library.
							Library scans now do this automatically — this button is for one-shot cleanup of
							pre-existing rows.
						</p>
					</div>
					<button
						onclick={pruneWantList}
						disabled={pruneWantListRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{pruneWantListRunning ? 'Running…' : 'Prune Want List'}
					</button>
				</div>
				{#if pruneWantListMessage}
					<p class="text-sm text-amber-300/90">{pruneWantListMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Merge Duplicate Issues</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Finds (series, issue number) pairs with more than one row and merges
							duplicates into a single canonical issue (the one with a ComicVine
							match wins). Reassigns files, downloads, backlog items, and copies
							wants / story-arc memberships before deleting the duplicates.
						</p>
					</div>
					<button
						onclick={dedupeIssues}
						disabled={dedupeIssuesRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{dedupeIssuesRunning ? 'Running…' : 'Merge Duplicates'}
					</button>
				</div>
				{#if dedupeIssuesMessage}
					<p class="text-sm text-amber-300/90">{dedupeIssuesMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Merge Duplicate Series</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Finds series rows that share the same title + year and merges
							them into a single canonical row. Canonical preference: has
							a ComicVine match, then a Metron match, then most files. Issues
							and comic files are consolidated. Fixes the "Wanted page shows
							issues I already own" symptom that happens when a CV match
							creates a fresh series row alongside the filename-parsed one.
						</p>
					</div>
					<button
						onclick={dedupeSeries}
						disabled={dedupeSeriesRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{dedupeSeriesRunning ? 'Running…' : 'Merge Series'}
					</button>
				</div>
				{#if dedupeSeriesMessage}
					<p class="text-sm text-amber-300/90">{dedupeSeriesMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Refresh Series Posters</p>
						<p class="text-xs text-gray-500 mt-0.5">
							For every series, ensures the Library page has a thumbnail.
							Extracts the first comic file's cover when none has been captured,
							and backfills <code class="text-amber-400 bg-gray-900 px-1 rounded">cover_image_url</code>
							from ComicVine / Metron for matched series that are missing it.
							Also drops <code class="text-amber-400 bg-gray-900 px-1 rounded">folder.jpg</code>
							+ <code class="text-amber-400 bg-gray-900 px-1 rounded">cover.jpg</code>
							into each series folder for Plex / Komga / Explorer.
							API-bound — burns CV / Metron quota on first run; subsequent runs are nearly free.
						</p>
					</div>
					<button
						onclick={writeFolderImages}
						disabled={folderImageRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{folderImageRunning ? 'Starting…' : 'Refresh Series Posters'}
					</button>
				</div>
				{#if folderImageMessage}
					<p class="text-sm text-amber-300/90">{folderImageMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Reconcile Backlog</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Marks every non-terminal backlog item completed when the underlying
							issue is already owned (file on disk) or already grabbed
							(completed download history row). Fixes the "failed (no nzb found)"
							pile that builds up because <code class="text-amber-400 bg-gray-900 px-1 rounded">AutoSearchAndGrab</code>
							returns nil for already-owned issues.
						</p>
					</div>
					<button
						onclick={reconcileBacklog}
						disabled={reconcileRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{reconcileRunning ? 'Running…' : 'Reconcile Backlog'}
					</button>
				</div>
				{#if reconcileMessage}
					<p class="text-sm text-amber-300/90">{reconcileMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Adopt Stranded Folders</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Walks each top-level folder under the library and detects
							SAB-style download folders (e.g.
							<code class="text-amber-400 bg-gray-900 px-1 rounded">{`Batman Gotham Knights Gilded City 01 (of 06) (2022) (Digital)`}</code>).
							Parses the folder name for series + issue + year and reassigns
							the comic files inside to the correct series in the DB
							(creating series / issues if needed). Run <em>Reorganize Library</em>
							afterwards to physically move the files into the canonical layout.
						</p>
					</div>
					<button
						onclick={adoptStrandedFolders}
						disabled={adoptRunning}
						class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
					>
						{adoptRunning ? 'Submitting…' : 'Adopt Folders'}
					</button>
				</div>
				{#if adoptMessage}
					<p class="text-sm text-amber-300/90">{adoptMessage}</p>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Reorganize Library</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Walk every comic file and move it into the canonical
							<code class="text-amber-400 bg-gray-900 px-1 rounded">{`{series} ({year})/{series} ({year}) NNN.{format}`}</code>
							layout. Annuals land in
							<code class="text-amber-400 bg-gray-900 px-1 rounded">{`<parent>/Annuals/`}</code>.
							Resets any custom naming template back to this default. Preview first
							to see what would move and where conflicts (two files mapping to the
							same canonical path) exist.
						</p>
					</div>
					<div class="flex flex-col gap-1.5">
						<button
							onclick={previewReorganize}
							disabled={reorgRunning}
							class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
						>
							{reorgRunning ? 'Working…' : 'Preview Reorganize'}
						</button>
						{#if reorgPreview && reorgPreview.moves > 0}
							<button
								onclick={applyReorganize}
								disabled={reorgRunning}
								class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-gray-900 font-semibold rounded-lg transition-colors whitespace-nowrap"
							>
								Reorganize {reorgPreview.moves}
							</button>
						{/if}
					</div>
				</div>
				{#if reorgMessage}
					<p class="text-sm text-amber-300/90">{reorgMessage}</p>
				{/if}
				{#if reorgPreview && reorgPreview.previews && (reorgPreview.moves > 0 || reorgPreview.conflicts > 0)}
					<div class="bg-gray-900/40 rounded p-3 mt-2 max-h-72 overflow-y-auto">
						<p class="text-xs text-gray-400 mb-2">
							{reorgPreview.moves} to move · {reorgPreview.conflicts} conflict{reorgPreview.conflicts === 1 ? '' : 's'} · {reorgPreview.skipped} unchanged
						</p>
						<ul class="text-xs text-gray-300 space-y-1 font-mono">
							{#each reorgPreview.previews.filter(p => p.status === 'move' || p.status === 'conflict') as p}
								<li class="border-l-2 {p.status === 'conflict' ? 'border-red-500/50' : 'border-amber-500/50'} pl-2">
									<div class="text-gray-500">{p.current_path}</div>
									<div class="{p.status === 'conflict' ? 'text-red-300' : 'text-green-300'}">→ {p.new_path}{p.reason ? ` (${p.reason})` : ''}</div>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Trash Orphan Files</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Trashes every <code class="text-amber-400 bg-gray-900 px-1 rounded">comic_files</code>
							row whose <code class="text-amber-400 bg-gray-900 px-1 rounded">issue_id</code> is NULL.
							These are files LongBox can't link to any issue — usually duplicates of canonical
							copies left over after dedupe-issues passes. Reorganize and dedupe-files both
							ignore them, so they keep non-canonical folders alive on disk forever. Files go
							to the OS recycle bin (reversible) and the DB rows are removed. Preview first.
						</p>
					</div>
					<div class="flex flex-col gap-1.5">
						<button
							onclick={previewOrphans}
							disabled={orphanRunning}
							class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
						>
							{orphanRunning ? 'Working…' : 'Preview Orphans'}
						</button>
						{#if orphanPreview && orphanPreview.scanned > 0}
							<button
								onclick={applyTrashOrphans}
								disabled={orphanRunning}
								class="px-3 py-1.5 text-sm bg-red-600/80 hover:bg-red-600 disabled:opacity-50 text-white rounded-lg transition-colors whitespace-nowrap"
							>
								Trash {orphanPreview.scanned}
							</button>
						{/if}
					</div>
				</div>
				{#if orphanMessage}
					<p class="text-sm text-amber-300/90">{orphanMessage}</p>
				{/if}
				{#if orphanPreview && orphanPreview.trashed && orphanPreview.scanned > 0}
					<div class="bg-gray-900/40 rounded p-3 mt-2 max-h-72 overflow-y-auto">
						<p class="text-xs text-gray-400 mb-2">
							{orphanPreview.scanned} orphan file{orphanPreview.scanned === 1 ? '' : 's'} ·
							would reclaim {(orphanPreview.bytes_reclaimed / (1024 * 1024)).toFixed(1)} MB
							{#if orphanPreview.dry_run} (preview){:else} · {orphanPreview.files_trashed} trashed{/if}
						</p>
						<ul class="text-xs text-gray-300 space-y-0.5 font-mono">
							{#each orphanPreview.trashed as p}
								<li class="text-red-300">trash: {p}</li>
							{/each}
						</ul>
					</div>
				{/if}

				<div class="flex items-center justify-between gap-4 pt-3 border-t border-gray-700/50">
					<div class="min-w-0">
						<p class="text-sm font-medium text-gray-200">Trash Duplicate Files on Disk</p>
						<p class="text-xs text-gray-500 mt-0.5">
							Finds groups of <code class="text-amber-400 bg-gray-900 px-1 rounded">comic_files</code>
							rows attached to the same issue (e.g. <code class="text-amber-400 bg-gray-900 px-1 rounded">Wonder Man 001.cbz</code>
							and <code class="text-amber-400 bg-gray-900 px-1 rounded">WonderMan-001.cbz</code> on the share)
							and moves all but a canonical to the OS recycle bin. Canonical preference:
							ComicInfo.xml present, then CBZ format, then largest file. Always preview first.
						</p>
					</div>
					<div class="flex flex-col gap-1.5">
						<button
							onclick={previewFileDedupe}
							disabled={fileDedupeRunning}
							class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed text-gray-100 rounded-lg transition-colors whitespace-nowrap"
						>
							{fileDedupeRunning ? 'Working…' : 'Preview Duplicates'}
						</button>
						{#if fileDedupePreview && fileDedupePreview.dry_run && fileDedupePreview.groups_found > 0}
							<button
								onclick={applyFileDedupe}
								disabled={fileDedupeRunning}
								class="px-3 py-1.5 text-sm bg-red-600/80 hover:bg-red-600 disabled:opacity-50 text-white rounded-lg transition-colors whitespace-nowrap"
							>
								Trash Duplicates
							</button>
						{/if}
					</div>
				</div>
				{#if fileDedupeMessage}
					<p class="text-sm text-amber-300/90">{fileDedupeMessage}</p>
				{/if}
				{#if fileDedupePreview && fileDedupePreview.groups_found > 0}
					<div class="bg-gray-900/40 rounded p-3 mt-2 max-h-72 overflow-y-auto">
						<p class="text-xs text-gray-400 mb-2">
							{fileDedupePreview.groups_found} duplicate group{fileDedupePreview.groups_found === 1 ? '' : 's'} ·
							would reclaim {formatBytes(fileDedupePreview.bytes_reclaimed)}
							{#if fileDedupePreview.dry_run} (preview — nothing trashed yet){:else} · {fileDedupePreview.files_trashed} trashed{/if}
						</p>
						<ul class="text-xs text-gray-300 space-y-2 font-mono">
							{#each fileDedupePreview.decisions ?? [] as d}
								<li class="border-l-2 border-green-500/50 pl-2">
									<div class="text-green-300">keep [{d.kept_reason}]: {d.kept_path}</div>
									{#each d.trashed ?? [] as p}
										<div class="text-red-300">trash: {p}</div>
									{/each}
								</li>
							{/each}
						</ul>
					</div>
				{/if}
			</div>
		</div>

		<!-- Server Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Server</h2>
			{#if shutdownTriggered}
				<p class="text-sm text-amber-400">Server is shutting down...</p>
			{:else if shutdownConfirming}
				<p class="text-sm text-gray-400 mb-4">Are you sure you want to shut down the server? You will lose access to the web UI.</p>
				<div class="flex gap-2">
					<button
						onclick={shutdownServer}
						class="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm font-semibold rounded-lg transition-colors"
					>
						Confirm Shutdown
					</button>
					<button
						onclick={() => shutdownConfirming = false}
						class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 text-sm rounded-lg transition-colors"
					>
						Cancel
					</button>
				</div>
			{:else}
				<p class="text-sm text-gray-400 mb-4">Stop the LongBox server process. You will need to restart it manually.</p>
				<button
					onclick={() => shutdownConfirming = true}
					class="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm font-semibold rounded-lg transition-colors"
				>
					Shutdown Server
				</button>
			{/if}
		</div>

		<!-- About Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">About</h2>
			<div class="space-y-2 text-sm text-gray-400">
				<p><span class="text-gray-300 font-medium">LongBox</span> — Comic Library Manager</p>
				<p>Metadata provided by <a href="https://comicvine.gamespot.com" target="_blank" rel="noopener" class="text-amber-400 hover:text-amber-300">ComicVine</a></p>
			</div>
		</div>
	{/if}
</div>
