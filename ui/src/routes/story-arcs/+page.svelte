<script lang="ts">
	import { ApiClient, type StoryArc, type StoryArcListResponse, type CVSearchResult } from '$lib/api/client';

	let arcs = $state<StoryArc[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Search state
	let searchQuery = $state('');
	let searchResults = $state<CVSearchResult[]>([]);
	let searching = $state(false);
	let searchError = $state<string | null>(null);
	let searchPage = $state(1);
	let searchTotal = $state(0);
	let importing = $state<number | null>(null);

	async function loadArcs() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<StoryArcListResponse>('/story-arcs');
			arcs = data.story_arcs || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load story arcs';
		} finally {
			loading = false;
		}
	}

	async function searchComicVine() {
		if (!searchQuery.trim()) return;
		searching = true;
		searchError = null;
		try {
			const data = await ApiClient.get<{ results: CVSearchResult[]; total: number; page: number }>(
				`/metadata/story-arcs/search?q=${encodeURIComponent(searchQuery)}&page=${searchPage}`
			);
			searchResults = data.results || [];
			searchTotal = data.total;
		} catch (e) {
			searchError = e instanceof Error ? e.message : 'Search failed';
		} finally {
			searching = false;
		}
	}

	async function importArc(cvId: number) {
		importing = cvId;
		try {
			await ApiClient.post('/story-arcs/import', { comicvine_id: cvId });
			searchResults = [];
			searchQuery = '';
			await loadArcs();
		} catch (e) {
			searchError = e instanceof Error ? e.message : 'Import failed';
		} finally {
			importing = null;
		}
	}

	async function deleteArc(id: number) {
		if (!confirm('Delete this story arc?')) return;
		try {
			await ApiClient.delete(`/story-arcs/${id}`);
			arcs = arcs.filter(a => a.id !== id);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	function handleSearchKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			searchPage = 1;
			searchComicVine();
		}
	}

	$effect(() => {
		loadArcs();
	});
</script>

<div class="space-y-6">
	<div>
		<h1 class="text-3xl font-bold">Story Arcs</h1>
		<p class="text-gray-400 mt-1">Track reading orders and crossover events</p>
	</div>

	<!-- Search ComicVine -->
	<div class="bg-gray-800 rounded-lg border border-gray-700 p-4">
		<h2 class="text-lg font-semibold mb-3">Import from ComicVine</h2>
		<div class="flex gap-2">
			<input
				type="text"
				bind:value={searchQuery}
				onkeydown={handleSearchKeydown}
				placeholder="Search for a story arc..."
				class="flex-1 px-4 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-200
					placeholder-gray-500 focus:outline-none focus:border-amber-500"
			/>
			<button
				onclick={() => { searchPage = 1; searchComicVine(); }}
				disabled={searching || !searchQuery.trim()}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					text-gray-900 font-semibold rounded-lg transition-colors"
			>
				{searching ? 'Searching...' : 'Search'}
			</button>
		</div>

		{#if searchError}
			<p class="text-red-400 text-sm mt-2">{searchError}</p>
		{/if}

		{#if searchResults.length > 0}
			<div class="mt-4 space-y-2">
				{#each searchResults as result (result.id)}
					<div class="flex items-center justify-between p-3 bg-gray-700/50 rounded-lg">
						<div class="flex-1 min-w-0">
							<p class="text-gray-200 font-medium">{result.name}</p>
							{#if result.description}
								<p class="text-gray-400 text-sm truncate mt-1">{result.description.replace(/<[^>]*>/g, '').slice(0, 120)}</p>
							{/if}
						</div>
						<button
							onclick={() => importArc(result.id)}
							disabled={importing === result.id}
							class="ml-4 px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
								text-gray-900 font-semibold rounded-lg transition-colors flex-shrink-0"
						>
							{importing === result.id ? 'Importing...' : 'Import'}
						</button>
					</div>
				{/each}
				{#if searchTotal > searchResults.length}
					<p class="text-gray-500 text-sm text-center pt-2">
						Showing {searchResults.length} of {searchTotal} results
					</p>
				{/if}
			</div>
		{/if}
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-12">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if arcs.length === 0}
		<div class="text-center py-12 text-gray-400">
			<p class="text-lg">No story arcs imported yet.</p>
			<p class="text-sm mt-2">Search ComicVine above to import a story arc.</p>
		</div>
	{:else}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
			{#each arcs as arc (arc.id)}
				<a href="/story-arcs/{arc.id}" class="block bg-gray-800 rounded-lg border border-gray-700 p-4 hover:border-amber-500/50 transition-colors">
					<div class="flex items-start justify-between gap-2">
						<h3 class="text-lg font-semibold text-gray-200">{arc.name}</h3>
						<button
							onclick={(e) => { e.preventDefault(); deleteArc(arc.id); }}
							class="text-gray-500 hover:text-red-400 transition-colors flex-shrink-0"
							title="Delete story arc"
						>
							<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
								<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
							</svg>
						</button>
					</div>
					{#if arc.description}
						<p class="text-gray-400 text-sm mt-2 line-clamp-2">{arc.description}</p>
					{/if}
					<div class="flex items-center gap-3 mt-3">
						<span class="text-sm text-gray-400">{arc.issue_count} issue{arc.issue_count !== 1 ? 's' : ''}</span>
						{#if arc.issue_count > 0}
							<div class="flex-1 bg-gray-700 rounded-full h-2 overflow-hidden">
								<div
									class="bg-amber-500 h-full rounded-full transition-all"
									style="width: {Math.round((arc.owned_count / arc.issue_count) * 100)}%"
								></div>
							</div>
							<span class="text-sm text-amber-400">{arc.owned_count}/{arc.issue_count}</span>
						{/if}
					</div>
				</a>
			{/each}
		</div>
	{/if}
</div>
