<script lang="ts">
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

	type Result = {
		metron_id: number;
		name: string;
		display_name: string;
		year_began: number;
		issue_count: number;
		volume: number;
	};

	let query = $state(seriesTitle);
	let results = $state<Result[]>([]);
	let total = $state(0);
	let page = $state(1);
	let searching = $state(false);
	let matching = $state(false);
	let matchingId = $state<number | null>(null);
	let error = $state<string | null>(null);
	let hasSearched = $state(false);

	let conflict = $state<{
		metronId: number;
		conflictingSeriesId: number;
		conflictingSeriesTitle: string;
		message: string;
	} | null>(null);
	let merging = $state(false);

	async function search() {
		if (!query.trim()) return;
		searching = true;
		error = null;
		hasSearched = true;
		try {
			const res = await fetch(
				`/api/v1/metadata/metron/search?q=${encodeURIComponent(query.trim())}&page=${page}`,
				{ credentials: 'include' }
			);
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				error = body?.error?.message || `HTTP ${res.status}`;
				results = [];
				return;
			}
			const data = await res.json();
			results = (data.results as Result[]) || [];
			total = data.total ?? results.length;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Search failed';
			results = [];
		} finally {
			searching = false;
		}
	}

	async function matchToSeries(metronId: number) {
		matching = true;
		matchingId = metronId;
		error = null;
		conflict = null;
		try {
			const res = await fetch(`/api/v1/series/${seriesId}/match-metron`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ metron_id: metronId }),
				credentials: 'include',
			});
			if (res.status === 409) {
				const body = await res.json().catch(() => null);
				if (body && body.conflicting_series_id) {
					conflict = {
						metronId,
						conflictingSeriesId: body.conflicting_series_id,
						conflictingSeriesTitle: body.conflicting_series_title || `series #${body.conflicting_series_id}`,
						message: body?.error?.message || 'This match conflicts with an existing local series.',
					};
					return;
				}
				error = body?.error?.message || 'This match conflicts with an existing local series.';
				return;
			}
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				error = body?.error?.message || `HTTP ${res.status}`;
				return;
			}
			onMatched();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Match failed';
		} finally {
			matching = false;
			matchingId = null;
		}
	}

	async function mergeIntoConflicting() {
		if (!conflict) return;
		merging = true;
		error = null;
		try {
			const res = await fetch(`/api/v1/series/${seriesId}/merge-into/${conflict.conflictingSeriesId}`, {
				method: 'POST',
				credentials: 'include',
			});
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				error = body?.error?.message || `Merge failed (HTTP ${res.status})`;
				return;
			}
			window.location.href = `/library/${conflict.conflictingSeriesId}`;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Merge failed';
		} finally {
			merging = false;
		}
	}

	function cancelConflict() {
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

	$effect(() => {
		if (query.trim()) {
			search();
		}
	});
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 bg-black/70 z-50 flex items-start justify-center pt-16 px-4"
	onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
	<div class="bg-gray-800 border border-gray-700 rounded-xl w-full max-w-3xl max-h-[80vh] flex flex-col shadow-2xl">
		<div class="p-4 border-b border-gray-700 flex items-center justify-between flex-shrink-0">
			<h2 class="text-lg font-semibold">Match to Metron</h2>
			<button onclick={onClose} class="text-gray-400 hover:text-gray-200 text-xl">&times;</button>
		</div>

		<div class="p-4 border-b border-gray-700 flex-shrink-0">
			<div class="flex gap-2">
				<input
					type="text"
					bind:value={query}
					placeholder="Search Metron series..."
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

		{#if conflict}
			<div class="px-4 pt-3 flex-shrink-0">
				<div class="bg-amber-900/20 border border-amber-700/60 rounded-lg p-3 space-y-2">
					<p class="text-sm text-amber-200">
						{conflict.message}
					</p>
					<p class="text-xs text-amber-300/80">
						Merging will move every issue and file from this series into
						<span class="font-semibold">{conflict.conflictingSeriesTitle}</span>
						(local series #{conflict.conflictingSeriesId}), then delete this
						duplicate series record. The merged series keeps its existing
						match, tracking, and read progress.
					</p>
					<div class="flex gap-2 pt-1">
						<button
							onclick={mergeIntoConflicting}
							disabled={merging}
							class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
						>
							{merging ? 'Merging…' : `Merge into ${conflict.conflictingSeriesTitle}`}
						</button>
						<button
							onclick={cancelConflict}
							disabled={merging}
							class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-200 rounded-lg transition-colors"
						>
							Cancel
						</button>
					</div>
				</div>
			</div>
		{/if}

		{#if error}
			<div class="px-4 pt-3 flex-shrink-0">
				<div class="bg-red-900/30 border border-red-700 rounded-lg p-3">
					<p class="text-sm text-red-400">{error}</p>
				</div>
			</div>
		{/if}

		<div class="flex-1 overflow-y-auto p-4 space-y-3">
			{#if searching}
				<div class="flex items-center justify-center py-10">
					<div class="text-gray-400">Searching Metron...</div>
				</div>
			{:else if results.length > 0}
				{#each results as result}
					<div class="flex gap-4 p-3 rounded-lg border border-gray-700 hover:border-gray-600 transition-colors"
						style="background-color: rgb(31 41 55);">
						<div class="flex-1 min-w-0">
							<h3 class="font-semibold text-gray-100 truncate">{result.display_name || result.name}</h3>
							<div class="flex flex-wrap items-center gap-2 mt-1 text-xs text-gray-400">
								{#if result.year_began}
									<span>{result.year_began}</span>
								{/if}
								<span>&middot;</span>
								<span>{result.issue_count} issues</span>
								{#if result.volume}
									<span>&middot;</span>
									<span>vol {result.volume}</span>
								{/if}
								<span>&middot;</span>
								<span class="text-gray-500">Metron #{result.metron_id}</span>
							</div>
						</div>
						<div class="flex-shrink-0 self-center">
							<button
								onclick={() => matchToSeries(result.metron_id)}
								disabled={matching}
								class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600
									disabled:bg-gray-600 disabled:cursor-not-allowed
									text-gray-900 font-semibold rounded-lg transition-colors"
							>
								{#if matching && matchingId === result.metron_id}
									Matching...
								{:else}
									Match
								{/if}
							</button>
						</div>
					</div>
				{/each}

				{#if total > 28}
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
							disabled={results.length < 28}
							class="px-3 py-1 text-sm bg-gray-700 rounded hover:bg-gray-600
								disabled:opacity-50 disabled:cursor-not-allowed"
						>
							Next
						</button>
					</div>
				{/if}
			{:else if hasSearched}
				<div class="flex items-center justify-center py-10 text-gray-400">
					<p>No results found.</p>
				</div>
			{/if}
		</div>
	</div>
</div>
