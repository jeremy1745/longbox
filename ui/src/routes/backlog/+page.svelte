<script lang="ts">
	import { onDestroy } from 'svelte';
	import {
		ApiClient,
		type BacklogRun,
		type BacklogItem,
		type BacklogRunListResponse,
		type BacklogItemListResponse
	} from '$lib/api/client';

	let runs = $state<BacklogRun[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);

	let expanded = $state<Record<number, boolean>>({});
	let items = $state<Record<number, BacklogItem[]>>({});
	let itemsLoading = $state<Record<number, boolean>>({});
	let itemsError = $state<Record<number, string | null>>({});
	let actingRunId = $state<number | null>(null);
	let retryingItemId = $state<number | null>(null);

	let eventSource: EventSource | null = null;
	let refreshTimer: ReturnType<typeof setTimeout> | null = null;

	type Activity = {
		stage: 'searching' | 'grabbing' | 'idle' | string;
		item_id?: number;
		run_id?: number;
		issue_id?: number;
		issue_number?: string;
		series_title?: string;
		message?: string;
		started_at: string;
	};
	let activity = $state<Activity | null>(null);
	let activityClearTimer: ReturnType<typeof setTimeout> | null = null;

	const statusColors: Record<string, string> = {
		planning: 'bg-blue-900/40 text-blue-300 border-blue-700/50',
		ready: 'bg-amber-900/40 text-amber-300 border-amber-700/50',
		attention: 'bg-red-900/40 text-red-300 border-red-700/50',
		paused: 'bg-gray-700 text-gray-300 border-gray-600',
		completed: 'bg-green-900/40 text-green-300 border-green-700/50'
	};

	const itemStatusColors: Record<string, string> = {
		pending: 'bg-gray-700 text-gray-300',
		searching: 'bg-blue-900/40 text-blue-300',
		queued: 'bg-amber-900/40 text-amber-300',
		downloading: 'bg-amber-900/40 text-amber-300',
		completed: 'bg-green-900/40 text-green-300',
		failed: 'bg-red-900/40 text-red-300',
		error: 'bg-red-900/60 text-red-200',
		canceled: 'bg-gray-700 text-gray-400'
	};

	let totals = $derived(
		runs.reduce(
			(acc, r) => {
				acc.total += r.total_issues;
				acc.queued += r.queued_issues;
				acc.completed += r.completed_issues;
				acc.failed += r.failed_issues;
				return acc;
			},
			{ total: 0, queued: 0, completed: 0, failed: 0 }
		)
	);

	async function loadRuns() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<BacklogRunListResponse>('/backlog/runs?per_page=200');
			runs = data.items || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load backlog runs';
		} finally {
			loading = false;
		}
	}

	async function loadItems(runId: number) {
		itemsLoading[runId] = true;
		itemsError[runId] = null;
		try {
			const data = await ApiClient.get<BacklogItemListResponse>(
				`/backlog/items?run_id=${runId}&per_page=500`
			);
			items[runId] = data.items || [];
		} catch (e) {
			itemsError[runId] = e instanceof Error ? e.message : 'Failed to load items';
		} finally {
			itemsLoading[runId] = false;
		}
	}

	async function toggleExpanded(run: BacklogRun) {
		const next = !expanded[run.id];
		expanded[run.id] = next;
		if (next && !items[run.id]) {
			await loadItems(run.id);
		}
	}

	async function pauseRun(run: BacklogRun) {
		actingRunId = run.id;
		try {
			const updated = await ApiClient.post<BacklogRun>(`/backlog/runs/${run.id}/pause`);
			upsertRun(updated);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to pause run';
		} finally {
			actingRunId = null;
		}
	}

	async function resumeRun(run: BacklogRun) {
		actingRunId = run.id;
		try {
			const updated = await ApiClient.post<BacklogRun>(`/backlog/runs/${run.id}/resume`);
			upsertRun(updated);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to resume run';
		} finally {
			actingRunId = null;
		}
	}

	async function retryAllInRun(run: BacklogRun) {
		actingRunId = run.id;
		try {
			const result = await ApiClient.post<{ retried: number; run: BacklogRun }>(
				`/backlog/runs/${run.id}/retry-all`
			);
			if (result.run) upsertRun(result.run);
			if (expanded[run.id]) await loadItems(run.id);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to retry all';
		} finally {
			actingRunId = null;
		}
	}

	async function retryItem(item: BacklogItem) {
		retryingItemId = item.id;
		try {
			await ApiClient.post(`/backlog/items/${item.id}/retry`);
			await loadItems(item.backlog_run_id);
		} catch (e) {
			itemsError[item.backlog_run_id] = e instanceof Error ? e.message : 'Failed to retry item';
		} finally {
			retryingItemId = null;
		}
	}

	function upsertRun(run: BacklogRun) {
		const idx = runs.findIndex((r) => r.id === run.id);
		if (idx >= 0) {
			runs = [...runs.slice(0, idx), run, ...runs.slice(idx + 1)];
		} else {
			runs = [run, ...runs];
		}
	}

	function upsertItem(item: BacklogItem) {
		const list = items[item.backlog_run_id];
		if (!list) return; // not loaded yet — nothing to update inline
		const idx = list.findIndex((i) => i.id === item.id);
		if (idx >= 0) {
			items[item.backlog_run_id] = [...list.slice(0, idx), item, ...list.slice(idx + 1)];
		} else {
			items[item.backlog_run_id] = [...list, item];
		}
	}

	function applyActivity(a: Activity) {
		activity = a;
		if (activityClearTimer) clearTimeout(activityClearTimer);
		// Idle pings auto-fade after a few seconds; active stages stay until
		// the next event lands.
		if (a.stage === 'idle') {
			activityClearTimer = setTimeout(() => {
				if (activity && activity.started_at === a.started_at) {
					activity = null;
				}
			}, 4000);
		}
	}

	function scheduleRefresh() {
		if (refreshTimer) return;
		refreshTimer = setTimeout(() => {
			refreshTimer = null;
			loadRuns();
			for (const idStr of Object.keys(expanded)) {
				const id = Number(idStr);
				if (expanded[id]) loadItems(id);
			}
		}, 400);
	}

	function connectSSE() {
		if (typeof window === 'undefined' || eventSource) return;
		const es = new EventSource('/api/v1/events');
		eventSource = es;
		es.onmessage = (event) => {
			try {
				const payload = JSON.parse(event.data);
				if (payload.type === 'backlog:run' && payload.data) {
					upsertRun(payload.data as BacklogRun);
					if (expanded[payload.data.id]) {
						scheduleRefresh();
					}
				} else if (payload.type === 'backlog:item' && payload.data) {
					upsertItem(payload.data as BacklogItem);
				} else if (payload.type === 'backlog:activity' && payload.data) {
					applyActivity(payload.data as Activity);
				}
			} catch {
				// ignore malformed events
			}
		};
		es.onerror = () => {
			es.close();
			eventSource = null;
			setTimeout(connectSSE, 2000);
		};
	}

	function formatDate(value?: string) {
		if (!value) return '';
		const d = new Date(value);
		if (isNaN(d.getTime())) return value;
		return d.toLocaleString();
	}

	$effect(() => {
		loadRuns();
		connectSSE();
	});

	onDestroy(() => {
		if (eventSource) {
			eventSource.close();
			eventSource = null;
		}
		if (refreshTimer) {
			clearTimeout(refreshTimer);
			refreshTimer = null;
		}
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Backlog</h1>
			<p class="text-gray-400 mt-1">
				{#if total > 0}
					{total} run{total !== 1 ? 's' : ''} —
					{totals.queued} active, {totals.completed} completed, {totals.failed} failed
				{:else}
					Auto-queued missing issues across tracked series
				{/if}
			</p>
		</div>
		<button
			onclick={loadRuns}
			class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg transition-colors"
		>
			Refresh
		</button>
	</div>

	{#if activity}
		<div
			class="rounded-lg border px-4 py-3 flex items-center gap-3 transition-colors {activity.stage === 'searching'
				? 'bg-blue-900/30 border-blue-700/60'
				: activity.stage === 'grabbing'
				? 'bg-amber-900/30 border-amber-700/60'
				: 'bg-gray-800 border-gray-700'}"
		>
			{#if activity.stage === 'searching' || activity.stage === 'grabbing'}
				<div class="w-3 h-3 rounded-full bg-current animate-pulse {activity.stage === 'searching' ? 'text-blue-400' : 'text-amber-400'}"></div>
			{:else}
				<div class="w-3 h-3 rounded-full bg-gray-500"></div>
			{/if}
			<div class="flex-1 min-w-0">
				<p class="text-sm">
					<span class="font-semibold capitalize {activity.stage === 'searching' ? 'text-blue-300' : activity.stage === 'grabbing' ? 'text-amber-300' : 'text-gray-300'}">
						{activity.stage}
					</span>
					{#if activity.series_title}
						<span class="text-gray-200">· {activity.series_title}</span>
					{/if}
					{#if activity.issue_number}
						<span class="text-gray-300">#{activity.issue_number}</span>
					{/if}
					{#if activity.message}
						<span class="text-gray-400">— {activity.message}</span>
					{/if}
				</p>
				<p class="text-xs text-gray-500 mt-0.5">{formatDate(activity.started_at)}</p>
			</div>
		</div>
	{/if}

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if runs.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
			</svg>
			<p class="text-lg font-medium">No backlog runs yet</p>
			<p class="text-sm mt-2">Open a series and click <span class="text-amber-400">Queue Missing</span> to start one.</p>
			<a href="/library" class="mt-4 text-amber-400 hover:text-amber-300 text-sm">Browse Library &rarr;</a>
		</div>
	{:else}
		<div class="space-y-4">
			{#each runs as run (run.id)}
				{@const status = run.paused ? 'paused' : run.status}
				<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
					<div class="px-4 py-3 border-b border-gray-700 flex items-center gap-4">
						<button
							onclick={() => toggleExpanded(run)}
							class="text-gray-400 hover:text-gray-200 transition-colors"
							aria-label={expanded[run.id] ? 'Collapse' : 'Expand'}
						>
							<svg
								class="w-4 h-4 transition-transform {expanded[run.id] ? 'rotate-90' : ''}"
								fill="none" stroke="currentColor" viewBox="0 0 24 24"
							>
								<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
							</svg>
						</button>
						<a
							href="/library/{run.series_id}"
							class="font-semibold text-amber-400 hover:text-amber-300 transition-colors flex-1 min-w-0 truncate"
						>
							{run.series_title || `Series #${run.series_id}`}
						</a>
						<span
							class="text-xs px-2 py-0.5 rounded-full border capitalize {statusColors[status] ?? 'bg-gray-700 text-gray-300 border-gray-600'}"
						>
							{status}
						</span>
						<div class="flex items-center gap-3 text-xs text-gray-400">
							<span title="Total issues in run">{run.total_issues} total</span>
							<span class="text-amber-400" title="Queued or in flight">{run.queued_issues} queued</span>
							<span class="text-green-400" title="Completed">{run.completed_issues} done</span>
							{#if run.failed_issues > 0}
								<span class="text-red-400" title="Failed">{run.failed_issues} failed</span>
							{/if}
						</div>
						<div class="flex items-center gap-2">
							{#if run.failed_issues > 0}
								<button
									onclick={() => retryAllInRun(run)}
									disabled={actingRunId === run.id}
									class="px-2.5 py-1 text-xs bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-md transition-colors"
									title="Reset every failed/errored item in this run back to pending"
								>
									{actingRunId === run.id ? '...' : `Retry All (${run.failed_issues})`}
								</button>
							{/if}
							{#if run.paused}
								<button
									onclick={() => resumeRun(run)}
									disabled={actingRunId === run.id}
									class="px-2.5 py-1 text-xs bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-md transition-colors"
								>
									{actingRunId === run.id ? '...' : 'Resume'}
								</button>
							{:else}
								<button
									onclick={() => pauseRun(run)}
									disabled={actingRunId === run.id}
									class="px-2.5 py-1 text-xs bg-gray-700 hover:bg-gray-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-200 rounded-md transition-colors"
								>
									{actingRunId === run.id ? '...' : 'Pause'}
								</button>
							{/if}
						</div>
					</div>

					{#if expanded[run.id]}
						<div class="bg-gray-900/40">
							{#if itemsLoading[run.id]}
								<div class="px-4 py-6 text-sm text-gray-400">Loading items…</div>
							{:else if itemsError[run.id]}
								<div class="px-4 py-3 text-sm text-red-400">{itemsError[run.id]}</div>
							{:else if !items[run.id] || items[run.id].length === 0}
								<div class="px-4 py-6 text-sm text-gray-500">No items in this run.</div>
							{:else}
								<table class="w-full text-sm">
									<thead class="text-left text-xs uppercase text-gray-500 bg-gray-800/40">
										<tr>
											<th class="px-4 py-2 font-medium">Issue</th>
											<th class="px-4 py-2 font-medium">Status</th>
											<th class="px-4 py-2 font-medium">Retries</th>
											<th class="px-4 py-2 font-medium">Last Error</th>
											<th class="px-4 py-2 font-medium">Updated</th>
											<th class="px-4 py-2 font-medium text-right">Actions</th>
										</tr>
									</thead>
									<tbody class="divide-y divide-gray-700/50">
										{#each items[run.id] as item (item.id)}
											<tr class="hover:bg-gray-800/40">
												<td class="px-4 py-2 text-gray-200 whitespace-nowrap">
													#{item.issue_number || item.issue_id}
													{#if item.variant_name}
														<span class="ml-1 text-xs text-gray-500">({item.variant_name})</span>
													{/if}
												</td>
												<td class="px-4 py-2">
													<span class="text-xs px-2 py-0.5 rounded-full capitalize {itemStatusColors[item.status] ?? 'bg-gray-700 text-gray-300'}">
														{item.status}
													</span>
												</td>
												<td class="px-4 py-2 text-gray-400">
													{item.retry_count}{#if item.retry_at} <span class="text-gray-500" title={item.retry_at}>· retry pending</span>{/if}
												</td>
												<td class="px-4 py-2 text-gray-400 max-w-xs truncate" title={item.last_error || ''}>
													{item.last_error || ''}
												</td>
												<td class="px-4 py-2 text-gray-500 whitespace-nowrap text-xs">
													{formatDate(item.updated_at)}
												</td>
												<td class="px-4 py-2 text-right">
													{#if item.status === 'failed' || item.status === 'error'}
														<button
															onclick={() => retryItem(item)}
															disabled={retryingItemId === item.id}
															class="px-2 py-1 text-xs bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed text-gray-900 font-semibold rounded-md transition-colors"
														>
															{retryingItemId === item.id ? '...' : 'Retry'}
														</button>
													{/if}
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							{/if}
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</div>
