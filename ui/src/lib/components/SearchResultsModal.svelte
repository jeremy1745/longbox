<script lang="ts">
	import { ApiClient, type SearchResult, type SearchResponse } from '$lib/api/client';

	let {
		issueId = $bindable<number | null>(null),
		issueLabel = '',
		open = $bindable(false),
	}: {
		issueId?: number | null;
		issueLabel?: string;
		open?: boolean;
	} = $props();

	let results = $state<SearchResult[]>([]);
	let loading = $state(false);
	let error = $state<string | null>(null);
	let grabbing = $state<string | null>(null);
	let grabMessage = $state<{ text: string; success: boolean } | null>(null);

	async function search() {
		if (!issueId) return;
		loading = true;
		error = null;
		results = [];
		grabMessage = null;
		try {
			const data = await ApiClient.get<SearchResponse>(`/search/issue/${issueId}`);
			results = data.results || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Search failed';
		} finally {
			loading = false;
		}
	}

	async function grab(result: SearchResult) {
		grabbing = result.guid;
		grabMessage = null;
		try {
			await ApiClient.post('/search/grab', {
				nzb_url: result.nzb_url,
				nzb_name: result.title,
				indexer_id: result.indexer_id,
				issue_id: issueId,
				size: result.size,
			});
			grabMessage = { text: `Sent "${result.title}" to download client`, success: true };
		} catch (e) {
			grabMessage = { text: e instanceof Error ? e.message : 'Grab failed', success: false };
		} finally {
			grabbing = null;
		}
	}

	function close() {
		open = false;
		issueId = null;
		results = [];
		error = null;
		grabMessage = null;
	}

	function formatSize(bytes: number): string {
		if (bytes === 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
	}

	function formatAge(dateStr: string): string {
		if (!dateStr) return '-';
		const date = new Date(dateStr);
		const now = new Date();
		const days = Math.floor((now.getTime() - date.getTime()) / (1000 * 60 * 60 * 24));
		if (days === 0) return 'Today';
		if (days === 1) return '1 day';
		if (days < 30) return `${days} days`;
		if (days < 365) return `${Math.floor(days / 30)} months`;
		return `${Math.floor(days / 365)} years`;
	}

	function scoreColor(score: number): string {
		if (score >= 80) return 'text-green-400';
		if (score >= 50) return 'text-amber-400';
		return 'text-red-400';
	}

	$effect(() => {
		if (open && issueId) {
			search();
		}
	});
</script>

{#if open}
	<!-- Backdrop -->
	<div
		class="fixed inset-0 bg-black/60 z-40"
		role="button"
		tabindex="-1"
		onclick={close}
		onkeydown={(e) => e.key === 'Escape' && close()}
	></div>

	<!-- Modal -->
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4">
		<div class="bg-gray-800 rounded-lg border border-gray-700 shadow-xl w-full max-w-4xl max-h-[80vh] flex flex-col">
			<!-- Header -->
			<div class="flex items-center justify-between px-6 py-4 border-b border-gray-700">
				<div>
					<h2 class="text-lg font-semibold">Search Results</h2>
					{#if issueLabel}
						<p class="text-sm text-gray-400 mt-0.5">{issueLabel}</p>
					{/if}
				</div>
				<button
					onclick={close}
					class="text-gray-400 hover:text-white transition-colors p-1"
				>
					<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
					</svg>
				</button>
			</div>

			<!-- Content -->
			<div class="flex-1 overflow-y-auto p-6">
				{#if grabMessage}
					<div class="mb-4 p-3 rounded-lg {grabMessage.success ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
						<p class="text-sm {grabMessage.success ? 'text-green-400' : 'text-red-400'}">{grabMessage.text}</p>
					</div>
				{/if}

				{#if loading}
					<div class="flex items-center justify-center py-12">
						<div class="text-gray-400">Searching indexers...</div>
					</div>
				{:else if error}
					<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
						<p class="text-red-400 text-sm">{error}</p>
					</div>
				{:else if results.length === 0}
					<div class="flex flex-col items-center justify-center py-12 text-gray-400">
						<svg class="w-12 h-12 mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
								d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
						</svg>
						<p class="font-medium">No results found</p>
						<p class="text-sm mt-1">Try searching manually with a different query.</p>
					</div>
				{:else}
					<table class="w-full text-sm">
						<thead>
							<tr class="text-left text-gray-400 border-b border-gray-700">
								<th class="pb-2 pr-4">Title</th>
								<th class="pb-2 pr-4">Indexer</th>
								<th class="pb-2 pr-4 text-right">Size</th>
								<th class="pb-2 pr-4 text-right">Age</th>
								<th class="pb-2 pr-4 text-right">Grabs</th>
								<th class="pb-2 pr-4 text-right">Score</th>
								<th class="pb-2 w-20"></th>
							</tr>
						</thead>
						<tbody class="divide-y divide-gray-700/50">
							{#each results as result (result.guid)}
								<tr class="hover:bg-gray-750 transition-colors">
									<td class="py-2.5 pr-4">
										<span class="text-gray-200 block truncate max-w-xs" title={result.title}>
											{result.title}
										</span>
									</td>
									<td class="py-2.5 pr-4 text-gray-400">{result.indexer_name}</td>
									<td class="py-2.5 pr-4 text-right text-gray-400 whitespace-nowrap">{formatSize(result.size)}</td>
									<td class="py-2.5 pr-4 text-right text-gray-400 whitespace-nowrap">{formatAge(result.publish_date)}</td>
									<td class="py-2.5 pr-4 text-right text-gray-400">{result.grabs || '-'}</td>
									<td class="py-2.5 pr-4 text-right font-medium {scoreColor(result.score)}">{result.score}</td>
									<td class="py-2.5">
										<button
											onclick={() => grab(result)}
											disabled={grabbing === result.guid}
											class="px-3 py-1 text-xs bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
												disabled:cursor-not-allowed text-gray-900 font-semibold rounded transition-colors"
										>
											{grabbing === result.guid ? '...' : 'Grab'}
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				{/if}
			</div>
		</div>
	</div>
{/if}
