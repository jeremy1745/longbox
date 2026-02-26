<script lang="ts">
	import { ApiClient, type Issue, type CalendarResponse } from '$lib/api/client';

	let issues = $state<Issue[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let trackedOnly = $state(true);

	// Current month navigation
	let currentDate = $state(new Date());
	let year = $derived(currentDate.getFullYear());
	let month = $derived(currentDate.getMonth());

	let monthName = $derived(currentDate.toLocaleString('default', { month: 'long', year: 'numeric' }));

	function startOfMonth(): string {
		return `${year}-${String(month + 1).padStart(2, '0')}-01`;
	}

	function endOfMonth(): string {
		const last = new Date(year, month + 1, 0);
		return `${year}-${String(month + 1).padStart(2, '0')}-${String(last.getDate()).padStart(2, '0')}`;
	}

	function prevMonth() {
		currentDate = new Date(year, month - 1, 1);
	}

	function nextMonth() {
		currentDate = new Date(year, month + 1, 1);
	}

	function goToday() {
		currentDate = new Date();
	}

	async function loadCalendar() {
		loading = true;
		error = null;
		try {
			const params = `start=${startOfMonth()}&end=${endOfMonth()}&tracked_only=${trackedOnly}`;
			const data = await ApiClient.get<CalendarResponse>(`/calendar?${params}`);
			issues = data.issues || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load calendar';
		} finally {
			loading = false;
		}
	}

	// Group issues by week
	let weekGroups = $derived(() => {
		const groups: { weekLabel: string; issues: Issue[] }[] = [];
		const weekMap = new Map<string, Issue[]>();

		for (const issue of issues) {
			if (!issue.store_date) continue;
			const d = new Date(issue.store_date + 'T00:00:00');
			// Get Monday of this week
			const day = d.getDay();
			const diff = d.getDate() - day + (day === 0 ? -6 : 1);
			const monday = new Date(d);
			monday.setDate(diff);
			const key = monday.toISOString().slice(0, 10);

			if (!weekMap.has(key)) {
				weekMap.set(key, []);
			}
			weekMap.get(key)!.push(issue);
		}

		const sortedWeeks = [...weekMap.entries()].sort(([a], [b]) => a.localeCompare(b));
		for (const [weekStart, weekIssues] of sortedWeeks) {
			const mon = new Date(weekStart + 'T00:00:00');
			const sun = new Date(mon);
			sun.setDate(mon.getDate() + 6);
			const label = `${mon.toLocaleDateString('default', { month: 'short', day: 'numeric' })} – ${sun.toLocaleDateString('default', { month: 'short', day: 'numeric' })}`;
			groups.push({ weekLabel: label, issues: weekIssues });
		}

		return groups;
	});

	function isToday(dateStr: string): boolean {
		const today = new Date().toISOString().slice(0, 10);
		return dateStr === today;
	}

	function isPast(dateStr: string): boolean {
		const today = new Date().toISOString().slice(0, 10);
		return dateStr < today;
	}

	$effect(() => {
		// Re-fetch when month or filter changes
		void [year, month, trackedOnly];
		loadCalendar();
	});
</script>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-3xl font-bold">Calendar</h1>
			<p class="text-gray-400 mt-1">Upcoming releases by store date</p>
		</div>
		<div class="flex items-center gap-3">
			<label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
				<input
					type="checkbox"
					bind:checked={trackedOnly}
					class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-amber-500 focus:ring-amber-500"
				/>
				Tracked only
			</label>
		</div>
	</div>

	<!-- Month Navigation -->
	<div class="flex items-center gap-4 bg-gray-800 rounded-lg border border-gray-700 px-4 py-3">
		<button
			onclick={prevMonth}
			class="p-1 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200 transition-colors"
			title="Previous month"
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
			</svg>
		</button>
		<h2 class="text-lg font-semibold flex-1 text-center">{monthName}</h2>
		<button
			onclick={goToday}
			class="text-xs px-2 py-1 bg-gray-700 text-gray-300 rounded hover:bg-gray-600 transition-colors"
		>
			Today
		</button>
		<button
			onclick={nextMonth}
			class="p-1 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200 transition-colors"
			title="Next month"
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
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if issues.length === 0}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
			</svg>
			<p class="text-lg font-medium">No releases this month</p>
			<p class="text-sm mt-2">
				{#if trackedOnly}
					Try unchecking "Tracked only" to see all series, or navigate to a different month.
				{:else}
					No issues have store dates in this month. Try a different month.
				{/if}
			</p>
		</div>
	{:else}
		<div class="space-y-4">
			{#each weekGroups() as week (week.weekLabel)}
				<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
					<div class="px-4 py-2 border-b border-gray-700 bg-gray-800/50">
						<h3 class="text-sm font-semibold text-gray-300">{week.weekLabel}</h3>
					</div>
					<div class="divide-y divide-gray-700/50">
						{#each week.issues as issue (issue.id)}
							<div class="px-4 py-3 flex items-center gap-4 hover:bg-gray-750 transition-colors">
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

								<!-- Cover thumbnail -->
								{#if issue.file_id}
									<div class="w-8 h-12 flex-shrink-0 bg-gray-700 rounded overflow-hidden">
										<img
											src="/api/v1/covers/file/{issue.file_id}"
											alt="#{issue.issue_number}"
											class="w-full h-full object-cover"
											loading="lazy"
										/>
									</div>
								{/if}

								<!-- Issue info -->
								<div class="flex-1 min-w-0">
									<div class="flex items-center gap-2">
										<a href="/library/{issue.series_id}"
											class="text-sm font-medium text-gray-200 hover:text-amber-400 transition-colors truncate">
											{issue.series_title}
										</a>
										<span class="text-sm text-gray-400">#{issue.issue_number}</span>
									</div>
									{#if issue.title}
										<p class="text-xs text-gray-500 truncate mt-0.5">{issue.title}</p>
									{/if}
								</div>

								<!-- Status badge -->
								<div class="flex-shrink-0">
									{#if issue.has_file}
										<span class="text-xs px-2 py-0.5 rounded-full bg-green-900/50 text-green-400">Owned</span>
									{:else}
										<span class="text-xs px-2 py-0.5 rounded-full bg-amber-900/50 text-amber-400">Wanted</span>
									{/if}
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/each}
		</div>

		<p class="text-center text-sm text-gray-500">
			{issues.length} issue{issues.length !== 1 ? 's' : ''} this month
		</p>
	{/if}
</div>
