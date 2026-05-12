<script lang="ts">
	import type { Series } from '$lib/api/client';
	import { createEventDispatcher } from 'svelte';

	export type SeriesGroup = {
		title: string;
		series: Series[];
		coverSeries: Series | null;
		fileCount: number;
		totalIssues: number;
	};

	let { group }: { group: SeriesGroup } = $props();

	const dispatch = createEventDispatcher<{ select: SeriesGroup }>();

	let completionPct = $derived(() => {
		if (group.totalIssues > 0) {
			return Math.min(100, Math.round((group.fileCount / group.totalIssues) * 100));
		}
		return group.series.length > 0 ? 100 : 0;
	});

	function handleSelect() {
		dispatch('select', group);
	}
</script>

<button
	type="button"
	onclick={handleSelect}
	class="group block rounded-lg overflow-hidden bg-gray-800 shadow-lg hover:shadow-2xl hover:ring-2 hover:ring-amber-400/40 transition-all w-full text-left"
>
	<div class="relative aspect-[2/3]">
		{#if group.coverSeries?.cover_file_id}
			<img
				src={`/api/v1/covers/file/${group.coverSeries.cover_file_id}`}
				alt={group.title}
				class="w-full h-full object-cover"
				loading="lazy"
			/>
		{:else}
			<div class="w-full h-full bg-gray-700 flex items-center justify-center">
				<svg class="w-12 h-12 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
						d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253" />
				</svg>
			</div>
		{/if}

		<div class="absolute top-2 left-2 bg-gray-900/80 backdrop-blur-sm text-gray-100 text-xs font-bold px-2 py-0.5 rounded-full shadow">
			{group.series.length} volume{group.series.length !== 1 ? 's' : ''}
		</div>
		<div class="absolute top-2 right-2 bg-gray-900/80 backdrop-blur-sm text-gray-100 text-xs font-bold px-2 py-0.5 rounded-full shadow">
			{group.fileCount} file{group.fileCount !== 1 ? 's' : ''}
		</div>

		<div class="absolute inset-0 bg-black/50 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
			<span class="px-4 py-2 bg-amber-500 text-gray-900 font-semibold rounded-lg text-sm">
				View Volumes
			</span>
		</div>
	</div>

	<div class="p-3 space-y-2">
		<p class="text-sm text-gray-200 font-medium truncate" title={group.title}>
			{group.title}
		</p>
		<p class="text-xs text-gray-500">{group.fileCount} comic{group.fileCount !== 1 ? 's' : ''}</p>

		{#if group.totalIssues > 0}
			<div>
				<div class="h-1 bg-gray-700 rounded-full overflow-hidden">
					<div
						class={`h-full rounded-full ${completionPct === 100 ? 'bg-green-500' : 'bg-amber-500'}`}
						style={`width: ${completionPct}%`}
					></div>
				</div>
				<p class="text-[10px] text-gray-500 mt-0.5 text-right">{completionPct}% complete</p>
			</div>
		{/if}
	</div>
</button>
