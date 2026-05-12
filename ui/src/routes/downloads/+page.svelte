<script lang="ts">
	import { ApiClient, type DownloadHistoryItem, type DownloadHistoryResponse, type BlocklistEntry, type BlocklistResponse } from '$lib/api/client';

	let items = $state<DownloadHistoryItem[]>([]);
	let total = $state(0);
	let page = $state(1);
	let perPage = $state(50);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Blocklist state
	let activeTab = $state<'history' | 'blocklist'>('history');
	let blocklistItems = $state<BlocklistEntry[]>([]);
	let blocklistTotal = $state(0);
	let blocklistLoading = $state(false);
	let blocklistError = $state<string | null>(null);

	async function loadHistory() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<DownloadHistoryResponse>(`/downloads?page=${page}&per_page=${perPage}`);
			items = data.items || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load download history';
		} finally {
			loading = false;
		}
	}

	function statusBadge(status: string): { label: string; classes: string } {
		switch (status) {
			case 'grabbed':
				return { label: 'Grabbed', classes: 'bg-blue-900/50 text-blue-400' };
			case 'downloading':
				return { label: 'Downloading', classes: 'bg-amber-900/50 text-amber-400' };
			case 'completed':
				return { label: 'Completed', classes: 'bg-green-900/50 text-green-400' };
			case 'failed':
				return { label: 'Failed', classes: 'bg-red-900/50 text-red-400' };
			case 'import_failed':
				return { label: 'Import Failed', classes: 'bg-red-900/50 text-red-400' };
			default:
				return { label: status, classes: 'bg-gray-700 text-gray-400' };
		}
	}

	function formatSize(bytes: number): string {
		if (bytes === 0) return '-';
		const units = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
	}

	function formatDate(dateStr: string): string {
		if (!dateStr) return '-';
		const d = new Date(dateStr);
		return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' });
	}

	let totalPages = $derived(Math.ceil(total / perPage));

	async function loadBlocklist() {
		blocklistLoading = true;
		blocklistError = null;
		try {
			const data = await ApiClient.get<BlocklistResponse>('/search/blocklist?page=1&per_page=100');
			blocklistItems = data.items || [];
			blocklistTotal = data.total;
		} catch (e) {
			blocklistError = e instanceof Error ? e.message : 'Failed to load blocklist';
		} finally {
			blocklistLoading = false;
		}
	}

	async function removeBlocklistEntry(id: number) {
		try {
			await ApiClient.delete(`/search/blocklist/${id}`);
			blocklistItems = blocklistItems.filter(b => b.id !== id);
			blocklistTotal--;
		} catch (e) {
			blocklistError = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function clearBlocklist() {
		if (!confirm('Clear entire blocklist?')) return;
		try {
			await ApiClient.delete('/search/blocklist');
			blocklistItems = [];
			blocklistTotal = 0;
		} catch (e) {
			blocklistError = e instanceof Error ? e.message : 'Clear failed';
		}
	}

	$effect(() => {
		if (activeTab === 'history') {
			loadHistory();
		} else {
			loadBlocklist();
		}
	});
</script>

<div class="space-y-6">
	<div>
		<h1 class="text-3xl font-bold">Downloads</h1>
		<p class="text-gray-400 mt-1">
			{#if total > 0}
				{total} download{total !== 1 ? 's' : ''} in history
			{:else}
				Download history from Usenet grabs
			{/if}
		</p>
	</div>

	<!-- Tabs -->
	<div class="flex gap-1 border-b border-gray-700">
		<button
			onclick={() => activeTab = 'history'}
			class="px-4 py-2 text-sm font-medium transition-colors border-b-2
				{activeTab === 'history' ? 'border-amber-500 text-amber-400' : 'border-transparent text-gray-400 hover:text-gray-300'}"
		>
			History
		</button>
		<button
			onclick={() => activeTab = 'blocklist'}
			class="px-4 py-2 text-sm font-medium transition-colors border-b-2
				{activeTab === 'blocklist' ? 'border-amber-500 text-amber-400' : 'border-transparent text-gray-400 hover:text-gray-300'}"
		>
			Blocklist{#if blocklistTotal > 0} ({blocklistTotal}){/if}
		</button>
	</div>

{#if activeTab === 'blocklist'}
	<!-- Blocklist View -->
	{#if blocklistError}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{blocklistError}</p>
		</div>
	{/if}

	{#if blocklistLoading}
		<div class="text-gray-400 py-8 text-center">Loading...</div>
	{:else if blocklistItems.length === 0}
		<div class="text-center py-12 text-gray-400">
			<p class="text-lg">Blocklist is empty</p>
			<p class="text-sm mt-2">Failed downloads are automatically added here to prevent re-grabbing.</p>
		</div>
	{:else}
		<div class="flex justify-end">
			<button
				onclick={clearBlocklist}
				class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-700 text-white rounded-lg transition-colors"
			>
				Clear All
			</button>
		</div>
		<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
			<table class="w-full text-sm">
				<thead>
					<tr class="text-left text-gray-400 border-b border-gray-700">
						<th class="px-4 py-3">NZB Name</th>
						<th class="px-4 py-3">Reason</th>
						<th class="px-4 py-3 text-right">Blocked</th>
						<th class="px-4 py-3 w-16"></th>
					</tr>
				</thead>
				<tbody class="divide-y divide-gray-700/50">
					{#each blocklistItems as entry (entry.id)}
						<tr class="hover:bg-gray-750 transition-colors">
							<td class="px-4 py-3">
								<span class="text-gray-200 block truncate max-w-md" title={entry.nzb_name}>{entry.nzb_name}</span>
							</td>
							<td class="px-4 py-3 text-gray-400">{entry.reason || '-'}</td>
							<td class="px-4 py-3 text-right text-gray-400 whitespace-nowrap">{formatDate(entry.blocked_at)}</td>
							<td class="px-4 py-3 text-right">
								<button
									onclick={() => removeBlocklistEntry(entry.id)}
									class="text-gray-500 hover:text-red-400 transition-colors"
									title="Remove from blocklist"
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
									</svg>
								</button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
{:else}
	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if items.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M9 19l3 3m0 0l3-3m-3 3V10" />
			</svg>
			<p class="text-lg font-medium">No downloads yet</p>
			<p class="text-sm mt-2">Grab NZBs from search results to see them here.</p>
		</div>
	{:else}
		<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
			<table class="w-full text-sm">
				<thead>
					<tr class="text-left text-gray-400 border-b border-gray-700">
						<th class="px-4 py-3">NZB</th>
						<th class="px-4 py-3">Issue</th>
						<th class="px-4 py-3">Indexer</th>
						<th class="px-4 py-3 text-center">Status</th>
						<th class="px-4 py-3 text-right">Size</th>
						<th class="px-4 py-3 text-right">Grabbed</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-gray-700/50">
					{#each items as item (item.id)}
						{@const badge = statusBadge(item.status)}
						<tr class="hover:bg-gray-750 transition-colors">
							<td class="px-4 py-3">
								<span class="text-gray-200 block truncate max-w-xs" title={item.nzb_name}>
									{item.nzb_name}
								</span>
							</td>
							<td class="px-4 py-3 text-gray-400">
								{#if item.series_title && item.issue_number}
									<a href="/library/{item.issue_id}" class="text-amber-400 hover:text-amber-300 transition-colors">
										{item.series_title} #{item.issue_number}
									</a>
								{:else}
									<span class="text-gray-500">-</span>
								{/if}
							</td>
							<td class="px-4 py-3 text-gray-400">{item.indexer_name || '-'}</td>
							<td class="px-4 py-3 text-center">
								<span class="inline-flex px-2 py-0.5 text-xs font-medium rounded-full {badge.classes}">
									{badge.label}
								</span>
							</td>
							<td class="px-4 py-3 text-right text-gray-400 whitespace-nowrap">{formatSize(item.size)}</td>
							<td class="px-4 py-3 text-right text-gray-400 whitespace-nowrap">{formatDate(item.grabbed_at)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
			<div class="flex items-center justify-center gap-2">
				<button
					onclick={() => { page = Math.max(1, page - 1); loadHistory(); }}
					disabled={page <= 1}
					class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 disabled:bg-gray-800 disabled:text-gray-600
						text-gray-300 rounded-lg border border-gray-700 transition-colors"
				>
					Previous
				</button>
				<span class="text-sm text-gray-400">Page {page} of {totalPages}</span>
				<button
					onclick={() => { page = Math.min(totalPages, page + 1); loadHistory(); }}
					disabled={page >= totalPages}
					class="px-3 py-1.5 text-sm bg-gray-800 hover:bg-gray-700 disabled:bg-gray-800 disabled:text-gray-600
						text-gray-300 rounded-lg border border-gray-700 transition-colors"
				>
					Next
				</button>
			</div>
		{/if}
	{/if}
{/if}
</div>
