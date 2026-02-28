<script lang="ts">
	import { page } from '$app/stores';
	import { ApiClient, type Series, type Issue, type IssueListResponse, type WriteMetadataResponse, type ComicFile, type SeriesFilesResponse, type FileRenameResponse } from '$lib/api/client';
	import ComicVineSearch from '$lib/components/ComicVineSearch.svelte';

	let series = $state<Series | null>(null);
	let issues = $state<Issue[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let showSearch = $state(false);
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

	let seriesId = $derived($page.params.id);

	// Computed stats
	let ownedCount = $derived(issues.filter(i => i.has_file).length);
	let missingCount = $derived(issues.filter(i => !i.has_file).length);
	let readCount = $derived(issues.filter(i => i.read_status === 'read').length);

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
			{/if}
			<div class="flex-1 min-w-0">
				<div class="flex items-start justify-between gap-4">
					<h1 class="text-3xl font-bold">{series.title}</h1>
					<div class="flex gap-2 flex-shrink-0">
						<button
							onclick={toggleTracked}
							disabled={toggling}
							class="px-3 py-1.5 text-sm rounded-lg transition-colors flex items-center gap-1.5
								{series.tracked
									? 'bg-amber-500/20 text-amber-400 border border-amber-500/50 hover:bg-amber-500/30'
									: 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
							title={series.tracked ? 'Stop tracking this series' : 'Track this series'}
						>
							<svg class="w-4 h-4" fill={series.tracked ? 'currentColor' : 'none'} stroke="currentColor" viewBox="0 0 24 24">
								<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
									d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
							</svg>
							{toggling ? '...' : series.tracked ? 'Tracking' : 'Track'}
						</button>
						{#if series.comicvine_id}
							<button
								onclick={refreshMetadata}
								disabled={refreshing}
								class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600
									disabled:bg-gray-600 disabled:cursor-not-allowed
									text-gray-200 rounded-lg transition-colors"
								title="Refresh metadata from ComicVine"
							>
								{refreshing ? 'Refreshing...' : 'Refresh'}
							</button>
						{/if}
						<button
							onclick={() => showSearch = true}
							class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600
								text-gray-900 font-semibold rounded-lg transition-colors"
						>
							{series.comicvine_id ? 'Re-match' : 'Match to ComicVine'}
						</button>
						{#if ownedCount > 0}
							<button
								onclick={writeMetadata}
								disabled={writingMetadata}
								class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600
									disabled:bg-gray-600 disabled:cursor-not-allowed
									text-gray-200 rounded-lg transition-colors flex items-center gap-1.5"
								title="Write ComicInfo.xml metadata into CBZ files"
							>
								<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
										d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
								</svg>
								{writingMetadata ? 'Writing...' : 'Write Metadata'}
							</button>
						{/if}
					</div>
				</div>
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
										/>
										<div class="absolute inset-0 bg-black/60 opacity-0 group-hover:opacity-100
											transition-opacity flex items-center justify-center">
											<span class="px-4 py-2 bg-amber-500 text-gray-900 font-semibold rounded-lg text-sm">
												Read
											</span>
										</div>
									</a>
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
										<button
											onclick={() => toggleReadStatus(issue)}
											class="text-xs px-2 py-0.5 rounded-full transition-colors
												{issue.read_status === 'read'
													? 'bg-green-900/50 text-green-400 hover:bg-green-900/70'
													: issue.read_status === 'reading'
													? 'bg-amber-900/50 text-amber-400 hover:bg-amber-900/70'
													: 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
											title="Click to toggle read status"
										>
											{issue.read_status === 'read' ? 'Read' : issue.read_status === 'reading' ? 'Reading' : 'Unread'}
										</button>
									{/if}
								</div>
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

	<!-- ComicVine Search Modal -->
	{#if showSearch}
		<ComicVineSearch
			seriesTitle={series.title}
			seriesId={Number(seriesId)}
			onMatched={handleMatched}
			onClose={() => showSearch = false}
		/>
	{/if}
{/if}
