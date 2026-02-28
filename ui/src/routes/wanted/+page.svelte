<script lang="ts">
	import { ApiClient, type WantListItem, type WantListResponse, type Job } from '$lib/api/client';
	import SearchResultsModal from '$lib/components/SearchResultsModal.svelte';

	let items = $state<WantListItem[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Search modal state
	let searchOpen = $state(false);
	let searchIssueId = $state<number | null>(null);
	let searchIssueLabel = $state('');

	// Search All state
	let searchingAll = $state(false);
	let searchAllMessage = $state<string | null>(null);

	function openSearch(item: WantListItem) {
		searchIssueId = item.issue_id;
		searchIssueLabel = `${item.series_title} #${item.issue_number}`;
		searchOpen = true;
	}

	async function searchAll() {
		searchingAll = true;
		searchAllMessage = null;
		try {
			const job = await ApiClient.post<Job>('/search/pull-list');
			searchAllMessage = `Pull list search job started (Job #${job.id})`;
		} catch (e) {
			searchAllMessage = e instanceof Error ? e.message : 'Failed to start search';
		} finally {
			searchingAll = false;
		}
	}

	const priorityLabels = ['None', 'Low', 'Medium', 'High'];
	const priorityColors = [
		'bg-gray-700 text-gray-400',
		'bg-blue-900/50 text-blue-400',
		'bg-amber-900/50 text-amber-400',
		'bg-red-900/50 text-red-400'
	];

	async function loadWantList() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<WantListResponse>('/want-list?per_page=200');
			items = data.items || [];
			total = data.total;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load want list';
		} finally {
			loading = false;
		}
	}

	async function removeItem(id: number) {
		try {
			await ApiClient.delete(`/want-list/${id}`);
			items = items.filter(i => i.id !== id);
			total--;
		} catch (e) {
			console.error('Failed to remove item', e);
		}
	}

	async function updatePriority(item: WantListItem, priority: number) {
		try {
			await ApiClient.put(`/want-list/${item.id}`, { priority, notes: item.notes || '' });
			item.priority = priority;
			items = [...items];
		} catch (e) {
			console.error('Failed to update priority', e);
		}
	}

	// Group items by series
	let groupedItems = $derived(() => {
		const groups: Record<number, { seriesTitle: string; seriesId: number; items: WantListItem[] }> = {};
		for (const item of items) {
			if (!groups[item.series_id]) {
				groups[item.series_id] = {
					seriesTitle: item.series_title,
					seriesId: item.series_id,
					items: []
				};
			}
			groups[item.series_id].items.push(item);
		}
		return Object.values(groups).sort((a, b) => a.seriesTitle.localeCompare(b.seriesTitle));
	});

	$effect(() => {
		loadWantList();
	});
</script>

<div class="space-y-6">
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Wanted</h1>
			<p class="text-gray-400 mt-1">
				{#if total > 0}
					{total} missing issue{total !== 1 ? 's' : ''} across {groupedItems().length} series
				{:else}
					Issues you're looking for
				{/if}
			</p>
		</div>
		{#if items.length > 0}
			<button
				onclick={searchAll}
				disabled={searchingAll}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors
					flex items-center gap-2"
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
						d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
				</svg>
				{searchingAll ? 'Starting...' : 'Search All'}
			</button>
		{/if}
	</div>

	{#if searchAllMessage}
		<div class="bg-blue-900/30 border border-blue-700 rounded-lg p-3">
			<p class="text-sm text-blue-400">{searchAllMessage}</p>
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
	{:else if items.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
			</svg>
			<p class="text-lg font-medium">No wanted issues</p>
			<p class="text-sm mt-2">Track a series to automatically add missing issues here.</p>
			<a href="/library" class="mt-4 text-amber-400 hover:text-amber-300 text-sm">Browse Library &rarr;</a>
		</div>
	{:else}
		<div class="space-y-6">
			{#each groupedItems() as group (group.seriesId)}
				<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
					<div class="px-4 py-3 border-b border-gray-700 flex items-center justify-between">
						<a href="/library/{group.seriesId}" class="font-semibold text-amber-400 hover:text-amber-300 transition-colors">
							{group.seriesTitle}
						</a>
						<span class="text-xs text-gray-400">{group.items.length} issue{group.items.length !== 1 ? 's' : ''}</span>
					</div>
					<div class="divide-y divide-gray-700/50">
						{#each group.items as item (item.id)}
							<div class="px-4 py-3 flex items-center gap-4 hover:bg-gray-750 transition-colors">
								<!-- Cover thumbnail -->
								<div class="w-10 h-14 flex-shrink-0 bg-gray-700 rounded overflow-hidden">
									{#if item.cover_url}
										<img src={item.cover_url} alt="#{item.issue_number}" class="w-full h-full object-cover" loading="lazy" />
									{:else}
										<div class="w-full h-full flex items-center justify-center text-gray-500 text-xs">
											#{item.issue_number}
										</div>
									{/if}
								</div>

								<!-- Issue info -->
								<div class="flex-1 min-w-0">
									<p class="text-sm font-medium text-gray-200">
										Issue #{item.issue_number}
									</p>
									{#if item.store_date || item.cover_date}
										<p class="text-xs text-gray-500 mt-0.5">
											{item.store_date || item.cover_date}
										</p>
									{/if}
								</div>

								<!-- Priority selector -->
								<div class="flex gap-1">
									{#each [0, 1, 2, 3] as p}
										<button
											onclick={() => updatePriority(item, p)}
											class="text-xs px-2 py-0.5 rounded-full transition-colors
												{item.priority === p ? priorityColors[p] : 'bg-gray-700/50 text-gray-500 hover:bg-gray-600'}"
											title={priorityLabels[p]}
										>
											{priorityLabels[p]}
										</button>
									{/each}
								</div>

								<!-- Search button -->
								<button
									onclick={() => openSearch(item)}
									class="text-gray-500 hover:text-amber-400 transition-colors p-1"
									title="Search indexers for this issue"
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
											d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
									</svg>
								</button>

								<!-- Remove button -->
								<button
									onclick={() => removeItem(item.id)}
									class="text-gray-500 hover:text-red-400 transition-colors p-1"
									title="Remove from want list"
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
									</svg>
								</button>
							</div>
						{/each}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<SearchResultsModal
	bind:open={searchOpen}
	bind:issueId={searchIssueId}
	issueLabel={searchIssueLabel}
/>
