<script lang="ts">
	import { ApiClient, type Series, type SeriesListResponse } from '$lib/api/client';
	import SeriesCard from '$lib/components/SeriesCard.svelte';

	let series = $state<Series[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let page = $state(1);
	let perPage = 60;
	let sortBy = $state('title');
	let order = $state<'asc' | 'desc'>('asc');
	let trackedFilter = $state(false);

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
			order = 'asc';
		}
		page = 1;
		loadSeries();
	}

	let totalPages = $derived(Math.ceil(total / perPage));

	$effect(() => {
		loadSeries();
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Library</h1>
			<p class="text-gray-400 mt-1">
				{#if total > 0}
					{total} series
				{:else}
					No series found
				{/if}
			</p>
		</div>
		<div class="flex gap-2 items-center">
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
			<button
				onclick={() => toggleSort('title')}
				class="px-3 py-1.5 text-sm rounded-md transition-colors {sortBy === 'title' ? 'bg-amber-500 text-gray-900' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
			>
				A-Z {sortBy === 'title' ? (order === 'asc' ? '↑' : '↓') : ''}
			</button>
			<button
				onclick={() => toggleSort('year')}
				class="px-3 py-1.5 text-sm rounded-md transition-colors {sortBy === 'year' ? 'bg-amber-500 text-gray-900' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
			>
				Year {sortBy === 'year' ? (order === 'asc' ? '↑' : '↓') : ''}
			</button>
			<button
				onclick={() => toggleSort('issue_count')}
				class="px-3 py-1.5 text-sm rounded-md transition-colors {sortBy === 'issue_count' ? 'bg-amber-500 text-gray-900' : 'bg-gray-700 text-gray-300 hover:bg-gray-600'}"
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
</div>
