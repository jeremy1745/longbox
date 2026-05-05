<script lang="ts">
	import { ApiClient, type Series, type SeriesListResponse } from '$lib/api/client';
	import SeriesCard from '$lib/components/SeriesCard.svelte';
	import ComicVineExplorer from '$lib/components/ComicVineExplorer.svelte';

	let series = $state<Series[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let page = $state(1);
	const perPage = 60;
	let sortBy = $state('title');
	let order = $state<'asc' | 'desc'>('asc');
	let trackedFilter = $state(false);
	let searchQuery = $state('');
	let showComicVineExplorer = $state(false);

	// Series delete state
	let pendingDelete = $state<Series | null>(null);
	let deleting = $state(false);
	let deleteError = $state<string | null>(null);
	let deleteNotice = $state<string | null>(null);

	let totalPages = $derived(Math.ceil(total / perPage));

	let filteredSeries = $derived.by(() => {
		const q = searchQuery.trim().toLowerCase();
		if (!q) return series;
		return series.filter((s) => s.title.toLowerCase().includes(q));
	});

	async function loadSeries() {
		loading = true;
		error = null;
		try {
			let url = `/series?page=${page}&per_page=${perPage}&sort=${sortBy}&order=${order}`;
			if (trackedFilter) url += '&tracked=true';
			const data = await ApiClient.get<SeriesListResponse>(url);
			series = data.series || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load series';
		} finally {
			loading = false;
		}
	}

	function toggleSort(field: string) {
		if (sortBy === field) {
			order = order === 'asc' ? 'desc' : 'asc';
		} else {
			sortBy = field;
			order = field === 'title' ? 'asc' : 'desc';
		}
		page = 1;
		loadSeries();
	}

	function setTracked(value: boolean) {
		if (trackedFilter === value) return;
		trackedFilter = value;
		page = 1;
		loadSeries();
	}

	function goToPage(p: number) {
		const next = Math.max(1, Math.min(totalPages || 1, p));
		if (next === page) return;
		page = next;
		loadSeries();
	}

	function handleTrackedFromExplorer(_series: Series) {
		loadSeries();
	}

	function requestDelete(s: Series) {
		pendingDelete = s;
		deleteError = null;
	}

	function cancelDelete() {
		if (deleting) return;
		pendingDelete = null;
		deleteError = null;
	}

	async function confirmDelete() {
		if (!pendingDelete) return;
		const target = pendingDelete;
		deleting = true;
		deleteError = null;
		try {
			const result = await ApiClient.delete<{ issues_deleted: number; files_trashed: number; errors?: string[] }>(
				`/series/${target.id}`
			);
			deleteNotice = `Deleted "${target.title}" — ${result.issues_deleted} issue${result.issues_deleted === 1 ? '' : 's'} removed, ${result.files_trashed} file${result.files_trashed === 1 ? '' : 's'} trashed.`;
			if (result.errors && result.errors.length > 0) {
				deleteNotice += ` Error: ${result.errors[0]}`;
			if (result.errors.length > 1) {
				deleteNotice += ` (+${result.errors.length - 1} more — see server log)`;
			}
			}
			pendingDelete = null;
			await loadSeries();
		} catch (e) {
			deleteError = e instanceof Error ? e.message : 'Delete failed';
		} finally {
			deleting = false;
		}
	}

	$effect(() => {
		loadSeries();
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between gap-4">
		<div>
			<h1 class="text-3xl font-bold">Library</h1>
			<p class="text-gray-400 mt-1">
				{#if total > 0}
					{total} series
					{#if trackedFilter} &middot; tracked only{/if}
				{:else}
					Your comic series collection
				{/if}
			</p>
		</div>
		<button
			onclick={() => (showComicVineExplorer = true)}
			class="px-4 py-2 bg-amber-500 hover:bg-amber-600 text-gray-900 font-semibold rounded-lg transition-colors flex items-center gap-2"
			title="Search ComicVine and add a new series"
		>
			<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
			</svg>
			Add Series
		</button>
	</div>

	<!-- Filters -->
	<div class="flex flex-wrap items-center gap-3">
		<div class="relative flex-1 min-w-[200px] max-w-md">
			<input
				type="text"
				bind:value={searchQuery}
				placeholder="Filter shown series by title…"
				class="w-full pl-9 pr-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
			/>
			<svg class="absolute left-2.5 top-2.5 w-4 h-4 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
			</svg>
		</div>

		<div class="inline-flex rounded-lg overflow-hidden border border-gray-700">
			<button
				onclick={() => setTracked(false)}
				class="px-3 py-2 text-sm transition-colors {!trackedFilter ? 'bg-amber-500 text-gray-900 font-semibold' : 'bg-gray-800 text-gray-300 hover:bg-gray-700'}"
			>
				All
			</button>
			<button
				onclick={() => setTracked(true)}
				class="px-3 py-2 text-sm transition-colors {trackedFilter ? 'bg-amber-500 text-gray-900 font-semibold' : 'bg-gray-800 text-gray-300 hover:bg-gray-700'}"
			>
				Tracked
			</button>
		</div>

		<div class="inline-flex rounded-lg overflow-hidden border border-gray-700">
			<button
				onclick={() => toggleSort('title')}
				class="px-3 py-2 text-sm transition-colors {sortBy === 'title' ? 'bg-gray-700 text-gray-100' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				title="Sort by title"
			>
				Title {sortBy === 'title' ? (order === 'asc' ? '↑' : '↓') : ''}
			</button>
			<button
				onclick={() => toggleSort('issue_count')}
				class="px-3 py-2 text-sm transition-colors {sortBy === 'issue_count' ? 'bg-gray-700 text-gray-100' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				title="Sort by issue count"
			>
				Issues {sortBy === 'issue_count' ? (order === 'asc' ? '↑' : '↓') : ''}
			</button>
		</div>
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading && series.length === 0}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading…</div>
		</div>
	{:else if series.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253" />
			</svg>
			<p class="text-lg font-medium">No series yet</p>
			<p class="text-sm mt-2">Run a Library Scan from the dashboard to discover comics on disk, or use Add Series to import from ComicVine.</p>
		</div>
	{:else if filteredSeries.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<p class="text-sm">No series match "{searchQuery}".</p>
		</div>
	{:else}
		<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
			{#each filteredSeries as s (s.id)}
				<SeriesCard series={s} onDelete={requestDelete} />
			{/each}
		</div>

		{#if totalPages > 1}
			<div class="flex items-center justify-center gap-3 pt-2">
				<button
					onclick={() => goToPage(page - 1)}
					disabled={page <= 1}
					class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-800 disabled:text-gray-600 disabled:cursor-not-allowed text-gray-200 rounded-lg transition-colors"
				>
					Previous
				</button>
				<span class="text-sm text-gray-400">
					Page {page} of {totalPages}
				</span>
				<button
					onclick={() => goToPage(page + 1)}
					disabled={page >= totalPages}
					class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-800 disabled:text-gray-600 disabled:cursor-not-allowed text-gray-200 rounded-lg transition-colors"
				>
					Next
				</button>
			</div>
		{/if}
	{/if}
</div>

{#if showComicVineExplorer}
	<ComicVineExplorer
		onClose={() => (showComicVineExplorer = false)}
		onTracked={handleTrackedFromExplorer}
	/>
{/if}

{#if pendingDelete}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="fixed inset-0 bg-black/70 flex items-center justify-center z-50 px-4"
		onclick={(e) => { if (e.target === e.currentTarget) cancelDelete(); }}
		onkeydown={(e) => { if (e.key === 'Escape') cancelDelete(); }}
		tabindex="-1"
		role="dialog"
		aria-modal="true"
	>
		<div class="bg-gray-900 border border-red-700 rounded-2xl shadow-2xl w-full max-w-md p-6 space-y-4">
			<h3 class="text-lg font-semibold text-red-300">Delete "{pendingDelete.title}"?</h3>
			<p class="text-sm text-gray-300">
				Every file in this series will be moved to the OS recycle bin. All
				issue and file records, plus the series record itself, will be removed
				from the database.
				{#if pendingDelete.file_count > 0}
					<span class="block mt-2 text-amber-300/90">
						{pendingDelete.file_count} file{pendingDelete.file_count === 1 ? '' : 's'} will be trashed.
					</span>
				{/if}
			</p>
			<p class="text-xs text-gray-500">
				Files are reversible — restore from the Recycle Bin and re-scan to
				re-import. Database deletion is permanent.
			</p>
			{#if deleteError}
				<p class="text-sm text-red-400">{deleteError}</p>
			{/if}
			<div class="flex justify-end gap-2">
				<button
					onclick={cancelDelete}
					class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 rounded-lg"
					disabled={deleting}
				>Cancel</button>
				<button
					onclick={confirmDelete}
					class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-500 text-white rounded-lg disabled:opacity-50"
					disabled={deleting}
				>{deleting ? 'Deleting…' : 'Delete Series'}</button>
			</div>
		</div>
	</div>
{/if}

{#if deleteNotice}
	<div class="fixed bottom-4 right-4 bg-gray-800 border border-amber-500/40 rounded-lg shadow-xl px-4 py-3 max-w-sm z-40">
		<p class="text-sm text-amber-200">{deleteNotice}</p>
		<button onclick={() => (deleteNotice = null)} class="text-xs text-gray-400 hover:text-gray-200 mt-1">dismiss</button>
	</div>
{/if}
