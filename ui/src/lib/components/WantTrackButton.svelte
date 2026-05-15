<script lang="ts">
	import type { WantTrackResult } from '$lib/api/client';

	let {
		comicvineId,
		metronId,
		sourceIssueId,
		variant = 'full',
		onTracked,
	}: {
		comicvineId?: number;
		metronId?: number;
		sourceIssueId?: number;
		variant?: 'compact' | 'full';
		onTracked?: (result: WantTrackResult) => void;
	} = $props();

	let loading = $state(false);
	let result = $state<WantTrackResult | null>(null);
	let error = $state<string | null>(null);
	// Conflict state: a series with this title+year already exists in the library.
	// Unlike ComicVineSearch's merge prompt, there is NO source series to merge FROM
	// here (the user is tracking a series that isn't in LongBox yet) — so the backend
	// omits requested_series_id and we offer navigation, not a merge.
	let conflict = $state<{ seriesId: number; title: string; message: string } | null>(null);

	let canTrack = $derived(!!comicvineId || !!metronId);

	async function track() {
		if (loading || !canTrack) return;
		loading = true;
		error = null;
		result = null;
		conflict = null;
		try {
			const body: Record<string, number> = {};
			if (comicvineId) body.comicvine_id = comicvineId;
			if (metronId) body.metron_id = metronId;
			if (sourceIssueId) body.source_issue_id = sourceIssueId;

			// Raw fetch (not ApiClient) so we can read the 409 conflict body.
			const res = await fetch('/api/v1/pull-list/want-track', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body),
				credentials: 'include',
			});

			if (res.status === 409) {
				const data = await res.json().catch(() => null);
				if (data && data.conflicting_series_id) {
					conflict = {
						seriesId: data.conflicting_series_id,
						title: data.conflicting_series_title || `series #${data.conflicting_series_id}`,
						message: data?.error?.message || 'This series already exists in your library.',
					};
					return;
				}
				error = data?.error?.message || 'This series already exists in your library.';
				return;
			}
			if (!res.ok) {
				const data = await res.json().catch(() => null);
				error = data?.error?.message || `HTTP ${res.status}`;
				return;
			}

			result = (await res.json()) as WantTrackResult;
			onTracked?.(result);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Track failed';
		} finally {
			loading = false;
		}
	}

	function dismiss() {
		result = null;
		error = null;
		conflict = null;
	}
</script>

<div class={variant === 'compact' ? 'relative inline-block' : 'space-y-2'}>
	{#if variant === 'compact'}
		<button
			onclick={track}
			disabled={loading || !canTrack}
			class="p-1.5 rounded-md text-gray-500 hover:text-amber-400 hover:bg-amber-500/10
				disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
			title="Track this series — queues every issue and pulls metadata"
		>
			{#if loading}
				<svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
					<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
					<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
				</svg>
			{:else}
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
				</svg>
			{/if}
		</button>
	{:else}
		<button
			onclick={track}
			disabled={loading || !canTrack}
			class="px-3 py-1.5 text-sm rounded-lg transition-colors flex items-center gap-1.5
				bg-gray-700 text-gray-300 hover:bg-gray-600 disabled:opacity-50 disabled:cursor-not-allowed"
			title="Track this series — queues every issue and pulls metadata"
		>
			<svg class="w-4 h-4 {loading ? 'animate-spin' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				{#if loading}
					<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
					<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
				{:else}
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
				{/if}
			</svg>
			{loading ? 'Tracking…' : 'Track'}
		</button>
	{/if}

	{#if conflict}
		<div class="mt-1 {variant === 'compact' ? 'absolute right-0 z-10 w-64' : ''} bg-amber-900/20 border border-amber-700/60 rounded-lg p-2.5 space-y-1.5 shadow-lg">
			<p class="text-xs text-amber-200">{conflict.message}</p>
			<div class="flex gap-2">
				<a
					href="/library/{conflict.seriesId}"
					class="px-2.5 py-1 text-xs bg-amber-500 hover:bg-amber-600 text-gray-900 font-semibold rounded transition-colors"
				>
					Go to {conflict.title}
				</a>
				<button onclick={dismiss} class="px-2.5 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-200 rounded transition-colors">
					Dismiss
				</button>
			</div>
		</div>
	{/if}

	{#if error}
		<div class="mt-1 {variant === 'compact' ? 'absolute right-0 z-10 w-64' : ''} bg-red-900/30 border border-red-700 rounded-lg p-2.5 shadow-lg">
			<p class="text-xs text-red-400">{error}</p>
			<button onclick={dismiss} class="mt-1 text-xs text-gray-400 hover:text-gray-200">Dismiss</button>
		</div>
	{/if}

	{#if result}
		<div class="mt-1 {variant === 'compact' ? 'absolute right-0 z-10 w-64' : ''} bg-green-900/25 border border-green-700/60 rounded-lg p-2.5 space-y-1 shadow-lg">
			<p class="text-xs text-green-300">
				Tracking — {result.issues_queued} queued, {result.files_moved} already in library
			</p>
			{#if result.warnings && result.warnings.length > 0}
				<ul class="text-[11px] text-amber-300/80 list-disc list-inside">
					{#each result.warnings as w}
						<li>{w}</li>
					{/each}
				</ul>
			{/if}
			<button onclick={dismiss} class="text-xs text-gray-400 hover:text-gray-200">Dismiss</button>
		</div>
	{/if}
</div>
