<script lang="ts">
	import { ApiClient, type ComicFile, type FileListResponse, type FileRenameResponse, type DuplicatesResponse, type DuplicateGroup } from '$lib/api/client';

	import FileFolderCard, { type FileFolderGroup } from '$lib/components/FileFolderCard.svelte';

	let files = $state<ComicFile[]>([]);
	let total = $state(0);
	let currentPage = $state(1);
	let perPage = $state(50);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let searchInput = $state('');
	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | null = null;

	// Tab state
	let activeTab = $state<'files' | 'duplicates'>('files');

	// Duplicates state
	let dupesByHash = $state<DuplicateGroup[]>([]);
	let dupesByIssue = $state<DuplicateGroup[]>([]);
	let dupesLoading = $state(false);
	let dupesError = $state<string | null>(null);
	let backfilling = $state(false);
	let backfillMessage = $state<string | null>(null);

	// Inline rename state
	let editingFileId = $state<number | null>(null);
	let editFileName = $state('');
	let renamingFileId = $state<number | null>(null);
	let renameError = $state<string | null>(null);

	let fileViewMode = $state<'folders' | 'table'>('folders');
	let folderFiles = $state<ComicFile[]>([]);
	let folderLoading = $state(false);
	let folderLoaded = $state(false);
	let folderSearchInput = $state('');
	let folderSearch = $state('');
	let folderError = $state<string | null>(null);
	let selectedFolder = $state<FileFolderGroup | null>(null);

	let totalPages = $derived(Math.max(1, Math.ceil(total / perPage)));

	async function loadFiles() {
		loading = true;
		error = null;
		try {
			let url = `/files?page=${currentPage}&per_page=${perPage}`;
			if (searchQuery) {
				url += `&search=${encodeURIComponent(searchQuery)}`;
			}
			const data = await ApiClient.get<FileListResponse>(url);
			files = data.files || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load files';
		} finally {
			loading = false;
		}
	}

	function handleSearchInput() {
		if (searchTimeout) clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => {
			searchQuery = searchInput;
			currentPage = 1;
		}, 300);
	}

	function goToPage(p: number) {
		if (p >= 1 && p <= totalPages) {
			currentPage = p;
		}
	}

	function startRename(file: ComicFile) {
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
			const idx = files.findIndex(f => f.id === file.id);
			if (idx !== -1) {
				files[idx] = updated;
				files = [...files];
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

	async function loadDuplicates() {
		dupesLoading = true;
		dupesError = null;
		try {
			const data = await ApiClient.get<DuplicatesResponse>('/files/duplicates');
			dupesByHash = data.by_hash || [];
			dupesByIssue = data.by_issue || [];
		} catch (e) {
			dupesError = e instanceof Error ? e.message : 'Failed to load duplicates';
		} finally {
			dupesLoading = false;
		}
	}

	async function deleteFile(id: number, deleteDisk: boolean) {
		if (!confirm(deleteDisk ? 'Delete this file from disk and database?' : 'Remove from database only?')) return;
		try {
			await ApiClient.delete(`/files/${id}?delete_disk=${deleteDisk}`);
			dupesByHash = dupesByHash.map(g => ({ ...g, files: g.files.filter(f => f.id !== id) })).filter(g => g.files.length > 1);
			dupesByIssue = dupesByIssue.map(g => ({ ...g, files: g.files.filter(f => f.id !== id) })).filter(g => g.files.length > 1);
		} catch (e) {
			dupesError = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function backfillHashes() {
		backfilling = true;
		backfillMessage = null;
		try {
			const data = await ApiClient.post<{ job_id: number; message: string }>('/files/backfill-hashes');
			backfillMessage = data.message || `Job #${data.job_id} started`;
		} catch (e) {
			backfillMessage = e instanceof Error ? e.message : 'Failed to start';
		} finally {
			backfilling = false;
		}
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
		return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
	}

	function describeFolder(file: ComicFile) {
		const normalizedPath = (file.file_path || '').replace(/\\/g, '/');
		const normalizedName = (file.file_name || '').replace(/\\/g, '/');
		let folderPath = normalizedPath;
		if (normalizedName && normalizedPath.toLowerCase().endsWith(normalizedName.toLowerCase())) {
			folderPath = normalizedPath.slice(0, normalizedPath.length - normalizedName.length).replace(/\/+$/, '');
		}
		if (!folderPath) {
			folderPath = '(root)';
		}
		const segments = folderPath.split('/').filter(Boolean);
		const label = segments.length ? segments[segments.length - 1] : folderPath;
		const id = folderPath.toLowerCase() || '(root)';
		return { id, path: folderPath, label };
	}

	let folderGroups = $derived(() => {
		const map = new Map<string, FileFolderGroup>();
		for (const file of folderFiles) {
			const { id, path, label } = describeFolder(file);
			let group = map.get(id);
			if (!group) {
				group = { id, path, label, files: [], totalSize: 0, formatCounts: {} };
				map.set(id, group);
			}
			group.files.push(file);
			group.totalSize += file.file_size || 0;
			const fmt = (file.file_format || 'cbz').toLowerCase();
			group.formatCounts[fmt] = (group.formatCounts[fmt] || 0) + 1;
		}
		for (const group of map.values()) {
			group.files.sort((a, b) => a.file_name.localeCompare(b.file_name));
		}
		let groups = [...map.values()];
		if (folderSearch) {
			const term = folderSearch;
			groups = groups.filter(group => group.label.toLowerCase().includes(term) || group.path.toLowerCase().includes(term));
		}
		return groups.sort((a, b) => a.label.localeCompare(b.label));
	});

	async function loadFolderFiles() {
		if (folderLoading || folderLoaded) return;
		folderLoading = true;
		folderError = null;
		try {
			const data = await ApiClient.get<FileListResponse>(`/files?page=1&per_page=10000`);
			folderFiles = data.files || [];
			folderLoaded = true;
		} catch (e) {
			folderError = e instanceof Error ? e.message : 'Failed to load files';
		} finally {
			folderLoading = false;
		}
	}

	function switchFileView(mode: 'folders' | 'table') {
		fileViewMode = mode;
		if (mode === 'folders') {
			loadFolderFiles();
		}
	}

	$effect(() => {
		folderSearch = folderSearchInput.trim().toLowerCase();
	});

	$effect(() => {
		if (activeTab === 'files' && fileViewMode === 'folders') {
			loadFolderFiles();
		}
	});

	$effect(() => {
		if (activeTab === 'files') {
			currentPage;
			searchQuery;
			loadFiles();
		} else {
			loadDuplicates();
		}
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-2xl font-bold">Files</h1>
			<p class="text-sm text-gray-400 mt-1">
				{#if activeTab === 'duplicates'}
					Duplicate audits by hash or issue
				{:else if fileViewMode === 'folders'}
					{folderGroups.length} folder{folderGroups.length === 1 ? '' : 's'} ·
					{#if folderLoaded}
						{folderFiles.length} file{folderFiles.length === 1 ? '' : 's'}
					{:else}
						Loading…
					{/if}
				{:else}
					{total} file{total !== 1 ? 's' : ''}
				{/if}
			</p>
		</div>
	</div>

	<!-- Tabs -->
	<div class="flex gap-1 border-b border-gray-700">
		<button
			onclick={() => activeTab = 'files'}
			class="px-4 py-2 text-sm font-medium transition-colors border-b-2
				{activeTab === 'files' ? 'border-amber-500 text-amber-400' : 'border-transparent text-gray-400 hover:text-gray-300'}"
		>
			All Files
		</button>
		<button
			onclick={() => activeTab = 'duplicates'}
			class="px-4 py-2 text-sm font-medium transition-colors border-b-2
				{activeTab === 'duplicates' ? 'border-amber-500 text-amber-400' : 'border-transparent text-gray-400 hover:text-gray-300'}"
		>
			Duplicates
		</button>
	</div>

{#if activeTab === 'duplicates'}
	<!-- Duplicates View -->
	<div class="space-y-4">
		<div class="flex items-center gap-3">
			<button
				onclick={backfillHashes}
				disabled={backfilling}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					text-gray-900 font-semibold rounded-lg transition-colors text-sm"
			>
				{backfilling ? 'Starting...' : 'Backfill File Hashes'}
			</button>
			{#if backfillMessage}
				<span class="text-sm text-green-400">{backfillMessage}</span>
			{/if}
		</div>

		{#if dupesError}
			<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
				<p class="text-red-400">{dupesError}</p>
			</div>
		{/if}

		{#if dupesLoading}
			<div class="text-gray-400 py-8 text-center">Loading...</div>
		{:else if dupesByHash.length === 0 && dupesByIssue.length === 0}
			<div class="text-gray-400 py-8 text-center">No duplicates found.</div>
		{:else}
			{#if dupesByHash.length > 0}
				<h3 class="text-lg font-semibold">By File Hash ({dupesByHash.length} group{dupesByHash.length !== 1 ? 's' : ''})</h3>
				{#each dupesByHash as group (group.key)}
					<div class="bg-gray-800 rounded-lg border border-gray-700 p-4 space-y-2">
						<p class="text-xs text-gray-500 font-mono">Hash: {group.key}</p>
						{#each group.files as file (file.id)}
							<div class="flex items-center justify-between p-2 bg-gray-700/50 rounded">
								<span class="text-sm text-gray-300 truncate flex-1" title={file.file_path}>{file.file_name}</span>
								<div class="flex items-center gap-3 flex-shrink-0 ml-4">
									<span class="text-xs text-gray-500">{formatSize(file.file_size)}</span>
									<button onclick={() => deleteFile(file.id, true)} class="text-xs text-red-400 hover:text-red-300">Delete</button>
								</div>
							</div>
						{/each}
					</div>
				{/each}
			{/if}
			{#if dupesByIssue.length > 0}
				<h3 class="text-lg font-semibold mt-4">By Issue ({dupesByIssue.length} group{dupesByIssue.length !== 1 ? 's' : ''})</h3>
				{#each dupesByIssue as group (group.key)}
					<div class="bg-gray-800 rounded-lg border border-gray-700 p-4 space-y-2">
						<p class="text-xs text-gray-500">Issue ID: {group.key}</p>
						{#each group.files as file (file.id)}
							<div class="flex items-center justify-between p-2 bg-gray-700/50 rounded">
								<span class="text-sm text-gray-300 truncate flex-1" title={file.file_path}>{file.file_name}</span>
								<div class="flex items-center gap-3 flex-shrink-0 ml-4">
									<span class="text-xs text-gray-500">{formatSize(file.file_size)}</span>
									<button onclick={() => deleteFile(file.id, true)} class="text-xs text-red-400 hover:text-red-300">Delete</button>
								</div>
							</div>
						{/each}
					</div>
				{/each}
			{/if}
		{/if}
	</div>
{:else}
	<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
		<div class="inline-flex w-full sm:w-auto bg-gray-900/40 border border-gray-700 rounded-xl p-1">
			<button
				onclick={() => switchFileView('folders')}
				class="flex-1 px-4 py-2 text-sm font-semibold rounded-lg transition-colors
					{fileViewMode === 'folders' ? 'bg-amber-500 text-gray-900 shadow' : 'text-gray-400 hover:text-gray-200'}"
			>
				Folders
			</button>
			<button
				onclick={() => switchFileView('table')}
				class="flex-1 px-4 py-2 text-sm font-semibold rounded-lg transition-colors
					{fileViewMode === 'table' ? 'bg-amber-500 text-gray-900 shadow' : 'text-gray-400 hover:text-gray-200'}"
			>
				Table
			</button>
		</div>
		<div class="w-full sm:w-auto">
			{#if fileViewMode === 'folders'}
				<input
					type="text"
					bind:value={folderSearchInput}
					placeholder="Filter folders by name or path..."
					class="w-full px-4 py-2 bg-gray-800 border border-gray-700 rounded-lg text-gray-200 placeholder-gray-500 focus:outline-none focus:border-amber-500"
				/>
			{:else}
				<input
					type="text"
					bind:value={searchInput}
					oninput={handleSearchInput}
					placeholder="Search files by name..."
					class="w-full sm:w-96 px-4 py-2 bg-gray-800 border border-gray-700 rounded-lg text-gray-200 placeholder-gray-500 focus:outline-none focus:border-amber-500"
				/>
			{/if}
		</div>
	</div>

	{#if fileViewMode === 'folders'}
		{#if folderError}
			<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
				<p class="text-red-400">{folderError}</p>
			</div>
		{:else if folderLoading && !folderLoaded}
			<div class="flex items-center justify-center py-12 text-gray-400">Loading folders…</div>
		{:else if folderGroups.length === 0}
			<div class="text-center py-12 text-gray-400">
				{folderSearch ? 'No folders match your filter.' : 'No folders found yet.'}
			</div>
		{:else}
			<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
				{#each folderGroups as group (group.id)}
					<FileFolderCard {group} on:select={(event) => selectedFolder = event.detail} />
				{/each}
			</div>
		{/if}
	{:else}
		{#if error}
			<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
				<p class="text-red-400">{error}</p>
			</div>
		{/if}

		{#if loading}
			<div class="flex items-center justify-center py-12">
				<div class="text-gray-400">Loading...</div>
			</div>
		{:else if files.length === 0}
			<div class="text-center py-12 text-gray-400">
				{searchQuery ? 'No files match your search.' : 'No files found.'}
			</div>
		{:else}
			<div class="overflow-x-auto">
				<table class="w-full text-sm">
					<thead>
						<tr class="text-left text-gray-400 border-b border-gray-700">
							<th class="pb-3 pr-4">Filename</th>
							<th class="pb-3 pr-4 w-20">Format</th>
							<th class="pb-3 pr-4 w-24">Size</th>
							<th class="pb-3 w-16">Actions</th>
						</tr>
					</thead>
					<tbody>
						{#each files as file (file.id)}
							<tr class="border-b border-gray-800 hover:bg-gray-800/50">
								<td class="py-3 pr-4">
									{#if editingFileId === file.id}
										<div class="flex flex-col gap-1">
											<div class="flex items-center gap-1">
												<input
													type="text"
													bind:value={editFileName}
													onkeydown={(e) => handleRenameKeydown(e, file)}
													class="flex-1 min-w-0 px-2 py-1 bg-gray-700 border border-gray-600 rounded text-gray-200
														focus:outline-none focus:border-amber-500 text-sm"
													disabled={renamingFileId === file.id}
												/>
												<span class="text-gray-500 text-sm flex-shrink-0">{file.file_name.substring(file.file_name.lastIndexOf('.'))}</span>
											</div>
											{#if renameError}
												<p class="text-xs text-red-400">{renameError}</p>
											{/if}
											<div class="flex gap-1">
												<button
														onclick={() => saveRename(file)}
														disabled={renamingFileId === file.id}
														class="text-xs px-2 py-1 bg-amber-500 hover:bg-amber-600 text-gray-900 rounded disabled:opacity-50"
													>
														{renamingFileId === file.id ? 'Saving...' : 'Save'}
													</button>
												<button
													onclick={cancelRename}
													disabled={renamingFileId === file.id}
													class="text-xs px-2 py-1 bg-gray-700 hover:bg-gray-600 text-gray-300 rounded disabled:opacity-50"
												>
													Cancel
												</button>
											</div>
										</div>
									{:else}
										<span class="text-gray-200" title={file.file_path}>{file.file_name}</span>
									{/if}
								</td>
								<td class="py-3 pr-4">
									<span class="text-xs px-2 py-0.5 bg-gray-700 rounded text-gray-300 uppercase">{file.file_format}</span>
								</td>
								<td class="py-3 pr-4 text-gray-400">{formatSize(file.file_size)}</td>
								<td class="py-3">
									{#if editingFileId !== file.id}
										<button
											onclick={() => startRename(file)}
											class="text-gray-500 hover:text-amber-400 transition-colors"
											title="Rename file"
										>
											<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
												<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
													d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
											</svg>
										</button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			{#if totalPages > 1}
				<div class="flex items-center justify-center gap-2 pt-4">
					<button
						onclick={() => goToPage(currentPage - 1)}
						disabled={currentPage <= 1}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 text-gray-300 rounded-lg disabled:opacity-50 disabled:cursor-not-allowed"
					>
						Previous
					</button>
					<span class="text-sm text-gray-400">
						Page {currentPage} of {totalPages}
					</span>
					<button
						onclick={() => goToPage(currentPage + 1)}
						disabled={currentPage >= totalPages}
						class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 text-gray-300 rounded-lg disabled:opacity-50 disabled:cursor-not-allowed"
					>
						Next
					</button>
				</div>
			{/if}
		{/if}
	{/if}

{/if}
</div>

{#if selectedFolder}
	<div
		class="fixed inset-0 bg-black/80 backdrop-blur-sm flex items-center justify-center z-50"
		role="dialog"
		aria-modal="true"
		tabindex="-1"
		onkeydown={(event) => { if (event.key === 'Escape') selectedFolder = null; }}
		onclick={(event) => {
			if (event.target === event.currentTarget) {
				selectedFolder = null;
			}
		}}
	>
		<div class="bg-gray-900 border border-gray-700 rounded-2xl shadow-2xl w-full max-w-5xl max-h-[90vh] overflow-hidden">
			<div class="flex items-center justify-between p-4 border-b border-gray-800">
				<div>
					<p class="text-xs uppercase tracking-wide text-gray-500">Folder</p>
					<h3 class="text-xl font-semibold text-white">{selectedFolder.label}</h3>
					<p class="text-xs text-gray-500 mt-1" title={selectedFolder.path}>{selectedFolder.path}</p>
				</div>
				<div class="flex items-center gap-4 text-sm text-gray-400">
					<span>{selectedFolder.files.length} file{selectedFolder.files.length === 1 ? '' : 's'}</span>
					<span>{formatSize(selectedFolder.totalSize)}</span>
					<button
						onclick={() => selectedFolder = null}
						class="text-gray-400 hover:text-white transition-colors"
						title="Close"
					>
						<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
						</svg>
					</button>
				</div>
			</div>
			<div class="flex flex-col md:flex-row">
				<div class="md:w-64 border-b md:border-b-0 md:border-r border-gray-800 p-4 space-y-3 bg-gray-950/40">
					<h4 class="text-sm font-semibold text-gray-200">Formats</h4>
					{#if Object.keys(selectedFolder.formatCounts).length === 0}
						<p class="text-xs text-gray-500">No format data.</p>
					{:else}
						<div class="space-y-2">
							{#each Object.entries(selectedFolder.formatCounts) as [fmt, count]}
								<div class="flex items-center justify-between text-xs text-gray-300">
									<span class="uppercase">{fmt}</span>
									<span>{count}</span>
								</div>
							{/each}
						</div>
					{/if}
				</div>
				<div class="flex-1 overflow-y-auto max-h-[calc(90vh-96px)]">
					<div class="divide-y divide-gray-800">
						{#each selectedFolder.files as file (file.id)}
							<div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 px-4 py-3">
								<div class="min-w-0">
									<p class="text-sm text-gray-100 truncate" title={file.file_name}>{file.file_name}</p>
									<p class="text-xs text-gray-500 truncate" title={file.file_path}>{file.file_path}</p>
								</div>
								<div class="text-xs text-gray-400 flex items-center gap-3 flex-shrink-0">
									<span class="uppercase px-2 py-0.5 rounded-full bg-gray-800 text-gray-200">{file.file_format}</span>
									<span>{formatSize(file.file_size)}</span>
								</div>
							</div>
						{/each}
					</div>
				</div>
			</div>
		</div>
	</div>
{/if}
