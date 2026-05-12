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
	let matchingKey = $state<string | null>(null); // "cv:<n>" or "mt:<n>"
	let error = $state<string | null>(null);
	let hasSearched = $state(false);

	// Merge-conflict prompt state. Populated when /match returns 409.
	type MergeConflict = {
		source: 'comicvine' | 'metron';
		externalId: number;
		conflictWith: { series_id: number; title: string; year?: number; tracked?: boolean };
		// The body the user originally wanted to send — replayed after the merge.
		pendingMatchBody: { comicvine_id?: number; metron_id?: number };
	};
	let conflict = $state<MergeConflict | null>(null);
	let merging = $state(false);

	const BASE = '/api/v1';

	function resultKey(r: MetadataSearchResult): string {
		if (r.comicvine_id) return `cv:${r.comicvine_id}`;
		if (r.metron_id) return `mt:${r.metron_id}`;
		return `?:${r.name}`;
	}

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

	// Direct fetch (not ApiClient) so we can intercept a 409 MERGE_REQUIRED
	// body before the ApiClient discards it.
	async function matchTo(result: MetadataSearchResult) {
		matching = true;
		matchingKey = resultKey(result);
		error = null;
		conflict = null;
		const body: { comicvine_id?: number; metron_id?: number } = {};
		if (result.comicvine_id) body.comicvine_id = result.comicvine_id;
		if (result.metron_id) body.metron_id = result.metron_id;
		try {
			const res = await fetch(`${BASE}/series/${seriesId}/match`, {
				method: 'POST',
				credentials: 'include',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			});
			if (res.status === 409) {
				const data = await res.json();
				conflict = {
					source: data.source,
					externalId: data.external_id,
					conflictWith: data.conflict_with,
					pendingMatchBody: body
				};
				return;
			}
			if (!res.ok) {
				const data = await res.json().catch(() => null);
				throw new Error(data?.error?.message || `HTTP ${res.status}`);
			}
			onMatched();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Match failed';
		} finally {
			matching = false;
			matchingKey = null;
		}
	}

	// User confirmed the merge prompt — collapse this series into the existing one.
	async function confirmMerge() {
		if (!conflict) return;
		merging = true;
		error = null;
		try {
			const res = await fetch(
				`${BASE}/series/${seriesId}/merge-into/${conflict.conflictWith.series_id}`,
				{ method: 'POST', credentials: 'include' }
			);
			if (!res.ok) {
				const data = await res.json().catch(() => null);
				throw new Error(data?.error?.message || `HTTP ${res.status}`);
			}
			// Merge succeeded — the source series is gone. Close the modal so
			// the caller can navigate the user back to the (surviving) target.
			onMatched();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Merge failed';
		} finally {
			merging = false;
			conflict = null;
		}
	}

	function cancelMerge() {
		conflict = null;
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
			<h2 class="text-lg font-semibold">Match Series</h2>
			<button onclick={onClose} class="text-gray-400 hover:text-gray-200 text-xl">&times;</button>
		</div>

		<!-- Search bar -->
		<div class="p-4 border-b border-gray-700 flex-shrink-0">
			<div class="flex gap-2">
				<input
					type="text"
					bind:value={query}
					placeholder="Search ComicVine + Metron volumes..."
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

		<!-- Merge-conflict prompt -->
		{#if conflict}
			<div class="px-4 pt-3 flex-shrink-0">
				<div class="bg-amber-900/30 border border-amber-700 rounded-lg p-4">
					<p class="text-sm text-amber-200 font-semibold mb-1">
						Already tracked in LongBox
					</p>
					<p class="text-sm text-amber-100/90">
						<span class="font-medium">{conflict.conflictWith.title}</span>{#if conflict.conflictWith.year} ({conflict.conflictWith.year}){/if} is already
						matched to this {conflict.source === 'metron' ? 'Metron' : 'ComicVine'} ID.
					</p>
					<p class="text-xs text-amber-100/70 mt-2">
						Merge this series into it? Issues, files, and want-list entries from this
						series will be moved into the existing one. Files on disk will be
						reorganized into the canonical folder on the next reorganize pass.
					</p>
					<div class="mt-3 flex gap-2">
						<button
							onclick={confirmMerge}
							disabled={merging}
							class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600
								disabled:bg-gray-600 disabled:cursor-not-allowed
								text-gray-900 font-semibold rounded-lg transition-colors"
						>
							{merging ? 'Merging...' : 'Merge into existing series'}
						</button>
						<button
							onclick={cancelMerge}
							disabled={merging}
							class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600
								text-gray-200 rounded-lg transition-colors"
						>
							Cancel
						</button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Results -->
		<div class="flex-1 overflow-y-auto p-4 space-y-3">
			{#if searching}
				<div class="flex items-center justify-center py-10">
					<div class="text-gray-400">Searching...</div>
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
							<div class="flex items-center gap-2">
								<h3 class="font-semibold text-gray-100 truncate">{result.name}</h3>
								{#if result.sources?.includes('comicvine')}
									<span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-blue-900/50 text-blue-300 border border-blue-700/50">CV</span>
								{/if}
								{#if result.sources?.includes('metron')}
									<span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-purple-900/50 text-purple-300 border border-purple-700/50">Metron</span>
								{/if}
							</div>
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
								onclick={() => matchTo(result)}
								disabled={matching || merging}
								class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600
									disabled:bg-gray-600 disabled:cursor-not-allowed
									text-gray-900 font-semibold rounded-lg transition-colors"
							>
								{#if matching && matchingKey === resultKey(result)}
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
