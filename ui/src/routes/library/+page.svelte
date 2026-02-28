<script lang="ts">
	import { ApiClient, type Series, type SeriesListResponse } from '$lib/api/client';
	import SeriesCard from '$lib/components/SeriesCard.svelte';

	// View mode: 'grid' = flat series grid (original), 'browse' = folder hierarchy
	let viewMode = $state<'grid' | 'browse'>('grid');

	// --- Grid mode state ---
	let series = $state<Series[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let page = $state(1);
	let perPage = 60;
	let sortBy = $state('title');
	let order = $state<'asc' | 'desc'>('asc');
	let trackedFilter = $state(false);

	// --- Browse mode state ---
	let allSeries = $state<Series[]>([]);
	let browseLoading = $state(false);
	let browseLoaded = $state(false);
	// Breadcrumb path: [] = publishers, [publisher] = series
	let browsePath = $state<string[]>([]);

	// Derived: unique publishers sorted alphabetically
	let publishers = $derived(() => {
		const pubMap = new Map<string, { name: string; seriesCount: number; fileCount: number }>();
		for (const s of allSeries) {
			const pub = s.publisher_name || 'Unknown Publisher';
			const existing = pubMap.get(pub);
			if (existing) {
				existing.seriesCount++;
				existing.fileCount += s.file_count;
			} else {
				pubMap.set(pub, { name: pub, seriesCount: 1, fileCount: s.file_count });
			}
		}
		return [...pubMap.values()].sort((a, b) => a.name.localeCompare(b.name));
	});

	// Derived: series for selected publisher
	let filteredSeries = $derived(() => {
		if (browsePath.length < 1) return [];
		const pub = browsePath[0];
		return allSeries
			.filter(s => {
				const sPub = s.publisher_name || 'Unknown Publisher';
				return sPub === pub;
			})
			.sort((a, b) => a.title.localeCompare(b.title));
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

	async function loadAllSeries() {
		if (browseLoaded) return;
		browseLoading = true;
		error = null;
		try {
			// Fetch all series in one large request for client-side grouping
			const data = await ApiClient.get<SeriesListResponse>(
				`/series?page=1&per_page=10000&sort=title&order=asc`
			);
			allSeries = data.series || [];
			browseLoaded = true;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load series';
		} finally {
			browseLoading = false;
		}
	}

	function toggleSort(field: string) {
		if (sortBy === field) {
			order = order === 'asc' ? 'desc' : 'asc';
		} else {
			sortBy = field;
			order = 'asc';
		}
		page = 1;
		loadSeries();
	}

	function switchView(mode: 'grid' | 'browse') {
		viewMode = mode;
		if (mode === 'browse') {
			browsePath = [];
			loadAllSeries();
		}
	}

	function navigateTo(...path: string[]) {
		browsePath = path;
	}

	let totalPages = $derived(Math.ceil(total / perPage));

	$effect(() => {
		if (viewMode === 'grid') {
			loadSeries();
		}
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Library</h1>
			<p class="text-gray-400 mt-1">
				{#if viewMode === 'grid'}
					{#if total > 0}
						{total} series
					{:else}
						No series found
					{/if}
				{:else}
					{allSeries.length} series
				{/if}
			</p>
		</div>
		<div class="flex gap-2 items-center">
			<!-- View mode toggle -->
			<div class="flex gap-1 mr-2 border-r border-gray-700 pr-3">
				<button
					onclick={() => switchView('grid')}
					class="px-3 py-1.5 text-sm rounded-md transition-colors flex items-center gap-1.5
						{viewMode === 'grid' ? 'bg-gray-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
					title="Grid view"
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zM14 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zM14 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
					</svg>
					Grid
				</button>
				<button
					onclick={() => switchView('browse')}
					class="px-3 py-1.5 text-sm rounded-md transition-colors flex items-center gap-1.5
						{viewMode === 'browse' ? 'bg-gray-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
					title="Browse by Publisher"
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
					</svg>
					Browse
				</button>
			</div>

			{#if viewMode === 'grid'}
				<!-- Tracked filter -->
				<div class="flex gap-1 mr-2 border-r border-gray-700 pr-3">
					<button
						onclick={() => { trackedFilter = false; page = 1; loadSeries(); }}
						class="px-3 py-1.5 text-sm rounded-md transition-colors {!trackedFilter ? 'bg-gray-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
					>
						All
					</button>
					<button
						onclick={() => { trackedFilter = true; page = 1; loadSeries(); }}
						class="px-3 py-1.5 text-sm rounded-md transition-colors flex items-center gap-1
							{trackedFilter ? 'bg-amber-500/20 text-amber-400 border border-amber-500/50' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
					>
						<svg class="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24">
							<path d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
						</svg>
						Tracked
					</button>
				</div>

				<!-- Sort buttons -->
				<button
					onclick={() => toggleSort('title')}
					class="px-3 py-1.5 text-sm rounded-md transition-colors {sortBy === 'title' ? 'bg-amber-500 text-gray-900' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
				>
					A-Z {sortBy === 'title' ? (order === 'asc' ? '↑' : '↓') : ''}
				</button>
				<button
					onclick={() => toggleSort('issue_count')}
					class="px-3 py-1.5 text-sm rounded-md transition-colors {sortBy === 'issue_count' ? 'bg-amber-500 text-gray-900' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
				>
					Issues {sortBy === 'issue_count' ? (order === 'asc' ? '↑' : '↓') : ''}
				</button>
			{/if}
		</div>
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	<!-- ==================== GRID VIEW ==================== -->
	{#if viewMode === 'grid'}
		{#if loading}
			<div class="flex items-center justify-center py-20">
				<div class="text-gray-400">Loading...</div>
			</div>
		{:else if series.length > 0}
			<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
				{#each series as s (s.id)}
					<SeriesCard series={s} />
				{/each}
			</div>

			<!-- Pagination -->
			{#if totalPages > 1}
				<div class="flex items-center justify-center gap-2 pt-4">
					<button
						onclick={() => { page = Math.max(1, page - 1); loadSeries(); }}
						disabled={page <= 1}
						class="px-3 py-1.5 text-sm bg-gray-700 rounded-md hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
					>
						Previous
					</button>
					<span class="text-sm text-gray-400">
						Page {page} of {totalPages}
					</span>
					<button
						onclick={() => { page = Math.min(totalPages, page + 1); loadSeries(); }}
						disabled={page >= totalPages}
						class="px-3 py-1.5 text-sm bg-gray-700 rounded-md hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
					>
						Next
					</button>
				</div>
			{/if}
		{:else}
			<div class="flex flex-col items-center justify-center py-20 text-gray-400">
				<p class="text-lg font-medium">No series found</p>
				<p class="text-sm mt-2">Scan your library from the dashboard to discover comics.</p>
			</div>
		{/if}

	<!-- ==================== BROWSE VIEW ==================== -->
	{:else}
		{#if browseLoading}
			<div class="flex items-center justify-center py-20">
				<div class="text-gray-400">Loading library...</div>
			</div>
		{:else}
			<!-- Breadcrumb navigation -->
			<nav class="flex items-center gap-1.5 text-sm">
				<button
					onclick={() => navigateTo()}
					class="transition-colors {browsePath.length === 0 ? 'text-amber-400 font-semibold' : 'text-gray-400 hover:text-amber-300'}"
				>
					<svg class="w-4 h-4 inline -mt-0.5 mr-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
					</svg>
					Publishers
				</button>
				{#if browsePath.length >= 1}
					<svg class="w-4 h-4 text-gray-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
					</svg>
					<span class="text-amber-400 font-semibold">{browsePath[0]}</span>
				{/if}
			</nav>

			<!-- Level 0: Publishers -->
			{#if browsePath.length === 0}
				<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
					{#each publishers() as pub (pub.name)}
						<button
							onclick={() => navigateTo(pub.name)}
							class="group block rounded-lg overflow-hidden bg-gray-800 shadow-lg hover:shadow-xl hover:ring-2 hover:ring-amber-400/50 transition-all text-left"
						>
							<div class="relative aspect-[3/2] bg-gradient-to-br from-gray-700 to-gray-800 flex items-center justify-center">
								<svg class="w-16 h-16 text-gray-600 group-hover:text-amber-500/40 transition-colors" fill="currentColor" viewBox="0 0 24 24">
									<path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
								</svg>
								<div class="absolute top-2 right-2 bg-gray-900/80 backdrop-blur-sm text-gray-100 text-xs font-bold px-2 py-0.5 rounded-full shadow">
									{pub.seriesCount} series
								</div>
							</div>
							<div class="p-3">
								<p class="text-sm text-gray-200 font-medium truncate" title={pub.name}>
									{pub.name}
								</p>
								<p class="text-xs text-gray-500 mt-0.5">
									{pub.fileCount} file{pub.fileCount !== 1 ? 's' : ''}
								</p>
							</div>
						</button>
					{/each}
				</div>
				{#if publishers().length === 0}
					<div class="flex flex-col items-center justify-center py-20 text-gray-400">
						<p class="text-lg font-medium">No series found</p>
						<p class="text-sm mt-2">Scan your library from the dashboard to discover comics.</p>
					</div>
				{/if}

			<!-- Level 1: Series for a publisher -->
			{:else if browsePath.length === 1}
				<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
					{#each filteredSeries() as s (s.id)}
						<SeriesCard series={s} />
					{/each}
				</div>
				{#if filteredSeries().length === 0}
					<div class="flex flex-col items-center justify-center py-20 text-gray-400">
						<p class="text-lg font-medium">No series found</p>
					</div>
				{/if}
			{/if}
		{/if}
	{/if}
</div>
