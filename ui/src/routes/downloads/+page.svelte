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

	// Pipeline status panel
	type PipelineStatus = {
		cron: Record<string, string>;
		backlog_status?: Record<string, number>;
		recent_failures?: Array<{ id: number; series_title: string; issue_number: string; last_error: string; updated_at: string }>;
		indexers?: Array<{ id: number; name: string; enabled: boolean; type: string }>;
		download_clients?: Array<{ id: number; name: string; enabled: boolean; type: string; url: string }>;
		want_list_total?: number;
		server_time: string;
	};
	let pipeline = $state<PipelineStatus | null>(null);
	let pipelineLoading = $state(false);
	let pipelineError = $state<string | null>(null);
	let pipelineOpen = $state(true);

	async function loadPipeline() {
		pipelineLoading = true;
		pipelineError = null;
		try {
			pipeline = await ApiClient.get<PipelineStatus>('/admin/pipeline-status');
		} catch (e) {
			pipelineError = e instanceof Error ? e.message : 'Pipeline status failed';
		} finally {
			pipelineLoading = false;
		}
	}

	function timeAgo(iso: string): string {
		if (!iso) return 'never';
		const then = new Date(iso).getTime();
		if (isNaN(then)) return iso;
		const sec = Math.floor((Date.now() - then) / 1000);
		if (sec < 60) return `${sec}s ago`;
		if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
		if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
		return `${Math.floor(sec / 86400)}d ago`;
	}

	$effect(() => {
		loadPipeline();
	});

	// Test Search diagnostic
	type TestSearchIndexer = {
		indexer_id: number;
		indexer_name: string;
		indexer_type: string;
		indexer_url: string;
		categories_sent: string;
		enabled: boolean;
		result_count: number;
		error?: string;
		top_results?: Array<{ title: string; size: number; grabs: number; category: string; guid: string }>;
	};
	type TestSearchResponse = { query: string; indexers: TestSearchIndexer[] };
	let testQuery = $state('');
	let testCategories = $state('');
	let testRunning = $state(false);
	let testResult = $state<TestSearchResponse | null>(null);
	let testError = $state<string | null>(null);

	async function runTestSearch() {
		if (!testQuery.trim()) return;
		testRunning = true;
		testError = null;
		testResult = null;
		try {
			const params = new URLSearchParams({ q: testQuery.trim() });
			if (testCategories.trim()) params.set('cat', testCategories.trim());
			testResult = await ApiClient.get<TestSearchResponse>(`/admin/test-search?${params}`);
		} catch (e) {
			testError = e instanceof Error ? e.message : 'Test search failed';
		} finally {
			testRunning = false;
		}
	}
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

	<!-- Pipeline Status -->
	<div class="bg-gray-800 rounded-lg border border-gray-700">
		<button
			onclick={() => pipelineOpen = !pipelineOpen}
			class="w-full flex items-center justify-between px-5 py-3 text-left hover:bg-gray-750 transition-colors"
		>
			<div class="flex items-center gap-3">
				<h2 class="text-base font-semibold text-gray-200">Pipeline Status</h2>
				{#if pipeline}
					{@const cron = pipeline.cron || {}}
					{@const stale = !cron.missing_search_last_run || (Date.now() - new Date(cron.missing_search_last_run).getTime() > 24 * 3600 * 1000)}
					{@const off = cron.missing_search_enabled !== 'true'}
					{#if off}
						<span class="text-xs px-2 py-0.5 bg-red-900/40 text-red-300 rounded">Missing search OFF</span>
					{:else if stale}
						<span class="text-xs px-2 py-0.5 bg-amber-900/40 text-amber-300 rounded">Missing search stale</span>
					{:else}
						<span class="text-xs px-2 py-0.5 bg-green-900/40 text-green-300 rounded">Missing search OK</span>
					{/if}
				{/if}
			</div>
			<div class="flex items-center gap-2 text-xs text-gray-400">
				{#if pipelineLoading}
					Loading…
				{/if}
				<span>{pipelineOpen ? '▾' : '▸'}</span>
			</div>
		</button>

		{#if pipelineOpen}
			<div class="px-5 pb-5 pt-1 border-t border-gray-700/50 space-y-4 text-sm">
				{#if pipelineError}
					<p class="text-red-400">{pipelineError}</p>
				{:else if pipeline}
					{@const cron = pipeline.cron || {}}
					<div class="grid grid-cols-1 md:grid-cols-3 gap-4">
						<div class="bg-gray-900/50 rounded p-3">
							<div class="flex items-center justify-between">
								<span class="text-gray-400">Missing search cron</span>
								<span class="font-mono {cron.missing_search_enabled === 'true' ? 'text-green-400' : 'text-red-400'}">
									{cron.missing_search_enabled === 'true' ? 'enabled' : 'disabled'}
								</span>
							</div>
							<div class="text-xs text-gray-500 mt-1">
								Interval: {cron.missing_search_interval || '10'} min ·
								Last run: {timeAgo(cron.missing_search_last_run)}
							</div>
						</div>
						<div class="bg-gray-900/50 rounded p-3">
							<div class="flex items-center justify-between">
								<span class="text-gray-400">Pull list cron</span>
								<span class="font-mono {cron.pull_list_enabled === 'true' ? 'text-green-400' : 'text-red-400'}">
									{cron.pull_list_enabled === 'true' ? 'enabled' : 'disabled'}
								</span>
							</div>
							<div class="text-xs text-gray-500 mt-1">
								Day: {cron.pull_list_day ?? '3'} (0=Sun) ·
								Hour: {cron.pull_list_hour ?? '6'} ·
								Last run: {cron.pull_list_last_run || 'never'}
							</div>
						</div>
						<div class="bg-gray-900/50 rounded p-3">
							<div class="flex items-center justify-between">
								<span class="text-gray-400">Auto scan cron</span>
								<span class="font-mono {cron.auto_scan_enabled === 'true' ? 'text-green-400' : 'text-red-400'}">
									{cron.auto_scan_enabled === 'true' ? 'enabled' : 'disabled'}
								</span>
							</div>
							<div class="text-xs text-gray-500 mt-1">
								Interval: {cron.auto_scan_interval || '?'} min ·
								Last run: {timeAgo(cron.auto_scan_last_run)}
							</div>
						</div>
					</div>

					<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
						<div class="bg-gray-900/50 rounded p-3">
							<div class="text-gray-400 mb-2">Backlog item statuses</div>
							{#if pipeline.backlog_status && Object.keys(pipeline.backlog_status).length > 0}
								<div class="flex flex-wrap gap-2">
									{#each Object.entries(pipeline.backlog_status) as [status, count]}
										<span class="text-xs px-2 py-0.5 rounded bg-gray-800 text-gray-200">
											{status}: <span class="font-mono">{count}</span>
										</span>
									{/each}
								</div>
							{:else}
								<div class="text-xs text-gray-500">No items</div>
							{/if}
							<div class="text-xs text-gray-500 mt-2">Want list total: {pipeline.want_list_total ?? '?'}</div>
						</div>

						<div class="bg-gray-900/50 rounded p-3">
							<div class="text-gray-400 mb-2">Indexers + download clients</div>
							{#if pipeline.indexers && pipeline.indexers.length > 0}
								<ul class="text-xs text-gray-300 space-y-0.5">
									{#each pipeline.indexers as ix}
										<li>
											<span class="{ix.enabled ? 'text-green-400' : 'text-red-400'}">●</span>
											{ix.name} <span class="text-gray-500">({ix.type})</span>
										</li>
									{/each}
								</ul>
							{:else}
								<div class="text-xs text-red-400">No indexers configured</div>
							{/if}
							<div class="border-t border-gray-700/50 mt-2 pt-2">
								{#if pipeline.download_clients && pipeline.download_clients.length > 0}
									<ul class="text-xs text-gray-300 space-y-0.5">
										{#each pipeline.download_clients as dc}
											<li>
												<span class="{dc.enabled ? 'text-green-400' : 'text-red-400'}">●</span>
												{dc.name} <span class="text-gray-500">({dc.type} · {dc.url})</span>
											</li>
										{/each}
									</ul>
								{:else}
									<div class="text-xs text-red-400">No download clients configured</div>
								{/if}
							</div>
						</div>
					</div>

					{#if pipeline.recent_failures && pipeline.recent_failures.length > 0}
						<div class="bg-gray-900/50 rounded p-3">
							<div class="text-gray-400 mb-2">Most recent backlog failures</div>
							<ul class="text-xs text-gray-300 space-y-1">
								{#each pipeline.recent_failures as f}
									<li class="flex gap-2">
										<span class="text-gray-500 whitespace-nowrap">{timeAgo(f.updated_at)}</span>
										<span class="text-amber-300">{f.series_title} #{f.issue_number}</span>
										<span class="text-gray-400">— {f.last_error || '(no error message)'}</span>
									</li>
								{/each}
							</ul>
						</div>
					{/if}

					<div class="flex justify-end">
						<button
							onclick={loadPipeline}
							disabled={pipelineLoading}
							class="text-xs px-3 py-1 bg-gray-700 hover:bg-gray-600 disabled:opacity-50 rounded text-gray-200"
						>
							{pipelineLoading ? 'Refreshing…' : 'Refresh'}
						</button>
					</div>

					<!-- Test Search -->
					<div class="bg-gray-900/50 rounded p-3">
						<div class="text-gray-400 mb-2">Test Search — paste the query LongBox would use and see what each indexer returns</div>
						<div class="flex gap-2">
							<input
								type="text"
								bind:value={testQuery}
								placeholder="e.g. Alice Never After 001"
								class="flex-1 px-3 py-1.5 bg-gray-800 border border-gray-700 rounded text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-amber-500"
								onkeydown={(e) => { if (e.key === 'Enter') runTestSearch(); }}
							/>
							<input
								type="text"
								bind:value={testCategories}
								placeholder="cat (optional, e.g. 7030)"
								class="w-44 px-3 py-1.5 bg-gray-800 border border-gray-700 rounded text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-amber-500"
							/>
							<button
								onclick={runTestSearch}
								disabled={testRunning || !testQuery.trim()}
								class="px-3 py-1.5 text-xs bg-amber-500 hover:bg-amber-600 disabled:bg-gray-700 disabled:opacity-50 text-gray-900 font-semibold rounded"
							>
								{testRunning ? 'Searching…' : 'Run'}
							</button>
						</div>
						{#if testError}
							<p class="mt-2 text-xs text-red-400">{testError}</p>
						{/if}
						{#if testResult}
							<div class="mt-3 space-y-3">
								{#each testResult.indexers as ix}
									<div class="border border-gray-700/60 rounded p-2">
										<div class="flex items-center justify-between text-xs">
											<div class="flex items-center gap-2">
												<span class="{ix.enabled ? 'text-green-400' : 'text-red-400'}">●</span>
												<span class="font-semibold text-gray-200">{ix.indexer_name}</span>
												<span class="text-gray-500">({ix.indexer_type})</span>
											</div>
											<div class="font-mono text-gray-400">
												{ix.result_count} hit{ix.result_count === 1 ? '' : 's'}
												{#if ix.categories_sent}· cats={ix.categories_sent}{/if}
											</div>
										</div>
										{#if ix.error}
											<p class="text-xs text-red-400 mt-1">{ix.error}</p>
										{:else if ix.top_results && ix.top_results.length > 0}
											<ul class="mt-1.5 text-xs text-gray-300 space-y-0.5 font-mono">
												{#each ix.top_results as r}
													<li class="truncate">
														<span class="text-gray-500">[{r.category || '-'}]</span>
														{r.title}
														<span class="text-gray-600">· {(r.size / (1024 * 1024)).toFixed(0)}MB · {r.grabs} grabs</span>
													</li>
												{/each}
											</ul>
										{:else}
											<p class="text-xs text-gray-500 mt-1">no hits</p>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{/if}
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
