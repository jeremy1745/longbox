<script lang="ts">
	import { ApiClient, type LibraryStats, type Job, type Series, type SeriesListResponse } from '$lib/api/client';
	import SeriesCard from '$lib/components/SeriesCard.svelte';

	let stats = $state<LibraryStats | null>(null);
	let recentSeries = $state<Series[]>([]);
	let loading = $state(true);
	let scanning = $state(false);
	let activeJob = $state<Job | null>(null);
	let error = $state<string | null>(null);
	let eventSource = $state<EventSource | null>(null);

	async function loadDashboard() {
		loading = true;
		error = null;
		try {
			const [statsData, seriesData] = await Promise.all([
				ApiClient.get<LibraryStats>('/library/stats'),
				ApiClient.get<SeriesListResponse>('/series?per_page=12&sort=updated_at&order=desc')
			]);
			stats = statsData;
			recentSeries = seriesData.series || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load dashboard';
		} finally {
			loading = false;
		}
	}

	async function triggerScan() {
		scanning = true;
		activeJob = null;
		error = null;
		try {
			activeJob = await ApiClient.post<Job>('/library/scan');
			connectSSE();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Scan failed';
			scanning = false;
		}
	}

	function connectSSE() {
		if (eventSource) {
			eventSource.close();
		}
		const es = new EventSource('/api/v1/events');
		eventSource = es;

		es.onmessage = (event) => {
			try {
				const data = JSON.parse(event.data);
				if (data.type === 'job:updated' && data.data) {
					const job = data.data as Job;
					if (activeJob && job.id === activeJob.id) {
						activeJob = job;
						if (job.status === 'completed' || job.status === 'failed' || job.status === 'cancelled') {
							scanning = false;
							es.close();
							eventSource = null;
							// Reload dashboard to show updated data
							loadDashboard();
						}
					}
				}
				if (data.type === 'files:added') {
					// New files detected by watcher — reload
					loadDashboard();
				}
			} catch {
				// ignore parse errors
			}
		};

		es.onerror = () => {
			// Reconnect will happen automatically
		};
	}

	$effect(() => {
		loadDashboard();
		return () => {
			if (eventSource) {
				eventSource.close();
			}
		};
	});
</script>

<div class="space-y-8">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Dashboard</h1>
			<p class="text-gray-400 mt-1">Welcome to LongBox</p>
		</div>
		<button
			onclick={triggerScan}
			disabled={scanning}
			class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed
				text-gray-900 font-semibold rounded-lg transition-colors"
		>
			{#if scanning}
				Scanning...
			{:else}
				Scan Library
			{/if}
		</button>
	</div>

	<!-- Active Job Progress -->
	{#if activeJob && scanning}
		<div class="bg-gray-800 border border-gray-700 rounded-lg p-4">
			<div class="flex items-center justify-between mb-2">
				<h3 class="font-semibold text-amber-400">
					{activeJob.type === 'scan' ? 'Library Scan' : 'Metadata Refresh'}
				</h3>
				<span class="text-sm text-gray-400">{activeJob.progress}%</span>
			</div>
			<div class="w-full bg-gray-700 rounded-full h-2">
				<div
					class="bg-amber-500 h-2 rounded-full transition-all duration-300"
					style="width: {activeJob.progress}%"
				></div>
			</div>
			{#if activeJob.message}
				<p class="text-xs text-gray-400 mt-2 truncate">{activeJob.message}</p>
			{/if}
		</div>
	{/if}

	<!-- Completed Job Result -->
	{#if activeJob && !scanning && activeJob.status === 'completed'}
		<div class="bg-gray-800 border border-gray-700 rounded-lg p-4">
			<h3 class="font-semibold text-green-400">Scan Complete</h3>
			{#if activeJob.message}
				<p class="text-sm text-gray-300 mt-1">{activeJob.message}</p>
			{/if}
		</div>
	{/if}

	{#if activeJob && !scanning && activeJob.status === 'failed'}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<h3 class="font-semibold text-red-400">Scan Failed</h3>
			{#if activeJob.message}
				<p class="text-sm text-red-300 mt-1">{activeJob.message}</p>
			{/if}
		</div>
	{/if}

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	<!-- Stats Cards -->
	{#if stats && (stats.total_series > 0 || stats.total_files > 0)}
		<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
			<div class="bg-gray-800 rounded-lg p-4 border border-gray-700">
				<p class="text-3xl font-bold text-amber-400">{stats.total_series}</p>
				<p class="text-sm text-gray-400 mt-1">Series</p>
			</div>
			<div class="bg-gray-800 rounded-lg p-4 border border-gray-700">
				<p class="text-3xl font-bold text-amber-400">{stats.total_files}</p>
				<p class="text-sm text-gray-400 mt-1">Comic Files</p>
			</div>
		</div>
	{/if}

	<!-- Recently Updated Series -->
	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if recentSeries.length > 0}
		<div>
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">Recently Updated</h2>
				<a href="/library" class="text-amber-400 hover:text-amber-300 text-sm">View All &rarr;</a>
			</div>
			<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4">
				{#each recentSeries as series (series.id)}
					<SeriesCard {series} />
				{/each}
			</div>
		</div>
	{:else if !scanning}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253" />
			</svg>
			<p class="text-lg font-medium">Your library is empty</p>
			<p class="text-sm mt-2">Click "Scan Library" to discover comics in your configured directory.</p>
		</div>
	{/if}
</div>
