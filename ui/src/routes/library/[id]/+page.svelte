<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { ApiClient, type Series, type Issue, type IssueListResponse, type WriteMetadataResponse, type ComicFile, type SeriesFilesResponse, type FileRenameResponse, type BacklogRun } from '$lib/api/client';
	import ComicVineSearch from '$lib/components/ComicVineSearch.svelte';
	import MetronSearch from '$lib/components/MetronSearch.svelte';
	import WantTrackButton from '$lib/components/WantTrackButton.svelte';
	import { proxiedCoverURL } from '$lib/cover';

	let series = $state<Series | null>(null);
	let issues = $state<Issue[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let showSearch = $state(false);
	let showMetronSearch = $state(false);
	let refreshingMetron = $state(false);
	let refreshing = $state(false);
	let toggling = $state(false);
	let writingMetadata = $state(false);
	let writeResult = $state<WriteMetadataResponse | null>(null);

	// File rename state
	let fileMap = $state<Map<number, ComicFile>>(new Map());
	let editingFileId = $state<number | null>(null);
	let editFileName = $state('');
	let renamingFileId = $state<number | null>(null);
	let renameError = $state<string | null>(null);

	let editingIssue = $state<Issue | null>(null);
	let editIssueTitle = $state('');
	let editIssueNumber = $state('');
	let editIssueWriters = $state('');
	let editIssueArtists = $state('');
	let editIssueRename = $state(true);
	let issueEditError = $state<string | null>(null);
	let issueEditNotice = $state<string | null>(null);
	let savingIssueEdit = $state(false);
	let issuePendingDelete = $state<Issue | null>(null);
	let deletingIssue = $state(false);
	let issueDeleteError = $state<string | null>(null);
	let bulkDeleteOpen = $state(false);
	let bulkDeleting = $state(false);
	let bulkDeleteError = $state<string | null>(null);
	let bulkDeleteNotice = $state<string | null>(null);
	let deleteSeriesOpen = $state(false);
	let deletingSeries = $state(false);
	let deleteSeriesError = $state<string | null>(null);
	let clearingWantList = $state(false);
	let queueingBacklog = $state(false);
	let backlogMessage = $state<string | null>(null);
	let actionsMenuOpen = $state(false);
	function closeActionsMenu() { actionsMenuOpen = false; }
	function toggleActionsMenu() { actionsMenuOpen = !actionsMenuOpen; }
	$effect(() => {
		if (!actionsMenuOpen) return;
		const handler = (e: MouseEvent) => {
			const t = e.target as HTMLElement;
			if (!t.closest('[data-actions-menu]')) {
				actionsMenuOpen = false;
			}
		};
		const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') actionsMenuOpen = false; };
		window.addEventListener('click', handler);
		window.addEventListener('keydown', esc);
		return () => {
			window.removeEventListener('click', handler);
			window.removeEventListener('keydown', esc);
		};
	});
	let writingSidecar = $state(false);
	let sidecarMessage = $state<string | null>(null);
	let writingFolderImage = $state(false);
	let folderImageMessage = $state<string | null>(null);
	let seriesId = $derived($page.params.id);

	// Computed stats
	let ownedCount = $derived(issues.filter(i => i.has_file).length);
	let missingCount = $derived(issues.filter(i => !i.has_file).length);
	let readCount = $derived(issues.filter(i => i.read_status === 'read').length);
	let skippedCount = $derived(issues.filter(i => i.skip_status === 'skipped' || i.skip_status === 'ignored').length);

	async function toggleSkipStatus(issue: Issue) {
		const next = issue.skip_status === 'skipped' ? null : 'skipped';
		try {
			await ApiClient.put(`/issues/${issue.id}/skip-status`, { skip_status: next });
			issue.skip_status = next as any;
			issues = [...issues];
		} catch (e) {
			console.error('Failed to update skip status', e);
		}
	}

	async function loadSeriesDetail() {
		loading = true;
		error = null;
		try {
			const [seriesData, issuesData, filesData] = await Promise.all([
				ApiClient.get<Series>(`/series/${seriesId}`),
				ApiClient.get<IssueListResponse>(`/series/${seriesId}/issues`),
				ApiClient.get<SeriesFilesResponse>(`/series/${seriesId}/files`)
			]);
			series = seriesData;
			issues = issuesData.issues || [];

			const map = new Map<number, ComicFile>();
			for (const f of filesData.files || []) {
				if (f.issue_id) {
					map.set(f.issue_id, f);
				}
			}
			fileMap = map;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load series';
		} finally {
			loading = false;
		}
	}

	async function toggleReadStatus(issue: Issue) {
		const nextStatus = issue.read_status === 'read' ? 'unread' : 'read';
		try {
			await ApiClient.put(`/issues/${issue.id}/read-status`, { read_status: nextStatus });
			issue.read_status = nextStatus;
			issues = [...issues];
		} catch (e) {
			console.error('Failed to update read status', e);
		}
	}

	async function refreshMetadata() {
		refreshing = true;
		try {
			await ApiClient.post(`/series/${seriesId}/refresh`);
			await loadSeriesDetail();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Refresh failed';
		} finally {
			refreshing = false;
		}
	}

	async function refreshFromMetron() {
		refreshingMetron = true;
		try {
			await ApiClient.post(`/series/${seriesId}/refresh-metron`);
			await loadSeriesDetail();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Metron refresh failed';
		} finally {
			refreshingMetron = false;
		}
	}

	async function toggleTracked() {
		if (!series) return;
		toggling = true;
		try {
			const endpoint = series.tracked
				? `/series/${seriesId}/untrack`
				: `/series/${seriesId}/track`;
			const result = await ApiClient.put<{ tracked: boolean; series: Series }>(endpoint);
			if (result.series) {
				series = result.series;
			} else {
				series.tracked = result.tracked;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to toggle tracking';
		} finally {
			toggling = false;
		}
	}

	async function writeMetadata() {
		writingMetadata = true;
		writeResult = null;
		try {
			const result = await ApiClient.post<WriteMetadataResponse>(
				`/series/${seriesId}/write-metadata`
			);
			writeResult = result;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to write metadata';
		} finally {
			writingMetadata = false;
		}
	}

	function startRename(file: ComicFile) {
		const ext = file.file_name.substring(file.file_name.lastIndexOf('.'));
		editFileName = file.file_name.substring(0, file.file_name.lastIndexOf('.'));
		editingFileId = file.id;
		renameError = null;
	}

	function cancelRename() {
		editingFileId = null;
		editFileName = '';
		renameError = null;
	}

	async function saveRename(file: ComicFile) {
		const ext = file.file_name.substring(file.file_name.lastIndexOf('.'));
		const newName = editFileName.trim() + ext;

		if (!editFileName.trim()) {
			renameError = 'Name cannot be empty';
			return;
		}

		if (newName === file.file_name) {
			cancelRename();
			return;
		}

		renamingFileId = file.id;
		renameError = null;
		try {
			const updated = await ApiClient.put<FileRenameResponse>(`/files/${file.id}/rename`, {
				file_name: newName
			});
			if (file.issue_id) {
				fileMap.set(file.issue_id, updated);
				fileMap = new Map(fileMap);
			}
			editingFileId = null;
			editFileName = '';
		} catch (e) {
			renameError = e instanceof Error ? e.message : 'Rename failed';
		} finally {
			renamingFileId = null;
		}
	}

	function handleRenameKeydown(e: KeyboardEvent, file: ComicFile) {
		if (e.key === 'Enter') {
			e.preventDefault();
			saveRename(file);
		} else if (e.key === 'Escape') {
			cancelRename();
		}
	}

	function openIssueEdit(issue: Issue) {
		editingIssue = issue;
		editIssueTitle = issue.title || '';
		editIssueNumber = issue.issue_number || '';
		editIssueWriters = issue.writers || '';
		editIssueArtists = issue.artists || '';
		editIssueRename = !!issue.has_file;
		issueEditError = null;
		issueEditNotice = null;
	}

	function cancelIssueEdit() {
		editingIssue = null;
		editIssueTitle = '';
		editIssueNumber = '';
		editIssueWriters = '';
		editIssueArtists = '';
		editIssueRename = true;
		issueEditError = null;
		issueEditNotice = null;
	}

	async function saveIssueEdit() {
		if (!editingIssue) return;
		const num = editIssueNumber.trim();
		if (!num) {
			issueEditError = 'Issue number is required';
			return;
		}
		savingIssueEdit = true;
		issueEditError = null;
		issueEditNotice = null;
		try {
			const resp = await ApiClient.put<{ issue: Issue; renamed_to?: string; rename_warning?: string }>(
				`/issues/${editingIssue.id}`,
				{
					issue_number: num,
					title: editIssueTitle,
					writers: editIssueWriters,
					artists: editIssueArtists,
					rename: editIssueRename
				}
			);
			const updated = resp.issue;
			const idx = issues.findIndex((i) => i.id === updated.id);
			if (idx !== -1) {
				issues[idx] = updated;
				issues = [...issues];
			}
			if (resp.renamed_to) {
				const oldFile = fileMap.get(updated.id);
				if (oldFile) {
					fileMap.set(updated.id, { ...oldFile, file_path: resp.renamed_to, file_name: resp.renamed_to.split(/[\\/]/).pop() || oldFile.file_name });
					fileMap = new Map(fileMap);
				}
			}
			if (resp.rename_warning) {
				issueEditNotice = `Saved, but file was not renamed: ${resp.rename_warning}`;
				return;
			}
			cancelIssueEdit();
		} catch (e) {
			issueEditError = e instanceof Error ? e.message : 'Failed to save';
		} finally {
			savingIssueEdit = false;
		}
	}

	function confirmDeleteIssue(issue: Issue) {
		issuePendingDelete = issue;
		issueDeleteError = null;
	}

	function cancelDeleteIssue() {
		issuePendingDelete = null;
		issueDeleteError = null;
	}

	async function deleteIssueNow() {
		const target = issuePendingDelete;
		if (!target) return;
		deletingIssue = true;
		issueDeleteError = null;
		try {
			await ApiClient.delete(`/issues/${target.id}`);
			issues = issues.filter((i) => i.id !== target.id);
			fileMap.delete(target.id);
			issuePendingDelete = null;
		} catch (e) {
			issueDeleteError = e instanceof Error ? e.message : 'Failed to delete issue';
		} finally {
			deletingIssue = false;
		}
	}

	function openDeleteSeries() {
		deleteSeriesError = null;
		deleteSeriesOpen = true;
	}

	function cancelDeleteSeries() {
		if (deletingSeries) return;
		deleteSeriesOpen = false;
	}

	async function confirmDeleteSeries() {
		if (!series) return;
		deletingSeries = true;
		deleteSeriesError = null;
		try {
			await ApiClient.delete(`/series/${seriesId}`);
			goto('/library');
		} catch (e) {
			deleteSeriesError = e instanceof Error ? e.message : 'Delete series failed';
		} finally {
			deletingSeries = false;
		}
	}

	function openBulkDelete() {
		bulkDeleteError = null;
		bulkDeleteNotice = null;
		bulkDeleteOpen = true;
	}

	function cancelBulkDelete() {
		bulkDeleteOpen = false;
	}

	async function confirmBulkDelete() {
		if (!series) return;
		bulkDeleting = true;
		bulkDeleteError = null;
		bulkDeleteNotice = null;
		try {
			const result = await ApiClient.delete<{ issues_deleted: number; files_trashed: number; errors?: string[] }>(
				`/series/${seriesId}/issues`
			);
			bulkDeleteNotice = `Deleted ${result.issues_deleted} issue${result.issues_deleted === 1 ? '' : 's'} (${result.files_trashed} file${result.files_trashed === 1 ? '' : 's'} trashed).`;
			if (result.errors && result.errors.length > 0) {
				bulkDeleteNotice += ` Error: ${result.errors[0]}`;
				if (result.errors.length > 1) {
					bulkDeleteNotice += ` (+${result.errors.length - 1} more — see server log)`;
				}
			}
			bulkDeleteOpen = false;
			await loadSeriesDetail();
		} catch (e) {
			bulkDeleteError = e instanceof Error ? e.message : 'Bulk delete failed';
		} finally {
			bulkDeleting = false;
		}
	}

	async function clearWantList() {
		if (!series) return;
		if (!confirm('Remove all want list entries for this series?')) return;
		clearingWantList = true;
		try {
			await ApiClient.delete(`/series/${seriesId}/want-list`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to clear want list';
		} finally {
			clearingWantList = false;
		}
	}

	async function queueBacklog() {
		if (!series) return;
		queueingBacklog = true;
		backlogMessage = null;
		try {
			const run = await ApiClient.post<BacklogRun>('/backlog/runs', {
				series_id: Number(seriesId)
			});
			if (run.total_issues === 0) {
				backlogMessage = 'No missing issues to queue.';
			} else {
				backlogMessage = `Queued ${run.total_issues} issue${run.total_issues !== 1 ? 's' : ''}. Opening backlog…`;
				setTimeout(() => goto('/backlog'), 600);
			}
		} catch (e) {
			backlogMessage = e instanceof Error ? e.message : 'Failed to queue backlog';
		} finally {
			queueingBacklog = false;
		}
	}

	async function writeSidecar() {
		if (!series) return;
		writingSidecar = true;
		sidecarMessage = null;
		try {
			const result = await ApiClient.post<{ series_id: number; folder: string; outcome: string }>(
				`/series/${seriesId}/write-longbox-metadata`
			);
			const labels: Record<string, string> = {
				written: 'Sidecars written',
				unchanged: 'Sidecars already up to date',
				no_files: 'No files in this series',
				no_cv_match: 'Series is not matched to ComicVine',
				no_folder: 'Could not determine a safe series folder (files may be split across the library root)',
				failed: 'Write failed — check server log',
			};
			const label = labels[result.outcome] || result.outcome;
			sidecarMessage = result.folder ? `${label} → ${result.folder}` : label;
		} catch (e) {
			sidecarMessage = e instanceof Error ? e.message : 'Sidecar write failed';
		} finally {
			writingSidecar = false;
		}
	}

	async function writeFolderImage() {
		if (!series) return;
		writingFolderImage = true;
		folderImageMessage = null;
		try {
			const result = await ApiClient.post<{ series_id: number; folder: string; outcome: string }>(
				`/series/${seriesId}/write-folder-image`
			);
			const labels: Record<string, string> = {
				written: 'Poster refreshed',
				unchanged: 'Poster already up to date',
				no_files: 'No files on disk (UI-only refresh applied if a provider match exists)',
				no_cover_source: 'No cover image available (match the series first)',
				no_folder: 'Could not determine a safe series folder',
				failed: 'Refresh failed — check server log',
			};
			const label = labels[result.outcome] || result.outcome;
			folderImageMessage = result.folder ? `${label} → ${result.folder}` : label;
		} catch (e) {
			folderImageMessage = e instanceof Error ? e.message : 'Folder image write failed';
		} finally {
			writingFolderImage = false;
		}
	}

	function handleMatched() {
		showSearch = false;
		loadSeriesDetail();
	}

	$effect(() => {
		if (seriesId) {
			loadSeriesDetail();
		}
	});
</script>

{#if loading}
	<div class="flex items-center justify-center py-20">
		<div class="text-gray-400">Loading...</div>
	</div>
{:else if error}
	<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
		<p class="text-red-400">{error}</p>
	</div>
{:else if series}
	<div class="space-y-6">
		<!-- Back link -->
		<a href="/library" class="text-amber-400 hover:text-amber-300 text-sm">&larr; Back to Library</a>

		<!-- Series Header -->
		<div class="flex gap-6">
			{#if series.cover_file_id}
				<div class="flex-shrink-0 w-48">
					<img
						src="/api/v1/covers/file/{series.cover_file_id}"
						alt={series.title}
						class="w-full rounded-lg shadow-lg"
					/>
				</div>
			{:else if series.cover_image_url}
				<div class="flex-shrink-0 w-48">
					<img
						src={proxiedCoverURL(series.cover_image_url)}
						alt={series.title}
						class="w-full rounded-lg shadow-lg"
					/>
				</div>
			{/if}
			<div class="flex-1 min-w-0">
				<div class="flex items-start justify-between gap-4">
					<h1 class="text-3xl font-bold">{series.title}</h1>
					<div class="flex flex-wrap items-center gap-2 flex-shrink-0">
						{#if series.tracked}
							<button
								onclick={toggleTracked}
								disabled={toggling}
								class="px-3 py-1.5 text-sm rounded-lg transition-colors flex items-center gap-1.5
									bg-amber-500/20 text-amber-400 border border-amber-500/50 hover:bg-amber-500/30"
								title="Stop tracking this series"
							>
								<svg class="w-4 h-4" fill="currentColor" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
										d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
								</svg>
								{toggling ? '...' : 'Tracking'}
							</button>
						{:else}
							<WantTrackButton
								variant="full"
								comicvineId={series.comicvine_id}
								metronId={series.metron_id}
								onTracked={() => loadSeriesDetail()}
							/>
						{/if}

						{#if missingCount > 0}
							<button
								onclick={queueBacklog}
								disabled={queueingBacklog}
								class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
								title="Queue every missing issue for automatic search and download"
							>
								{queueingBacklog ? 'Queueing...' : `Queue Missing (${missingCount})`}
							</button>
						{/if}

						<!-- Actions dropdown -->
						<div class="relative" data-actions-menu>
							<button
								type="button"
								onclick={toggleActionsMenu}
								class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg transition-colors flex items-center gap-1.5"
								aria-haspopup="menu"
								aria-expanded={actionsMenuOpen}
							>
								Actions
								<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
								</svg>
							</button>

							{#if actionsMenuOpen}
								<div
									role="menu"
									class="absolute right-0 top-full mt-2 w-60 bg-gray-900 border border-gray-700 rounded-lg shadow-2xl z-30 py-1"
									data-actions-menu
								>
									<!-- Match group -->
									<div class="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-gray-500">Match</div>
									<button
										role="menuitem"
										onclick={() => { closeActionsMenu(); showSearch = true; }}
										class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 flex items-center justify-between"
									>
										<span>{series.comicvine_id ? 'Re-match CV' : 'Match to ComicVine'}</span>
										<span class="text-xs text-amber-400">CV</span>
									</button>
									<button
										role="menuitem"
										onclick={() => { closeActionsMenu(); showMetronSearch = true; }}
										class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 flex items-center justify-between"
									>
										<span>{series.metron_id ? 'Re-match Metron' : 'Match to Metron'}</span>
										<span class="text-xs text-blue-400">Metron</span>
									</button>

									<!-- Refresh group -->
									{#if series.comicvine_id || series.metron_id}
										<div class="border-t border-gray-700/60 mt-1"></div>
										<div class="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-gray-500">Refresh</div>
									{/if}
									{#if series.comicvine_id}
										<button
											role="menuitem"
											onclick={() => { closeActionsMenu(); refreshMetadata(); }}
											disabled={refreshing}
											class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50 flex items-center justify-between"
										>
											<span>{refreshing ? 'Refreshing CV…' : 'Refresh from CV'}</span>
											<span class="text-xs text-amber-400">CV</span>
										</button>
									{/if}
									{#if series.metron_id}
										<button
											role="menuitem"
											onclick={() => { closeActionsMenu(); refreshFromMetron(); }}
											disabled={refreshingMetron}
											class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50 flex items-center justify-between"
										>
											<span>{refreshingMetron ? 'Refreshing Metron…' : 'Refresh from Metron'}</span>
											<span class="text-xs text-blue-400">Metron</span>
										</button>
									{/if}

									<!-- Write group -->
									{#if ownedCount > 0 || series.comicvine_id}
										<div class="border-t border-gray-700/60 mt-1"></div>
										<div class="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-gray-500">Write</div>
									{/if}
									{#if ownedCount > 0}
										<button
											role="menuitem"
											onclick={() => { closeActionsMenu(); writeMetadata(); }}
											disabled={writingMetadata}
											class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50"
											title="Write ComicInfo.xml metadata into archive files"
										>
											{writingMetadata ? 'Writing ComicInfo…' : 'Write ComicInfo.xml'}
										</button>
									{/if}
									{#if series.comicvine_id}
										<button
											role="menuitem"
											onclick={() => { closeActionsMenu(); writeSidecar(); }}
											disabled={writingSidecar}
											class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50"
											title="Write longbox-series.json + longbox-series.txt to the series folder"
										>
											{writingSidecar ? 'Writing sidecar…' : 'Write LongBox Sidecar'}
										</button>
									{/if}
									<button
										role="menuitem"
										onclick={() => { closeActionsMenu(); writeFolderImage(); }}
										disabled={writingFolderImage}
										class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50"
										title="Backfill cover_file_id / cover_image_url and write folder.jpg + cover.jpg"
									>
										{writingFolderImage ? 'Refreshing poster…' : 'Refresh Series Poster'}
									</button>

									<!-- Destructive -->
									<div class="border-t border-gray-700/60 mt-1"></div>
									<button
										role="menuitem"
										onclick={() => { closeActionsMenu(); clearWantList(); }}
										disabled={clearingWantList}
										class="w-full text-left px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-50"
									>
										{clearingWantList ? 'Clearing want list…' : 'Clear Want List'}
									</button>
									{#if issues.length > 0}
										<button
											role="menuitem"
											onclick={() => { closeActionsMenu(); openBulkDelete(); }}
											disabled={bulkDeleting}
											class="w-full text-left px-3 py-2 text-sm text-red-300 hover:bg-red-900/30 disabled:opacity-50"
											title="Trash every file in this series and remove all issue records (series record is preserved)"
										>
											{bulkDeleting ? 'Deleting…' : 'Delete All Issues'}
										</button>
									{/if}
									<button
										role="menuitem"
										onclick={() => { closeActionsMenu(); openDeleteSeries(); }}
										disabled={deletingSeries}
										class="w-full text-left px-3 py-2 text-sm text-red-300 hover:bg-red-900/30 disabled:opacity-50"
										title="Trash every file, delete every issue, and delete the series record itself"
									>
										{deletingSeries ? 'Deleting series…' : 'Delete Series'}
									</button>
								</div>
							{/if}
						</div>
					</div>
				</div>
				{#if backlogMessage}
					<div class="mt-3 text-sm text-amber-300/90">{backlogMessage}</div>
				{/if}
				{#if folderImageMessage}
					<div class="mt-2 text-sm text-amber-300/90 break-all">{folderImageMessage}</div>
				{/if}
				{#if sidecarMessage}
					<div class="mt-2 text-sm text-amber-300/90 break-all">{sidecarMessage}</div>
				{/if}
				<div class="flex flex-wrap items-center gap-3 mt-2 text-sm text-gray-400">
					{#if series.year}
						<span>{series.year}</span>
					{/if}
					{#if series.publisher_name}
						<span>&middot;</span>
						<span>{series.publisher_name}</span>
					{/if}
					<span>&middot;</span>
					<span>{series.issue_count} issue{series.issue_count !== 1 ? 's' : ''}</span>
					<span>&middot;</span>
					<span class="capitalize">{series.status}</span>
					{#if series.comicvine_id}
						<span>&middot;</span>
						<a
							href="https://comicvine.gamespot.com/volume/4050-{series.comicvine_id}"
							target="_blank"
							rel="noopener"
							class="text-amber-400 hover:text-amber-300"
						>
							ComicVine
						</a>
					{/if}
					{#if series.metron_id}
						<span>&middot;</span>
						<a
							href="https://metron.cloud/series/{series.metron_id}/"
							target="_blank"
							rel="noopener"
							class="text-blue-400 hover:text-blue-300"
						>
							Metron
						</a>
					{/if}
				</div>
				{#if writeResult}
					<div class="mt-3 p-3 rounded-lg {writeResult.failed > 0 ? 'bg-amber-900/30 border border-amber-700' : 'bg-green-900/30 border border-green-700'}">
						<p class="text-sm {writeResult.failed > 0 ? 'text-amber-400' : 'text-green-400'}">
							ComicInfo.xml written: {writeResult.succeeded} succeeded{writeResult.failed > 0 ? `, ${writeResult.failed} failed` : ''}{writeResult.skipped > 0 ? `, ${writeResult.skipped} skipped (non-CBZ)` : ''}
						</p>
					</div>
				{/if}

				{#if series.description}
					<p class="text-gray-300 mt-4 text-sm leading-relaxed line-clamp-4">{series.description}</p>
				{/if}

				<!-- Collection stats -->
				{#if issues.length > 0}
					<div class="flex gap-4 mt-4">
						<div class="text-center px-3 py-2 bg-gray-800 rounded-lg border border-gray-700">
							<p class="text-lg font-bold text-amber-400">{ownedCount}</p>
							<p class="text-xs text-gray-400">Owned</p>
						</div>
						<div class="text-center px-3 py-2 bg-gray-800 rounded-lg border border-gray-700">
							<p class="text-lg font-bold text-red-400">{missingCount}</p>
							<p class="text-xs text-gray-400">Missing</p>
						</div>
						<div class="text-center px-3 py-2 bg-gray-800 rounded-lg border border-gray-700">
							<p class="text-lg font-bold text-green-400">{readCount}</p>
							<p class="text-xs text-gray-400">Read</p>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<!-- Annual Series -->
		{#if series.annual_series && series.annual_series.length > 0}
			<div>
				<h2 class="text-lg font-semibold mb-3">Annual Series</h2>
				<div class="flex flex-wrap gap-2">
					{#each series.annual_series as annual (annual.id)}
						<a href="/library/{annual.id}" class="px-3 py-1.5 bg-gray-800 border border-gray-700 rounded-lg text-sm text-amber-400 hover:border-amber-500/50 transition-colors">
							{annual.title}{#if annual.year} ({annual.year}){/if}
						</a>
					{/each}
				</div>
			</div>
		{/if}

		<!-- Issue List -->
		<div>
			<h2 class="text-xl font-semibold mb-4">Issues ({issues.length})</h2>
			{#if issues.length > 0}
				<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
					{#each issues as issue (issue.id)}
						{@const file = issue.file_id ? fileMap.get(issue.id) : undefined}
						<div class="relative group rounded-lg overflow-hidden bg-gray-800 shadow-lg
							{issue.has_file ? '' : 'opacity-60'}">
							<div class="aspect-[2/3] bg-gray-700 relative">
								{#if issue.file_id}
									<a href="/reader/{issue.id}" class="block w-full h-full">
										<img
											src="/api/v1/covers/file/{issue.file_id}"
											alt="#{issue.issue_number}"
											class="w-full h-full object-cover"
											loading="lazy"
											onerror={(e) => {
												const img = e.currentTarget as HTMLImageElement;
												const fallback = proxiedCoverURL(issue.cover_url);
												if (fallback && img.src.indexOf('/covers/proxy') === -1) img.src = fallback;
											}}
										/>
										<div class="absolute inset-0 bg-black/60 opacity-0 group-hover:opacity-100
											transition-opacity flex items-center justify-center">
											<span class="px-4 py-2 bg-amber-500 text-gray-900 font-semibold rounded-lg text-sm">
												Read
											</span>
										</div>
									</a>
								{:else if issue.cover_url}
									<img
										src={proxiedCoverURL(issue.cover_url)}
										alt="#{issue.issue_number}"
										class="w-full h-full object-cover"
										loading="lazy"
									/>
								{:else}
									<div class="w-full h-full flex flex-col items-center justify-center text-gray-500 text-sm gap-1">
										<svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
												d="M12 9v3m0 0v3m0-3h3m-3 0H9m12 0a9 9 0 11-18 0 9 9 0 0118 0z" />
										</svg>
										<span class="text-xs">Missing</span>
									</div>
								{/if}
							</div>
							<div class="p-3">
								<div class="flex items-center justify-between">
									<p class="text-sm font-medium text-gray-200">
										#{issue.issue_number}
									</p>
									{#if issue.has_file}
										<div class="flex items-center gap-1">
											<span
												class="text-xs px-2 py-0.5 rounded-full
													{issue.read_status === 'read'
														? 'bg-green-900/50 text-green-400'
														: issue.read_status === 'reading'
														? 'bg-amber-900/50 text-amber-400'
														: 'bg-gray-700 text-gray-400'}"
											>
												{issue.read_status === 'read' ? 'Read' : issue.read_status === 'reading' ? 'Reading' : 'Unread'}
											</span>
											<button
												onclick={() => toggleReadStatus(issue)}
												class="text-xs px-2 py-0.5 rounded text-gray-300 hover:text-white hover:bg-gray-700 transition-colors"
												title={issue.read_status === 'read' ? 'Mark as Unread' : 'Mark as Read'}
											>
												{issue.read_status === 'read' ? 'Mark Unread' : 'Mark Read'}
											</button>
										</div>
									{/if}
								</div>
								{#if issue.skip_status}
									<button
										onclick={() => toggleSkipStatus(issue)}
										class="text-xs px-2 py-0.5 rounded-full ml-1
											bg-gray-700 text-gray-500 hover:bg-gray-600"
										title="Click to un-skip"
									>
										{issue.skip_status === 'ignored' ? 'Ignored' : 'Skipped'}
									</button>
								{:else if !issue.has_file}
									<button
										onclick={() => toggleSkipStatus(issue)}
										class="text-xs px-1 py-0.5 rounded text-gray-600 hover:text-gray-400 ml-1"
										title="Skip this issue"
									>
										Skip
									</button>
								{/if}
								{#if issue.title}
									<p class="text-xs text-gray-400 mt-1 truncate" title={issue.title}>
										{issue.title}
									</p>
								{/if}
								{#if issue.writers}
									<p class="text-xs text-gray-500 mt-1 truncate">{issue.writers}</p>
								{/if}
								{#if issue.cover_date}
									<p class="text-xs text-gray-500 mt-1">{issue.cover_date}</p>
								{/if}
								<!-- Filename + inline rename -->
								{#if file}
									<div class="mt-1.5">
										{#if editingFileId === file.id}
											<div class="flex flex-col gap-1">
												<div class="flex items-center gap-0.5">
													<input
														type="text"
														bind:value={editFileName}
														onkeydown={(e) => handleRenameKeydown(e, file)}
														class="flex-1 min-w-0 text-xs px-1.5 py-0.5 bg-gray-700 border border-gray-600 rounded text-gray-200 focus:outline-none focus:border-amber-500"
														disabled={renamingFileId === file.id}
													/>
													<span class="text-xs text-gray-500 flex-shrink-0">{file.file_name.substring(file.file_name.lastIndexOf('.'))}</span>
												</div>
												{#if renameError}
													<p class="text-xs text-red-400">{renameError}</p>
												{/if}
												<div class="flex gap-1">
													<button
														onclick={() => saveRename(file)}
														disabled={renamingFileId === file.id}
														class="text-xs px-1.5 py-0.5 bg-amber-500 hover:bg-amber-600 text-gray-900 rounded disabled:opacity-50"
													>
														{renamingFileId === file.id ? '...' : 'Save'}
													</button>
													<button
														onclick={cancelRename}
														disabled={renamingFileId === file.id}
														class="text-xs px-1.5 py-0.5 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded disabled:opacity-50"
													>
														Cancel
													</button>
												</div>
											</div>
										{:else}
											<div class="flex items-center gap-1 group/file">
												<p class="text-xs text-gray-500 truncate flex-1" title={file.file_name}>
													{file.file_name}
												</p>
												<button
													onclick={() => startRename(file)}
													class="flex-shrink-0 opacity-0 group-hover/file:opacity-100 transition-opacity text-gray-500 hover:text-amber-400"
													title="Rename file"
												>
													<svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
														<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
															d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
													</svg>
												</button>
											</div>
										{/if}
								<div class="flex items-center justify-end gap-2 mt-2 text-xs">
									<button
										onclick={() => openIssueEdit(issue)}
										class="px-2 py-0.5 rounded bg-gray-700 hover:bg-gray-600 text-gray-300 transition-colors"
									>
										Edit
									</button>
									<button
										onclick={() => confirmDeleteIssue(issue)}
										class="px-2 py-0.5 rounded bg-red-900/40 hover:bg-red-900/60 text-red-300 transition-colors"
									>
										Delete
									</button>
								</div>
									</div>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			{:else}
				<p class="text-gray-400">No issues found for this series.</p>
			{/if}
		</div>
	</div>

	{#if editingIssue}
		<div class="fixed inset-0 bg-black/70 flex items-center justify-center z-50 px-4"
			onclick={(e) => { if (e.target === e.currentTarget) cancelIssueEdit(); }}
			onkeydown={(e) => { if (e.key === 'Escape') cancelIssueEdit(); }}
			tabindex="-1" role="dialog" aria-modal="true">
			<div class="bg-gray-900 border border-gray-700 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4">
				<h3 class="text-lg font-semibold">Edit Issue #{editingIssue.issue_number}</h3>
				<div class="space-y-2">
					<label class="text-sm text-gray-400" for="edit-issue-number">Issue Number</label>
					<input
						id="edit-issue-number"
						type="text"
						bind:value={editIssueNumber}
						class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded text-gray-100 focus:outline-none focus:border-amber-500"
						disabled={savingIssueEdit}
					/>
				</div>
				<div class="space-y-2">
					<label class="text-sm text-gray-400" for="edit-issue-title">Title</label>
					<input
						id="edit-issue-title"
						type="text"
						bind:value={editIssueTitle}
						class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded text-gray-100 focus:outline-none focus:border-amber-500"
						disabled={savingIssueEdit}
					/>
				</div>
				<div class="space-y-2">
					<label class="text-sm text-gray-400" for="edit-issue-writers">Writers</label>
					<input
						id="edit-issue-writers"
						type="text"
						bind:value={editIssueWriters}
						placeholder="Comma-separated"
						class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded text-gray-100 focus:outline-none focus:border-amber-500"
						disabled={savingIssueEdit}
					/>
				</div>
				<div class="space-y-2">
					<label class="text-sm text-gray-400" for="edit-issue-artists">Artists</label>
					<input
						id="edit-issue-artists"
						type="text"
						bind:value={editIssueArtists}
						placeholder="Comma-separated"
						class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded text-gray-100 focus:outline-none focus:border-amber-500"
						disabled={savingIssueEdit}
					/>
				</div>
				{#if editingIssue.has_file}
					<label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
						<input
							type="checkbox"
							bind:checked={editIssueRename}
							class="w-4 h-4 accent-amber-500"
							disabled={savingIssueEdit}
						/>
						Rename file on disk to match the new metadata
					</label>
				{/if}
				{#if issueEditError}
					<p class="text-sm text-red-400">{issueEditError}</p>
				{/if}
				{#if issueEditNotice}
					<p class="text-sm text-amber-300">{issueEditNotice}</p>
				{/if}
				<div class="flex justify-end gap-2">
					<button
						onclick={cancelIssueEdit}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 rounded-lg"
						disabled={savingIssueEdit}
					>Cancel</button>
					<button
						onclick={saveIssueEdit}
						class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 text-gray-900 rounded-lg disabled:opacity-50"
						disabled={savingIssueEdit}
					>{savingIssueEdit ? 'Saving...' : 'Save'}</button>
				</div>
			</div>
		</div>
	{/if}

	{#if issuePendingDelete}
		<div class="fixed inset-0 bg-black/70 flex items-center justify-center z-50 px-4"
			onclick={(e) => { if (e.target === e.currentTarget) cancelDeleteIssue(); }}
			onkeydown={(e) => { if (e.key === 'Escape') cancelDeleteIssue(); }}
			tabindex="-1" role="dialog" aria-modal="true">
			<div class="bg-gray-900 border border-gray-700 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4">
				<h3 class="text-lg font-semibold text-red-300">Delete Issue #{issuePendingDelete.issue_number}?</h3>
				<p class="text-sm text-gray-400">This will move the file to the recycle bin and remove the issue from the library.</p>
				{#if issueDeleteError}
					<p class="text-sm text-red-400">{issueDeleteError}</p>
				{/if}
				<div class="flex justify-end gap-2">
					<button
						onclick={cancelDeleteIssue}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 rounded-lg"
						disabled={deletingIssue}
					>Cancel</button>
					<button
						onclick={deleteIssueNow}
						class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-500 text-white rounded-lg disabled:opacity-50"
						disabled={deletingIssue}
					>{deletingIssue ? 'Deleting...' : 'Delete'}</button>
				</div>
			</div>
		</div>
	{/if}

	{#if deleteSeriesOpen}
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="fixed inset-0 bg-black/70 flex items-center justify-center z-50 px-4"
			onclick={(e) => { if (e.target === e.currentTarget) cancelDeleteSeries(); }}
			onkeydown={(e) => { if (e.key === 'Escape') cancelDeleteSeries(); }}
			tabindex="-1" role="dialog" aria-modal="true">
			<div class="bg-gray-900 border border-red-700 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4">
				<h3 class="text-lg font-semibold text-red-300">Delete "{series.title}"?</h3>
				<p class="text-sm text-gray-300">
					Every file in this series will be moved to the OS recycle bin. All
					issue and file records, plus the series record itself, will be removed
					from the database.
					{#if issues.length > 0}
						<span class="block mt-2 text-amber-300/90">
							{issues.length} issue{issues.length === 1 ? '' : 's'} affected.
						</span>
					{/if}
				</p>
				<p class="text-xs text-gray-500">
					Files are reversible — restore from the Recycle Bin and re-scan to
					re-import. Database deletion is permanent.
				</p>
				{#if deleteSeriesError}
					<p class="text-sm text-red-400">{deleteSeriesError}</p>
				{/if}
				<div class="flex justify-end gap-2">
					<button
						onclick={cancelDeleteSeries}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 rounded-lg"
						disabled={deletingSeries}
					>Cancel</button>
					<button
						onclick={confirmDeleteSeries}
						class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-500 text-white rounded-lg disabled:opacity-50"
						disabled={deletingSeries}
					>{deletingSeries ? 'Deleting…' : 'Delete Series'}</button>
				</div>
			</div>
		</div>
	{/if}

	{#if bulkDeleteOpen}
		<div class="fixed inset-0 bg-black/70 flex items-center justify-center z-50 px-4"
			onclick={(e) => { if (e.target === e.currentTarget) cancelBulkDelete(); }}
			onkeydown={(e) => { if (e.key === 'Escape') cancelBulkDelete(); }}
			tabindex="-1" role="dialog" aria-modal="true">
			<div class="bg-gray-900 border border-red-700 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4">
				<h3 class="text-lg font-semibold text-red-300">Delete all {issues.length} issues in {series.title}?</h3>
				<p class="text-sm text-gray-300">
					Every file will be moved to the OS recycle bin. Issue and file records
					will be removed from the database. The series record stays.
				</p>
				<p class="text-sm text-gray-500">
					This is reversible at the file level — you can restore from the recycle bin
					— but the database deletion is permanent (re-scan to re-import).
				</p>
				{#if bulkDeleteError}
					<p class="text-sm text-red-400">{bulkDeleteError}</p>
				{/if}
				<div class="flex justify-end gap-2">
					<button
						onclick={cancelBulkDelete}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 rounded-lg"
						disabled={bulkDeleting}
					>Cancel</button>
					<button
						onclick={confirmBulkDelete}
						class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-500 text-white rounded-lg disabled:opacity-50"
						disabled={bulkDeleting}
					>{bulkDeleting ? 'Deleting…' : `Delete ${issues.length} issues`}</button>
				</div>
			</div>
		</div>
	{/if}

	{#if bulkDeleteNotice}
		<div class="fixed bottom-4 right-4 bg-gray-800 border border-amber-500/40 rounded-lg shadow-xl px-4 py-3 max-w-sm z-40">
			<p class="text-sm text-amber-200">{bulkDeleteNotice}</p>
			<button onclick={() => (bulkDeleteNotice = null)} class="text-xs text-gray-400 hover:text-gray-200 mt-1">dismiss</button>
		</div>
	{/if}

	<!-- ComicVine Search Modal -->
	{#if showSearch}
		<ComicVineSearch
			seriesTitle={series.title}
			seriesId={Number(seriesId)}
			onMatched={handleMatched}
			onClose={() => showSearch = false}
		/>
	{/if}

	<!-- Metron Search Modal -->
	{#if showMetronSearch}
		<MetronSearch
			seriesTitle={series.title}
			seriesId={Number(seriesId)}
			onMatched={() => { showMetronSearch = false; loadSeriesDetail(); }}
			onClose={() => showMetronSearch = false}
		/>
	{/if}
{/if}
