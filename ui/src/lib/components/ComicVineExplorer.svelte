<script lang="ts">
	import {
		ApiClient,
		type MetadataSearchResult,
		type MetadataSearchResponse,
		type TrackFromPullListResponse,
		type ComicVineIssue,
		type VolumeIssuesResponse,
		type Series
	} from '$lib/api/client';

	let {
		onClose,
		onTracked = () => {}
	}: {
		onClose: () => void;
		onTracked?: (series: Series) => void;
	} = $props();

	let query = $state('');
	let results = $state<MetadataSearchResult[]>([]);
	let total = $state(0);
	let page = $state(1);
	let searching = $state(false);
	let error = $state<string | null>(null);
	let info = $state<string | null>(null);
	let hasSearched = $state(false);
	let trackingId = $state<number | null>(null);
	let issuesPreview = $state<Record<number, ComicVineIssue[]>>({});
	let previewLoading = $state<Record<number, boolean>>({});
	let expanded = $state<number | null>(null);
	let trackedCvIds = $state<number[]>([]);
	let trackedSet = $derived(new Set(trackedCvIds));

	function sortResults(list: MetadataSearchResult[]): MetadataSearchResult[] {
		return [...list].sort((a, b) => {
			const ay = parseInt(a.start_year || '0', 10);
			const by = parseInt(b.start_year || '0', 10);
			if (by === ay) {
				return a.name.localeCompare(b.name);
			}
			return by - ay;
		});
	}

	async function search() {
		if (!query.trim()) return;
		searching = true;
		error = null;
		info = null;
		hasSearched = true;
		try {
			const data = await ApiClient.get<MetadataSearchResponse>(
				`/metadata/search?q=${encodeURIComponent(query.trim())}&page=${page}`
			);
			results = sortResults(data.results || []);
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Search failed';
			results = [];
			total = 0;
		} finally {
			searching = false;
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

	function isTracked(cvId: number) {
		return trackedSet.has(cvId);
	}

	async function trackSeries(cvId: number) {
		trackingId = cvId;
		error = null;
		info = null;
		try {
			const data = await ApiClient.post<TrackFromPullListResponse>('/calendar/track', {
				series_cv_id: cvId,
				want_all: true
			});
			info = `${data.series.title} tracked (${data.want_list_added} wanted issues)`;
			trackedCvIds = [...new Set([...trackedCvIds, cvId])];
			onTracked?.(data.series);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to track series';
		} finally {
			trackingId = null;
		}
	}

	async function togglePreview(cvId: number) {
		if (expanded === cvId) {
			expanded = null;
			return;
		}
		expanded = cvId;
		if (!issuesPreview[cvId]) {
			previewLoading = { ...previewLoading, [cvId]: true };
			try {
				const data = await ApiClient.get<VolumeIssuesResponse>(`/metadata/volume/${cvId}/issues`);
				const list = (data.issues || []).slice(0, 12);
				issuesPreview = { ...issuesPreview, [cvId]: list };
			} catch (e) {
				error = e instanceof Error ? e.message : 'Failed to load issues';
			} finally {
				previewLoading = { ...previewLoading, [cvId]: false };
			}
		}
	}

	function formatStoreDate(date?: string) {
		if (!date) return 'TBD';
		const parsed = new Date(date);
		if (Number.isNaN(parsed.valueOf())) return date;
		return parsed.toLocaleDateString();
	}
</script>

<div class="fixed inset-0 bg-black/70 z-50 flex items-start justify-center pt-16 px-4"
	onclick={(e) => {
		if (e.target === e.currentTarget) onClose();
	}}>
	<div class="bg-gray-800 border border-gray-700 rounded-2xl w-full max-w-5xl max-h-[85vh] flex flex-col shadow-2xl overflow-hidden">
		<div class="p-4 border-b border-gray-700 flex items-center justify-between">
			<div>
				<h2 class="text-xl font-semibold">Browse ComicVine</h2>
				<p class="text-gray-400 text-sm">Search any series, see every relaunch by start year, and track it instantly.</p>
			</div>
			<button onclick={onClose} class="text-gray-400 hover:text-gray-100 text-2xl leading-none">&times;</button>
		</div>

		<div class="p-4 border-b border-gray-700 flex gap-2">
			<input
				type="text"
				bind:value={query}
				placeholder="Search ComicVine (e.g., X-Men, Detective Comics)"
				class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-100 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500"
				onkeydown={(e) => {
					if (e.key === 'Enter') {
						page = 1;
						search();
					}
				}}
			/>
			<button
				onclick={() => {
					page = 1;
					search();
				}}
				disabled={searching || !query.trim()}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg"
			>
				{searching ? 'Searching…' : 'Search'}
			</button>
		</div>

		{#if error}
			<div class="px-4 pt-3">
				<div class="bg-red-900/30 border border-red-700 rounded-lg p-3 text-sm text-red-300">{error}</div>
			</div>
		{/if}
		{#if info}
			<div class="px-4 pt-3">
				<div class="bg-green-900/30 border border-green-700 rounded-lg p-3 text-sm text-green-300">{info}</div>
			</div>
		{/if}

		<div class="flex-1 overflow-y-auto p-4 space-y-3">
			{#if searching}
				<div class="flex items-center justify-center py-10 text-gray-400">Searching ComicVine…</div>
			{:else if results.length > 0}
				{#each results as result}
					<div class="bg-gray-750 border border-gray-700 rounded-xl p-4 space-y-3" style="background-color: rgb(31 41 55);">
						<div class="flex flex-wrap items-center gap-3">
							<div class="min-w-[140px]">
								<p class="text-xs uppercase tracking-wide text-gray-500">Series</p>
								<p class="text-base font-semibold text-gray-100">{result.name}</p>
							</div>
							<div class="flex-1 flex flex-wrap gap-4 text-sm text-gray-300">
								<span><span class="text-gray-500">Start:</span> {result.start_year || 'Unknown'}</span>
								<span><span class="text-gray-500">Issues:</span> {result.issue_count}</span>
								{#if result.publisher}
									<span><span class="text-gray-500">Publisher:</span> {result.publisher}</span>
								{/if}
								{#if isTracked(result.comicvine_id)}
									<span class="px-2 py-0.5 rounded-full bg-emerald-900/50 border border-emerald-700 text-emerald-200 text-xs">Already in library</span>
								{/if}
							</div>
							<div class="flex gap-2">
								<button
									class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 rounded-lg"
									onclick={() => togglePreview(result.comicvine_id)}
								>
									{expanded === result.comicvine_id ? 'Hide issues' : 'Preview issues'}
								</button>
								<button
									disabled={trackingId === result.comicvine_id || isTracked(result.comicvine_id)}
									onclick={() => trackSeries(result.comicvine_id)}
									class="px-3 py-1.5 text-sm bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg"
								>
									{trackingId === result.comicvine_id ? 'Tracking…' : isTracked(result.comicvine_id) ? 'Tracked' : 'Track series'}
								</button>
							</div>
						</div>
						{#if result.description}
							<p class="text-sm text-gray-400 leading-relaxed">{result.description}</p>
						{/if}

						{#if expanded === result.comicvine_id}
							<div class="mt-3 border-t border-gray-700 pt-3">
								{#if previewLoading[result.comicvine_id]}
									<p class="text-sm text-gray-400">Loading issue list…</p>
								{:else if issuesPreview[result.comicvine_id]?.length}
									<p class="text-xs uppercase tracking-wide text-gray-500 mb-2">First {issuesPreview[result.comicvine_id].length} issues</p>
									<div class="grid grid-cols-1 md:grid-cols-2 gap-2">
										{#each issuesPreview[result.comicvine_id] as issue}
											<div class="p-2 rounded bg-gray-750/70 border border-gray-700">
												<p class="text-sm text-gray-100 font-semibold">#{issue.issue_number} {issue.title ? `– ${issue.title}` : ''}</p>
												<p class="text-xs text-gray-400">Store: {formatStoreDate(issue.store_date)}</p>
											</div>
										{/each}
									</div>
								{:else}
									<p class="text-sm text-gray-400">No preview data available.</p>
								{/if}
							</div>
						{/if}
					</div>
				{/each}

				{#if total > results.length}
					<div class="flex items-center justify-center gap-3 pt-4">
						<button
							onclick={prevPage}
							disabled={page <= 1}
							class="px-3 py-1 text-sm bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
						>
							Previous
						</button>
						<span class="text-sm text-gray-400">Page {page}</span>
						<button
							onclick={nextPage}
							disabled={results.length === 0}
							class="px-3 py-1 text-sm bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
						>
							Next
						</button>
					</div>
				{/if}
			{:else if hasSearched}
				<div class="flex items-center justify-center py-12 text-gray-400">No series found. Try another name.</div>
			{:else}
				<div class="flex items-center justify-center py-12 text-gray-500 text-sm">
					Enter a title to see every volume ComicVine knows about.
				</div>
			{/if}
		</div>
	</div>
</div>
