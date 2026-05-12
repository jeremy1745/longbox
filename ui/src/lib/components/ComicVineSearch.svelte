<script lang="ts">
	import { ApiClient, type MetadataSearchResult, type MetadataSearchResponse } from '$lib/api/client';

	let {
		seriesTitle = '',
		seriesId,
		onMatched,
		onClose,
	}: {
		seriesTitle?: string;
		seriesId: number;
		onMatched: () => void;
		onClose: () => void;
	} = $props();

	let query = $state(seriesTitle);
	let results = $state<MetadataSearchResult[]>([]);
	let total = $state(0);
	let page = $state(1);
	let searching = $state(false);
	let matching = $state(false);
	let matchingId = $state<number | null>(null);
	let error = $state<string | null>(null);
	let hasSearched = $state(false);

	async function search() {
		if (!query.trim()) return;
		searching = true;
		error = null;
		hasSearched = true;
		try {
			const data = await ApiClient.get<MetadataSearchResponse>(
				`/metadata/search?q=${encodeURIComponent(query.trim())}&page=${page}`
			);
			results = data.results || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Search failed';
			results = [];
		} finally {
			searching = false;
		}
	}

	async function matchToVolume(cvId: number) {
		matching = true;
		matchingId = cvId;
		error = null;
		try {
			await ApiClient.post(`/series/${seriesId}/match`, { comicvine_id: cvId });
			onMatched();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Match failed';
		} finally {
			matching = false;
			matchingId = null;
		}
	}

	function nextPage() {
		page++;
		search();
	}

	function prevPage() {
		if (page > 1) {
			page--;
			search();
		}
	}

	// Auto-search on mount if we have a title
	$effect(() => {
		if (query.trim()) {
			search();
		}
	});
</script>

<!-- Modal Backdrop -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 bg-black/70 z-50 flex items-start justify-center pt-16 px-4"
	onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
	<div class="bg-gray-800 border border-gray-700 rounded-xl w-full max-w-3xl max-h-[80vh] flex flex-col shadow-2xl">
		<!-- Header -->
		<div class="p-4 border-b border-gray-700 flex items-center justify-between flex-shrink-0">
			<h2 class="text-lg font-semibold">Match to ComicVine</h2>
			<button onclick={onClose} class="text-gray-400 hover:text-gray-200 text-xl">&times;</button>
		</div>

		<!-- Search bar -->
		<div class="p-4 border-b border-gray-700 flex-shrink-0">
			<div class="flex gap-2">
				<input
					type="text"
					bind:value={query}
					placeholder="Search ComicVine volumes..."
					class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
						text-gray-100 placeholder-gray-500
						focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
					onkeydown={(e) => { if (e.key === 'Enter') { page = 1; search(); } }}
				/>
				<button
					onclick={() => { page = 1; search(); }}
					disabled={searching || !query.trim()}
					class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
						disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
				>
					{searching ? 'Searching...' : 'Search'}
				</button>
			</div>
		</div>

		{#if error}
			<div class="px-4 pt-3 flex-shrink-0">
				<div class="bg-red-900/30 border border-red-700 rounded-lg p-3">
					<p class="text-sm text-red-400">{error}</p>
				</div>
			</div>
		{/if}

		<!-- Results -->
		<div class="flex-1 overflow-y-auto p-4 space-y-3">
			{#if searching}
				<div class="flex items-center justify-center py-10">
					<div class="text-gray-400">Searching ComicVine...</div>
				</div>
			{:else if results.length > 0}
				{#each results as result}
					<div class="flex gap-4 p-3 bg-gray-750 rounded-lg border border-gray-700 hover:border-gray-600 transition-colors"
						style="background-color: rgb(31 41 55);">
						<!-- Thumbnail -->
						<div class="flex-shrink-0 w-16 h-24 bg-gray-700 rounded overflow-hidden">
							{#if result.image_url}
								<img
									src={result.image_url}
									alt={result.name}
									class="w-full h-full object-cover"
								/>
							{:else}
								<div class="w-full h-full flex items-center justify-center text-gray-500 text-xs">
									No image
								</div>
							{/if}
						</div>

						<!-- Info -->
						<div class="flex-1 min-w-0">
							<h3 class="font-semibold text-gray-100 truncate">{result.name}</h3>
							<div class="flex flex-wrap items-center gap-2 mt-1 text-xs text-gray-400">
								{#if result.start_year}
									<span>{result.start_year}</span>
								{/if}
								{#if result.publisher}
									<span>&middot;</span>
									<span>{result.publisher}</span>
								{/if}
								<span>&middot;</span>
								<span>{result.issue_count} issues</span>
							</div>
							{#if result.description}
								<p class="text-xs text-gray-400 mt-2 line-clamp-2">{result.description}</p>
							{/if}
						</div>

						<!-- Match button -->
						<div class="flex-shrink-0 self-center">
							<button
								onclick={() => matchToVolume(result.comicvine_id)}
								disabled={matching}
								class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600
									disabled:bg-gray-600 disabled:cursor-not-allowed
									text-gray-900 font-semibold rounded-lg transition-colors"
							>
								{#if matching && matchingId === result.comicvine_id}
									Matching...
								{:else}
									Match
								{/if}
							</button>
						</div>
					</div>
				{/each}

				<!-- Pagination -->
				{#if total > 10}
					<div class="flex items-center justify-center gap-3 pt-2">
						<button
							onclick={prevPage}
							disabled={page <= 1}
							class="px-3 py-1 text-sm bg-gray-700 rounded hover:bg-gray-600
								disabled:opacity-50 disabled:cursor-not-allowed"
						>
							Previous
						</button>
						<span class="text-sm text-gray-400">Page {page}</span>
						<button
							onclick={nextPage}
							disabled={results.length < 10}
							class="px-3 py-1 text-sm bg-gray-700 rounded hover:bg-gray-600
								disabled:opacity-50 disabled:cursor-not-allowed"
						>
							Next
						</button>
					</div>
				{/if}
			{:else if hasSearched}
				<div class="flex items-center justify-center py-10 text-gray-400">
					<p>No results found. Try a different search term.</p>
				</div>
			{/if}
		</div>
	</div>
</div>
