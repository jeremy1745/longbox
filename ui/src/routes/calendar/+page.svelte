<script lang="ts">
	import { untrack } from 'svelte';
	import { ApiClient, type PullListIssue, type ReleasesResponse, type ReleaseDebugInfo, type CalendarRefreshResponse, type WantListItem, type WantTrackResult } from '$lib/api/client';
	import WantTrackButton from '$lib/components/WantTrackButton.svelte';

	let releases = $state<PullListIssue[]>([]);
	let debugInfo = $state<ReleaseDebugInfo | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let filterMode = $state<'all' | 'tracked'>('all');
	let refreshing = $state(false);
	let refreshMessage = $state<string | null>(null);

	// Want action state (Track is handled by the WantTrackButton component)
	let wantingInProgress = $state<Set<string>>(new Set());

	// View mode: 'week' = single week view, 'month' = full month view
	let viewMode = $state<'week' | 'month'>('week');

	// Current reference date for navigation
	let currentDate = $state(new Date());

	// --- Week mode helpers ---
	function getMonday(d: Date): Date {
		const result = new Date(d);
		const day = result.getDay();
		const diff = result.getDate() - day + (day === 0 ? -6 : 1);
		result.setDate(diff);
		result.setHours(0, 0, 0, 0);
		return result;
	}

	function getSunday(monday: Date): Date {
		const result = new Date(monday);
		result.setDate(monday.getDate() + 6);
		return result;
	}

	function formatDate(d: Date): string {
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
	}

	// Derived dates based on view mode
	let weekMonday = $derived(getMonday(currentDate));
	let weekSunday = $derived(getSunday(weekMonday));

	let year = $derived(currentDate.getFullYear());
	let month = $derived(currentDate.getMonth());
	let monthName = $derived(currentDate.toLocaleString('default', { month: 'long', year: 'numeric' }));

	let weekLabel = $derived.by(() => {
		const mon = weekMonday;
		const sun = weekSunday;
		const monStr = mon.toLocaleDateString('default', { month: 'short', day: 'numeric' });
		const sunStr = sun.toLocaleDateString('default', { month: 'short', day: 'numeric', year: 'numeric' });
		return `${monStr} – ${sunStr}`;
	});

	let startDate = $derived.by(() => {
		if (viewMode === 'week') {
			return formatDate(weekMonday);
		}
		return `${year}-${String(month + 1).padStart(2, '0')}-01`;
	});

	let endDate = $derived.by(() => {
		if (viewMode === 'week') {
			return formatDate(weekSunday);
		}
		const last = new Date(year, month + 1, 0);
		return `${year}-${String(month + 1).padStart(2, '0')}-${String(last.getDate()).padStart(2, '0')}`;
	});

	// Navigation
	function prevPeriod() {
		if (viewMode === 'week') {
			const d = new Date(weekMonday);
			d.setDate(d.getDate() - 7);
			currentDate = d;
		} else {
			currentDate = new Date(year, month - 1, 1);
		}
	}

	function nextPeriod() {
		if (viewMode === 'week') {
			const d = new Date(weekMonday);
			d.setDate(d.getDate() + 7);
			currentDate = d;
		} else {
			currentDate = new Date(year, month + 1, 1);
		}
	}

	function goToday() {
		currentDate = new Date();
	}

	let requestId = 0;

	async function loadReleases(start: string, end: string) {
		const thisRequest = ++requestId;
		loading = true;
		error = null;
		try {
			const params = `start=${start}&end=${end}`;
			const data = await ApiClient.get<ReleasesResponse>(`/calendar/releases?${params}`);
			// Only apply results if this is still the latest request (prevents race conditions)
			if (thisRequest !== requestId) return;
			releases = data.releases || [];
			debugInfo = data.debug || null;
		} catch (e) {
			if (thisRequest !== requestId) return;
			const msg = e instanceof Error ? e.message : 'Failed to load releases';
			if (msg.includes('API key')) {
				error = 'ComicVine API key not configured. Set it in Settings to see weekly releases.';
			} else {
				error = msg;
			}
			releases = [];
		} finally {
			if (thisRequest === requestId) {
				loading = false;
			}
		}
	}

	async function refreshTracked() {
		refreshing = true;
		refreshMessage = null;
		error = null;
		try {
			const data = await ApiClient.post<CalendarRefreshResponse>('/calendar/refresh');
			refreshMessage = `${data.message} (${data.matched_series} of ${data.tracked_series} series matched)`;
			pollForRefreshCompletion(data.job_id);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to refresh';
			refreshing = false;
		}
	}

	async function pollForRefreshCompletion(jobId: number) {
		const poll = async () => {
			try {
				const job = await ApiClient.get<{ id: number; status: string; message?: string }>(`/jobs/${jobId}`);
				if (job.status === 'completed') {
					refreshing = false;
					refreshMessage = 'Refresh complete! Reloading...';
					await loadReleases(startDate, endDate);
					refreshMessage = null;
				} else if (job.status === 'failed') {
					refreshing = false;
					error = job.message || 'Refresh job failed';
					refreshMessage = null;
				} else {
					if (job.message) refreshMessage = job.message;
					setTimeout(poll, 2000);
				}
			} catch {
				refreshing = false;
				refreshMessage = null;
			}
		};
		setTimeout(poll, 2000);
	}

	// Filtered releases based on filter mode
	let filteredReleases = $derived(
		filterMode === 'tracked'
			? releases.filter(r => r.tracked)
			: releases
	);

	// Helper: group issues by publisher, sorted by publisher name then alphabetical by series
	interface PublisherGroup {
		publisher: string;
		issues: PullListIssue[];
	}

	function groupByPublisher(issues: PullListIssue[]): PublisherGroup[] {
		const pubMap = new Map<string, PullListIssue[]>();
		for (const issue of issues) {
			const pub = issue.publisher || 'Other';
			if (!pubMap.has(pub)) pubMap.set(pub, []);
			pubMap.get(pub)!.push(issue);
		}

		// Sort each publisher's issues alphabetically by series name, then issue number
		for (const [, pubIssues] of pubMap) {
			pubIssues.sort((a, b) => {
				const nameCompare = a.series_name.localeCompare(b.series_name);
				if (nameCompare !== 0) return nameCompare;
				return (parseFloat(a.issue_number) || 0) - (parseFloat(b.issue_number) || 0);
			});
		}

		// Sort publisher groups alphabetically, with "Other" at the end
		return [...pubMap.entries()]
			.sort(([a], [b]) => {
				if (a === 'Other') return 1;
				if (b === 'Other') return -1;
				return a.localeCompare(b);
			})
			.map(([publisher, issues]) => ({ publisher, issues }));
	}

	// Group by publisher (flat — used for week view since all issues are in the same week)
	let publisherGroups = $derived.by(() => {
		return groupByPublisher(filteredReleases);
	});

	// Group by week then publisher (for month view)
	interface WeekPublisherGroup {
		weekLabel: string;
		publishers: PublisherGroup[];
		totalIssues: number;
	}

	let weekGroups = $derived.by(() => {
		const groups: WeekPublisherGroup[] = [];
		const weekMap = new Map<string, PullListIssue[]>();

		for (const issue of filteredReleases) {
			if (!issue.store_date) continue;
			const d = new Date(issue.store_date + 'T00:00:00');
			const monday = getMonday(d);
			const key = formatDate(monday);
			if (!weekMap.has(key)) weekMap.set(key, []);
			weekMap.get(key)!.push(issue);
		}

		const sortedWeeks = [...weekMap.entries()].sort(([a], [b]) => a.localeCompare(b));
		for (const [weekStart, weekIssues] of sortedWeeks) {
			const mon = new Date(weekStart + 'T00:00:00');
			const sun = getSunday(mon);
			const label = `${mon.toLocaleDateString('default', { month: 'short', day: 'numeric' })} – ${sun.toLocaleDateString('default', { month: 'short', day: 'numeric' })}`;
			groups.push({
				weekLabel: label,
				publishers: groupByPublisher(weekIssues),
				totalIssues: weekIssues.length
			});
		}
		return groups;
	});

	function isToday(dateStr: string): boolean {
		return dateStr === formatDate(new Date());
	}

	function isPast(dateStr: string): boolean {
		return dateStr < formatDate(new Date());
	}

	function isThisWeek(): boolean {
		const todayMon = getMonday(new Date());
		return formatDate(todayMon) === formatDate(weekMonday);
	}

	// Stats
	let trackedCount = $derived(filteredReleases.filter(r => r.tracked).length);
	let ownedCount = $derived(filteredReleases.filter(r => r.has_file).length);
	let wantedCount = $derived(filteredReleases.filter(r => r.wanted && !r.has_file).length);

	// Called by WantTrackButton's onTracked: mark every release from this series
	// as tracked (and link the new local series id) so the row badges update.
	function markSeriesTracked(issue: PullListIssue, result: WantTrackResult) {
		releases = releases.map(r =>
			r.series_cv_id === issue.series_cv_id
				? { ...r, tracked: true, local_series_id: result.series_id }
				: r
		);
	}

	// Unique key for want-in-progress tracking
	function wantKey(issue: PullListIssue): string {
		if (issue.comicvine_id) return `cv:${issue.comicvine_id}`;
		if (issue.local_issue_id) return `local:${issue.local_issue_id}`;
		if (issue.series_cv_id) return `series:${issue.series_cv_id}:${issue.issue_number}`;
		return '';
	}

	function canWant(issue: PullListIssue): boolean {
		return !issue.has_file && !issue.wanted && (!!issue.comicvine_id || !!issue.local_issue_id || !!issue.series_cv_id);
	}

	async function wantIssue(issue: PullListIssue) {
		const key = wantKey(issue);
		if (key === '' || wantingInProgress.has(key)) return;

		wantingInProgress = new Set([...wantingInProgress, key]);
		try {
			// Use local_issue_id if available (direct DB add), then ComicVine issue ID, then series + issue number
			const body = issue.local_issue_id
				? { local_issue_id: issue.local_issue_id }
				: issue.comicvine_id
					? { comicvine_id: issue.comicvine_id, series_cv_id: issue.series_cv_id }
					: { series_cv_id: issue.series_cv_id, issue_number: issue.issue_number };

			await ApiClient.post<WantListItem>('/calendar/want', body);

			// Mark this issue as wanted and the series as tracked
			releases = releases.map(r => {
				const isSameIssue = (issue.comicvine_id && r.comicvine_id === issue.comicvine_id)
					|| (issue.local_issue_id && r.local_issue_id === issue.local_issue_id)
					|| (issue.series_cv_id && r.series_cv_id === issue.series_cv_id && r.issue_number === issue.issue_number);
				if (isSameIssue) {
					return { ...r, tracked: true, wanted: true };
				}
				if (r.series_cv_id === issue.series_cv_id) {
					return { ...r, tracked: true };
				}
				return r;
			});
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to add to want list';
		} finally {
			wantingInProgress = new Set([...wantingInProgress].filter(id => id !== key));
		}
	}

	$effect(() => {
		// Read derived dates to establish reactive dependencies
		const start = startDate;
		const end = endDate;
		// untrack the async call so state writes inside loadReleases don't re-trigger this effect
		untrack(() => loadReleases(start, end));
	});
</script>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Pull List</h1>
			<p class="text-gray-400 mt-1">
				{#if viewMode === 'week'}
					All comics releasing this week
				{:else}
					All releases this month
				{/if}
			</p>
		</div>
		<div class="flex items-center gap-3">
			<!-- Refresh tracked -->
			<button
				onclick={refreshTracked}
				disabled={refreshing}
				class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 disabled:bg-gray-700/50
					disabled:cursor-not-allowed text-gray-200 rounded-lg transition-colors
					flex items-center gap-1.5"
				title="Refresh tracked series metadata from ComicVine"
			>
				<svg class="w-4 h-4 {refreshing ? 'animate-spin' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
						d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
				</svg>
				{refreshing ? 'Syncing...' : 'Sync Tracked'}
			</button>

			<!-- View toggle -->
			<div class="flex gap-1 border-l border-gray-700 pl-3">
				<button
					onclick={() => viewMode = 'week'}
					class="px-3 py-1.5 text-sm rounded-md transition-colors
						{viewMode === 'week' ? 'bg-gray-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
				>
					Week
				</button>
				<button
					onclick={() => viewMode = 'month'}
					class="px-3 py-1.5 text-sm rounded-md transition-colors
						{viewMode === 'month' ? 'bg-gray-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
				>
					Month
				</button>
			</div>

			<!-- Filter -->
			<div class="flex gap-1 border-l border-gray-700 pl-3">
				<button
					onclick={() => filterMode = 'all'}
					class="px-3 py-1.5 text-sm rounded-md transition-colors
						{filterMode === 'all' ? 'bg-amber-500/20 text-amber-400 border border-amber-500/50' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
				>
					All
				</button>
				<button
					onclick={() => filterMode = 'tracked'}
					class="px-3 py-1.5 text-sm rounded-md transition-colors
						{filterMode === 'tracked' ? 'bg-amber-500/20 text-amber-400 border border-amber-500/50' : 'bg-gray-700 text-gray-400 hover:bg-gray-600'}"
				>
					Tracked
				</button>
			</div>
		</div>
	</div>

	<!-- Refresh status -->
	{#if refreshMessage}
		<div class="bg-blue-900/30 border border-blue-700 rounded-lg px-4 py-3 flex items-center gap-3">
			{#if refreshing}
				<svg class="w-5 h-5 text-blue-400 animate-spin flex-shrink-0" fill="none" viewBox="0 0 24 24">
					<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
					<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
				</svg>
			{/if}
			<p class="text-sm text-blue-400">{refreshMessage}</p>
		</div>
	{/if}

	<!-- Navigation bar -->
	<div class="flex items-center gap-4 bg-gray-800 rounded-lg border border-gray-700 px-4 py-3">
		<button
			onclick={prevPeriod}
			class="p-1 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200 transition-colors"
			title={viewMode === 'week' ? 'Previous week' : 'Previous month'}
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
			</svg>
		</button>

		<h2 class="text-lg font-semibold flex-1 text-center">
			{#if viewMode === 'week'}
				{weekLabel}
				{#if isThisWeek()}
					<span class="ml-2 text-xs font-normal px-2 py-0.5 bg-amber-500/20 text-amber-400 rounded-full">This Week</span>
				{/if}
			{:else}
				{monthName}
			{/if}
		</h2>

		<button
			onclick={goToday}
			class="text-xs px-2 py-1 bg-gray-700 text-gray-300 rounded hover:bg-gray-600 transition-colors"
		>
			Today
		</button>

		<button
			onclick={nextPeriod}
			class="p-1 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200 transition-colors"
			title={viewMode === 'week' ? 'Next week' : 'Next month'}
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
			</svg>
		</button>
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-center">
				<div class="text-gray-400">Loading releases from ComicVine...</div>
				<div class="text-xs text-gray-600 mt-1">{startDate} to {endDate}</div>
			</div>
		</div>
	{:else if filteredReleases.length === 0}
		<div class="flex flex-col items-center justify-center py-16 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
			</svg>
			<p class="text-lg font-medium mb-2">
				{#if filterMode === 'tracked'}
					No tracked series releasing {viewMode === 'week' ? 'this week' : 'this month'}
				{:else}
					No releases found for {startDate} to {endDate}
				{/if}
			</p>
			<p class="text-sm">
				{#if filterMode === 'tracked'}
					Try switching to <button onclick={() => filterMode = 'all'} class="text-amber-400 hover:text-amber-300 underline">All</button> to see every comic releasing, or navigate to a different {viewMode === 'week' ? 'week' : 'month'}.
				{:else}
					ComicVine may not have data for this period yet. Try a different {viewMode === 'week' ? 'week' : 'month'}.
				{/if}
			</p>
			{#if debugInfo}
				<div class="text-xs text-gray-600 mt-4 font-mono space-y-1 text-left">
					<p>source={debugInfo.source} | walksoftly={debugInfo.walksoftly_count}{#if debugInfo.week_num} (week {debugInfo.week_num}){/if} | local={debugInfo.local_count} | tracked={debugInfo.tracked_count} | results={debugInfo.total_results}</p>
					{#if debugInfo.walksoftly_error}
						<p>walksoftly error: {debugInfo.walksoftly_error}</p>
					{/if}
					{#if debugInfo.cv_fallback_count}
						<p>CV fallback: {debugInfo.cv_fallback_count} issues</p>
					{/if}
				</div>
			{/if}
		</div>
	{:else}
		<!-- Summary stats -->
		<div class="flex gap-3 flex-wrap">
			<div class="flex items-center gap-2 px-3 py-1.5 bg-gray-800 rounded-lg border border-gray-700">
				<span class="text-sm text-gray-400">Releases:</span>
				<span class="text-sm font-semibold text-gray-200">{filteredReleases.length}</span>
			</div>
			{#if trackedCount > 0}
				<div class="flex items-center gap-2 px-3 py-1.5 bg-gray-800 rounded-lg border border-gray-700">
					<span class="text-sm text-gray-400">Tracked:</span>
					<span class="text-sm font-semibold text-amber-400">{trackedCount}</span>
				</div>
			{/if}
			{#if wantedCount > 0}
				<div class="flex items-center gap-2 px-3 py-1.5 bg-gray-800 rounded-lg border border-gray-700">
					<span class="text-sm text-gray-400">Wanted:</span>
					<span class="text-sm font-semibold text-blue-400">{wantedCount}</span>
				</div>
			{/if}
			{#if ownedCount > 0}
				<div class="flex items-center gap-2 px-3 py-1.5 bg-gray-800 rounded-lg border border-gray-700">
					<span class="text-sm text-gray-400">Owned:</span>
					<span class="text-sm font-semibold text-green-400">{ownedCount}</span>
				</div>
			{/if}
		</div>

		<!-- ==================== WEEK VIEW ==================== -->
		{#if viewMode === 'week'}
			<div class="space-y-4">
				{#each publisherGroups as group (group.publisher)}
					<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
						<div class="px-4 py-2.5 border-b border-gray-700 bg-gray-800/50 flex items-center justify-between">
							<h3 class="text-sm font-semibold text-gray-300">{group.publisher}</h3>
							<span class="text-xs text-gray-500">{group.issues.length} title{group.issues.length !== 1 ? 's' : ''}</span>
						</div>
						<div class="divide-y divide-gray-700/50">
							{#each group.issues as issue}
								{@render issueRow(issue)}
							{/each}
						</div>
					</div>
				{/each}
			</div>

		<!-- ==================== MONTH VIEW ==================== -->
		{:else}
			<div class="space-y-6">
				{#each weekGroups as week (week.weekLabel)}
					<div>
						<div class="flex items-center justify-between mb-2 px-1">
							<h3 class="text-sm font-semibold text-gray-300">{week.weekLabel}</h3>
							<span class="text-xs text-gray-500">{week.totalIssues} title{week.totalIssues !== 1 ? 's' : ''}</span>
						</div>
						<div class="space-y-3">
							{#each week.publishers as group (group.publisher)}
								<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
									<div class="px-4 py-2 border-b border-gray-700 bg-gray-800/50 flex items-center justify-between">
										<h4 class="text-xs font-semibold text-gray-400 uppercase tracking-wide">{group.publisher}</h4>
										<span class="text-xs text-gray-500">{group.issues.length}</span>
									</div>
									<div class="divide-y divide-gray-700/50">
										{#each group.issues as issue}
											{@render issueRowCompact(issue)}
										{/each}
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		{/if}

		<p class="text-center text-sm text-gray-500">
			{filteredReleases.length} release{filteredReleases.length !== 1 ? 's' : ''} {viewMode === 'week' ? 'this week' : 'this month'}
			{#if filterMode === 'tracked'} (tracked only){/if}
		</p>
	{/if}
</div>

{#snippet issueRow(issue: PullListIssue)}
	<div class="px-4 py-3 flex items-center gap-4 hover:bg-gray-750 transition-colors
		{issue.tracked ? '' : 'opacity-80'}">
		<!-- Issue info -->
		<div class="flex-1 min-w-0">
			<div class="flex items-center gap-2">
				{#if issue.local_series_id}
					<a href="/library/{issue.local_series_id}"
						class="text-sm font-medium text-gray-200 hover:text-amber-400 transition-colors truncate">
						{issue.series_name}
					</a>
				{:else}
					<span class="text-sm font-medium text-gray-200 truncate">{issue.series_name}</span>
				{/if}
				<span class="text-sm text-amber-400 font-semibold flex-shrink-0">#{issue.issue_number}</span>
				{#if issue.comicvine_url}
					<a href={issue.comicvine_url} target="_blank" rel="noopener noreferrer"
						class="flex-shrink-0 text-gray-500 hover:text-amber-400 transition-colors"
						title="View on ComicVine">
						<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
								d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
						</svg>
					</a>
				{/if}
			</div>
			{#if issue.title}
				<p class="text-xs text-gray-400 truncate mt-0.5">{issue.title}</p>
			{/if}
			{#if issue.writers}
				<p class="text-xs text-gray-500 truncate mt-0.5">{issue.writers}</p>
			{/if}
		</div>

		<!-- Action buttons -->
		<div class="flex items-center gap-0.5 flex-shrink-0">
			{#if !issue.tracked && issue.series_cv_id}
				<WantTrackButton
					variant="compact"
					comicvineId={issue.series_cv_id}
					sourceIssueId={issue.local_issue_id}
					onTracked={(result) => markSeriesTracked(issue, result)}
				/>
			{/if}
			{#if canWant(issue)}
				<button
					onclick={() => wantIssue(issue)}
					disabled={wantingInProgress.has(wantKey(issue))}
					class="p-1.5 rounded-md text-gray-500 hover:text-blue-400 hover:bg-blue-500/10
						disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
					title="Add to want list"
				>
					{#if wantingInProgress.has(wantKey(issue))}
						<svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
							<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
							<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
						</svg>
					{:else}
						<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
								d="M12 4v16m8-8H4" />
						</svg>
					{/if}
				</button>
			{/if}
		</div>

		<!-- Status badges -->
		<div class="flex items-center gap-1.5 flex-shrink-0">
			{#if issue.tracked}
				<span class="text-xs px-2 py-0.5 rounded-full bg-amber-900/30 text-amber-400 border border-amber-500/30">Tracked</span>
			{/if}
			{#if issue.wanted && !issue.has_file}
				<span class="text-xs px-2 py-0.5 rounded-full bg-blue-900/50 text-blue-400 border border-blue-500/30">Wanted</span>
			{/if}
			{#if issue.has_file}
				<span class="text-xs px-2 py-0.5 rounded-full bg-green-900/50 text-green-400">Owned</span>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet issueRowCompact(issue: PullListIssue)}
	<div class="px-4 py-3 flex items-center gap-4 hover:bg-gray-750 transition-colors
		{issue.tracked ? '' : 'opacity-80'}">
		<!-- Date badge -->
		<div class="w-12 text-center flex-shrink-0">
			<p class="text-xs font-medium
				{issue.store_date && isToday(issue.store_date) ? 'text-amber-400' :
				 issue.store_date && isPast(issue.store_date) ? 'text-gray-500' :
				 'text-gray-300'}">
				{#if issue.store_date}
					{new Date(issue.store_date + 'T00:00:00').toLocaleDateString('default', { month: 'short', day: 'numeric' })}
				{/if}
			</p>
		</div>

		<!-- Issue info -->
		<div class="flex-1 min-w-0">
			<div class="flex items-center gap-2">
				{#if issue.local_series_id}
					<a href="/library/{issue.local_series_id}"
						class="text-sm font-medium text-gray-200 hover:text-amber-400 transition-colors truncate">
						{issue.series_name}
					</a>
				{:else}
					<span class="text-sm font-medium text-gray-200 truncate">{issue.series_name}</span>
				{/if}
				<span class="text-sm text-gray-400 flex-shrink-0">#{issue.issue_number}</span>
				{#if issue.comicvine_url}
					<a href={issue.comicvine_url} target="_blank" rel="noopener noreferrer"
						class="flex-shrink-0 text-gray-500 hover:text-amber-400 transition-colors"
						title="View on ComicVine">
						<svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
								d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
						</svg>
					</a>
				{/if}
			</div>
			{#if issue.title}
				<p class="text-xs text-gray-500 truncate mt-0.5">{issue.title}</p>
			{/if}
		</div>

		<!-- Action buttons -->
		<div class="flex items-center gap-0.5 flex-shrink-0">
			{#if !issue.tracked && issue.series_cv_id}
				<WantTrackButton
					variant="compact"
					comicvineId={issue.series_cv_id}
					sourceIssueId={issue.local_issue_id}
					onTracked={(result) => markSeriesTracked(issue, result)}
				/>
			{/if}
			{#if canWant(issue)}
				<button
					onclick={() => wantIssue(issue)}
					disabled={wantingInProgress.has(wantKey(issue))}
					class="p-1 rounded-md text-gray-500 hover:text-blue-400 hover:bg-blue-500/10
						disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
					title="Add to want list"
				>
					{#if wantingInProgress.has(wantKey(issue))}
						<svg class="w-3.5 h-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
							<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
							<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
						</svg>
					{:else}
						<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
								d="M12 4v16m8-8H4" />
						</svg>
					{/if}
				</button>
			{/if}
		</div>

		<!-- Status badges -->
		<div class="flex items-center gap-1.5 flex-shrink-0">
			{#if issue.tracked}
				<span class="text-xs px-2 py-0.5 rounded-full bg-amber-900/30 text-amber-400 border border-amber-500/30">Tracked</span>
			{/if}
			{#if issue.wanted && !issue.has_file}
				<span class="text-xs px-2 py-0.5 rounded-full bg-blue-900/50 text-blue-400 border border-blue-500/30">Wanted</span>
			{/if}
			{#if issue.has_file}
				<span class="text-xs px-2 py-0.5 rounded-full bg-green-900/50 text-green-400">Owned</span>
			{/if}
		</div>
	</div>
{/snippet}
