<script lang="ts">
	import { ApiClient, type LibraryStats, type Job, type Series, type SeriesListResponse, type Issue } from '$lib/api/client';
	import SeriesCard from '$lib/components/SeriesCard.svelte';
	import { getJobState, setActiveJob, isScanJob } from '$lib/stores/jobs.svelte';
	import { proxiedCoverURL } from '$lib/cover';

	let stats = $state<LibraryStats | null>(null);
	let recentSeries = $state<Series[]>([]);
	let newThisWeek = $state<Issue[]>([]);
	let newThisWeekRange = $state<{ start: string; end: string } | null>(null);
	let newThisWeekLoading = $state(true);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let lastScanStatus = $state<string | null>(null);

	const jobState = getJobState();
	const scanInProgress = $derived(
		isScanJob(jobState.activeJob) &&
		jobState.activeJob?.status === 'running'
	);

	async function loadDashboard() {
		loading = true;
		error = null;
		try {
			const [statsData, seriesData] = await Promise.all([
				ApiClient.get<LibraryStats>('/library/stats'),
				ApiClient.get<SeriesListResponse>('/series?per_page=12&sort=created_at&order=desc')
			]);
			stats = statsData;
			recentSeries = seriesData.series || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load dashboard';
		} finally {
			loading = false;
		}
	}

	async function loadNewThisWeek() {
		newThisWeekLoading = true;
		try {
			const data = await ApiClient.get<{ issues: Issue[]; total: number; start_date: string; end_date: string }>(
				'/library/new-this-week'
			);
			newThisWeek = data.issues || [];
			newThisWeekRange = { start: data.start_date, end: data.end_date };
		} catch (e) {
			// Best effort — failure here shouldn't block the rest of the dashboard.
			console.error('Failed to load new this week', e);
			newThisWeek = [];
		} finally {
			newThisWeekLoading = false;
		}
	}

	function formatRange(start?: string, end?: string): string {
		if (!start || !end) return '';
		const s = new Date(start + 'T00:00:00');
		const e = new Date(end + 'T00:00:00');
		const fmt = (d: Date) => d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
		return `${fmt(s)} – ${fmt(e)}`;
	}

	async function triggerScan() {
		error = null;
		try {
			const job = await ApiClient.post<Job>('/library/scan');
			setActiveJob(job);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Scan failed';
		}
	}

	async function triggerReconcileCV() {
		error = null;
		try {
			const job = await ApiClient.post<Job>('/library/scan/reconcile-cv');
			setActiveJob(job);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Reconcile failed';
		}
	}

	$effect(() => {
		loadDashboard();
		loadNewThisWeek();
	});

	$effect(() => {
		const job = jobState.activeJob;
		if (isScanJob(job)) {
			if (
				(job.status === 'completed' || job.status === 'failed' || job.status === 'cancelled') &&
				job.status !== lastScanStatus
			) {
				loadDashboard();
				loadNewThisWeek();
			}
			lastScanStatus = job.status;
		} else {
			lastScanStatus = null;
		}
	});
</script>

<div class="space-y-8">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Dashboard</h1>
			<p class="text-gray-400 mt-1">Welcome to LongBox</p>
		</div>
	<div class="flex items-center gap-2">
		<button
			onclick={triggerReconcileCV}
			disabled={scanInProgress}
			class="px-4 py-2 bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700 disabled:opacity-50 disabled:cursor-not-allowed
				text-gray-100 font-medium rounded-lg transition-colors"
			title="Run a scan that ignores the per-series ComicVine TTL — refreshes every tracked series"
		>
			Reconcile CV
		</button>
		<button
			onclick={triggerScan}
			disabled={scanInProgress}
			class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:cursor-not-allowed
				text-gray-900 font-semibold rounded-lg transition-colors"
		>
			{#if scanInProgress}
				Scanning...
			{:else}
				Scan Library
			{/if}
		</button>
	</div>
	</div>

	<!-- Active Job Progress -->
	{#if isScanJob(jobState.activeJob) && jobState.activeJob?.status === 'running'}
		<div class="bg-gray-800 border border-gray-700 rounded-lg p-4">
			<div class="flex items-center justify-between mb-2">
				<h3 class="font-semibold text-amber-400">Library Scan</h3>
				<span class="text-sm text-gray-400">{jobState.activeJob.progress}%</span>
			</div>
			<div class="w-full bg-gray-700 rounded-full h-2">
				<div
					class="bg-amber-500 h-2 rounded-full transition-all duration-300"
					style="width: {jobState.activeJob.progress}%"
				></div>
			</div>
			{#if jobState.activeJob.message}
				<p class="text-xs text-gray-400 mt-2 truncate">{jobState.activeJob.message}</p>
			{/if}
		</div>
	{/if}

	<!-- Completed Job Result -->
	{#if isScanJob(jobState.activeJob) && jobState.activeJob?.status === 'completed'}
		<div class="bg-gray-800 border border-gray-700 rounded-lg p-4">
			<h3 class="font-semibold text-green-400">Scan Complete</h3>
			{#if jobState.activeJob.message}
				<p class="text-sm text-gray-300 mt-1">{jobState.activeJob.message}</p>
			{/if}
		</div>
	{/if}

	{#if isScanJob(jobState.activeJob) && jobState.activeJob?.status === 'failed'}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<h3 class="font-semibold text-red-400">Scan Failed</h3>
			{#if jobState.activeJob.message}
				<p class="text-sm text-red-300 mt-1">{jobState.activeJob.message}</p>
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

	<!-- New Issues This Week -->
	{#if !newThisWeekLoading && newThisWeek.length > 0}
		<div>
			<div class="flex items-end justify-between mb-4">
				<div>
					<h2 class="text-xl font-semibold">New Issues This Week</h2>
					<p class="text-xs text-gray-500 mt-0.5">
						Issues released {formatRange(newThisWeekRange?.start, newThisWeekRange?.end)} that you own
						{#if newThisWeek.length > 0}· {newThisWeek.length} issue{newThisWeek.length === 1 ? '' : 's'}{/if}
					</p>
				</div>
			</div>
			<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4">
				{#each newThisWeek as issue (issue.id)}
					<a
						href="/reader/{issue.id}"
						class="group block rounded-lg overflow-hidden bg-gray-800 shadow-lg hover:shadow-xl hover:ring-2 hover:ring-amber-400/50 transition-all"
					>
						<div class="relative aspect-[2/3] bg-gray-700 overflow-hidden">
							{#if issue.file_id}
								<img
									src="/api/v1/covers/file/{issue.file_id}"
									alt="{issue.series_title} #{issue.issue_number}"
									class="w-full h-full object-cover"
									loading="lazy"
								/>
							{:else if issue.cover_url}
								<img
									src={proxiedCoverURL(issue.cover_url)}
									alt="{issue.series_title} #{issue.issue_number}"
									class="w-full h-full object-cover"
									loading="lazy"
								/>
							{:else}
								<div class="w-full h-full flex items-center justify-center text-gray-500 text-xs">
									#{issue.issue_number}
								</div>
							{/if}
							{#if issue.read_status === 'read'}
								<div class="absolute top-2 right-2 bg-green-500 rounded-full px-2 py-0.5 text-[10px] font-bold text-gray-900">
									READ
								</div>
							{/if}
							<div class="absolute top-2 left-2 bg-gray-900/80 backdrop-blur-sm text-gray-100 text-xs font-bold px-2 py-0.5 rounded-full">
								#{issue.issue_number}
							</div>
						</div>
						<div class="p-3">
							<p class="text-sm text-gray-200 font-medium truncate" title={issue.series_title}>
								{issue.series_title}
							</p>
							<p class="text-xs text-gray-500 mt-0.5">
								{issue.store_date || issue.cover_date || ''}
							</p>
						</div>
					</a>
				{/each}
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
				<h2 class="text-xl font-semibold">Recently Added</h2>
				<a href="/library" class="text-amber-400 hover:text-amber-300 text-sm">View All &rarr;</a>
			</div>
			<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4">
				{#each recentSeries as series (series.id)}
					<SeriesCard {series} />
				{/each}
			</div>
		</div>
	{:else if !scanInProgress}
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
