<script lang="ts">
	import { ApiClient, type Job, type JobListResponse } from '$lib/api/client';

	let jobs = $state<Job[]>([]);
	let activeJobs = $state<Job[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let eventSource = $state<EventSource | null>(null);

	async function loadJobs() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<JobListResponse>('/jobs?limit=50');
			jobs = data.jobs || [];
			activeJobs = data.active || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load jobs';
		} finally {
			loading = false;
		}
	}

	async function cancelJob(jobId: number) {
		try {
			await ApiClient.post(`/jobs/${jobId}/cancel`);
			await loadJobs();
		} catch (e) {
			console.error('Failed to cancel job', e);
		}
	}

	function connectSSE() {
		const es = new EventSource('/api/v1/events');
		eventSource = es;

		es.onmessage = (event) => {
			try {
				const data = JSON.parse(event.data);
				if (data.type === 'job:updated' || data.type === 'job:created') {
					// Reload jobs list on any job event
					loadJobs();
				}
			} catch {
				// ignore
			}
		};
	}

	function formatDuration(job: Job): string {
		if (!job.started_at) return '-';
		const start = new Date(job.started_at).getTime();
		const end = job.completed_at ? new Date(job.completed_at).getTime() : Date.now();
		const ms = end - start;
		if (ms < 1000) return `${ms}ms`;
		if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
		return `${(ms / 60000).toFixed(1)}m`;
	}

	function formatTime(dateStr: string): string {
		const d = new Date(dateStr);
		return d.toLocaleString();
	}

	function statusColor(status: string): string {
		switch (status) {
			case 'running': return 'text-amber-400';
			case 'completed': return 'text-green-400';
			case 'failed': return 'text-red-400';
			case 'cancelled': return 'text-gray-400';
			case 'pending': return 'text-blue-400';
			default: return 'text-gray-400';
		}
	}

	function statusBadgeClass(status: string): string {
		switch (status) {
			case 'running': return 'bg-amber-900/50 text-amber-400';
			case 'completed': return 'bg-green-900/50 text-green-400';
			case 'failed': return 'bg-red-900/50 text-red-400';
			case 'cancelled': return 'bg-gray-700 text-gray-400';
			case 'pending': return 'bg-blue-900/50 text-blue-400';
			default: return 'bg-gray-700 text-gray-400';
		}
	}

	function jobTypeLabel(type: string): string {
		switch (type) {
			case 'scan': return 'Library Scan';
			case 'metadata_refresh': return 'Metadata Refresh';
			case 'longbox_metadata': return 'LongBox Sidecars';
			case 'mylar_metadata': return 'LongBox Sidecars';
			default: return type;
		}
	}

	$effect(() => {
		loadJobs();
		connectSSE();
		return () => {
			if (eventSource) {
				eventSource.close();
			}
		};
	});
</script>

<div class="space-y-6">
	<div>
		<h1 class="text-3xl font-bold">Jobs</h1>
		<p class="text-gray-400 mt-1">Background task history and progress</p>
	</div>

	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	<!-- Active Jobs -->
	{#if activeJobs.length > 0}
		<div class="space-y-3">
			<h2 class="text-lg font-semibold text-amber-400">Active Jobs</h2>
			{#each activeJobs as job (job.id)}
				<div class="bg-gray-800 border border-amber-700/50 rounded-lg p-4">
					<div class="flex items-center justify-between mb-2">
						<div class="flex items-center gap-3">
							<span class="font-medium">{jobTypeLabel(job.type)}</span>
							<span class="text-xs px-2 py-0.5 rounded-full {statusBadgeClass(job.status)}">{job.status}</span>
						</div>
						<div class="flex items-center gap-3">
							<span class="text-sm text-gray-400">{job.progress}%</span>
							{#if job.status === 'running'}
								<button
									onclick={() => cancelJob(job.id)}
									class="text-xs px-2 py-1 bg-red-900/50 text-red-400 rounded hover:bg-red-900/70 transition-colors"
								>
									Cancel
								</button>
							{/if}
						</div>
					</div>
					<div class="w-full bg-gray-700 rounded-full h-2 mb-2">
						<div
							class="bg-amber-500 h-2 rounded-full transition-all duration-300"
							style="width: {job.progress}%"
						></div>
					</div>
					{#if job.message}
						<p class="text-xs text-gray-400 truncate">{job.message}</p>
					{/if}
					{#if job.total_items > 0}
						<p class="text-xs text-gray-500 mt-1">{job.processed_items} / {job.total_items} items</p>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	<!-- Job History -->
	{#if loading}
		<div class="flex items-center justify-center py-20">
			<div class="text-gray-400">Loading...</div>
		</div>
	{:else if jobs.length > 0}
		<div>
			<h2 class="text-lg font-semibold mb-3">History</h2>
			<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
				<table class="w-full">
					<thead>
						<tr class="border-b border-gray-700 text-left text-sm text-gray-400">
							<th class="px-4 py-3 font-medium">ID</th>
							<th class="px-4 py-3 font-medium">Type</th>
							<th class="px-4 py-3 font-medium">Status</th>
							<th class="px-4 py-3 font-medium">Progress</th>
							<th class="px-4 py-3 font-medium">Duration</th>
							<th class="px-4 py-3 font-medium">Message</th>
							<th class="px-4 py-3 font-medium">Started</th>
						</tr>
					</thead>
					<tbody>
						{#each jobs as job (job.id)}
							<tr class="border-b border-gray-700/50 text-sm hover:bg-gray-750 transition-colors"
								style="background-color: transparent;">
								<td class="px-4 py-3 text-gray-500">#{job.id}</td>
								<td class="px-4 py-3">{jobTypeLabel(job.type)}</td>
								<td class="px-4 py-3">
									<span class="text-xs px-2 py-0.5 rounded-full {statusBadgeClass(job.status)}">{job.status}</span>
								</td>
								<td class="px-4 py-3">
									{#if job.status === 'running'}
										<div class="flex items-center gap-2">
											<div class="w-16 bg-gray-700 rounded-full h-1.5">
												<div class="bg-amber-500 h-1.5 rounded-full" style="width: {job.progress}%"></div>
											</div>
											<span class="text-xs text-gray-400">{job.progress}%</span>
										</div>
									{:else}
										<span class="text-gray-400">{job.progress}%</span>
									{/if}
								</td>
								<td class="px-4 py-3 text-gray-400">{formatDuration(job)}</td>
								<td class="px-4 py-3 text-gray-400 max-w-xs truncate" title={job.message || ''}>
									{job.message || '-'}
								</td>
								<td class="px-4 py-3 text-gray-500 text-xs">
									{job.started_at ? formatTime(job.started_at) : '-'}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{:else}
		<div class="flex flex-col items-center justify-center py-20 text-gray-400">
			<p class="text-lg font-medium">No jobs yet</p>
			<p class="text-sm mt-2">Jobs will appear here when you scan your library or refresh metadata.</p>
		</div>
	{/if}
</div>
