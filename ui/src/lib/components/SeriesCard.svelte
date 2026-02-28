<script lang="ts">
	import type { Series } from '$lib/api/client';

	let { series }: { series: Series } = $props();

	let completionPct = $derived(
		series.total_issues > 0
			? Math.round((series.file_count / series.total_issues) * 100)
			: series.issue_count > 0 ? 100 : 0
	);
</script>

<a
	href="/library/{series.id}"
	class="group block rounded-lg overflow-hidden bg-gray-800 shadow-lg hover:shadow-xl hover:ring-2 hover:ring-amber-400/50 transition-all"
>
	<!-- Stacked cover effect to look like a folder of issues -->
	<div class="relative aspect-[2/3]">
		<!-- Background layers (stacked look) -->
		{#if series.file_count > 1}
			<div class="absolute inset-x-0 top-0 bottom-0 bg-gray-600 rounded-t-lg translate-x-1.5 -translate-y-1 scale-[0.94] opacity-30"></div>
			<div class="absolute inset-x-0 top-0 bottom-0 bg-gray-650 rounded-t-lg translate-x-0.5 -translate-y-0.5 scale-[0.97] opacity-50"></div>
		{/if}

		<!-- Main cover -->
		<div class="relative w-full h-full bg-gray-700 overflow-hidden">
			{#if series.cover_file_id}
				<img
					src="/api/v1/covers/file/{series.cover_file_id}"
					alt={series.title}
					class="w-full h-full object-cover"
					loading="lazy"
				/>
			{:else}
				<div class="w-full h-full flex items-center justify-center text-gray-500">
					<svg class="w-12 h-12" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
							d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253" />
					</svg>
				</div>
			{/if}

			<!-- Issue count badge -->
			<div class="absolute top-2 left-2 bg-gray-900/80 backdrop-blur-sm text-gray-100 text-xs font-bold px-2 py-0.5 rounded-full shadow">
				{series.file_count}{series.total_issues > 0 ? ` / ${series.total_issues}` : ''}
			</div>

			<!-- Tracked badge -->
			{#if series.tracked}
				<div class="absolute top-2 right-2 bg-amber-500 rounded-full p-0.5 shadow-lg" title="Tracked">
					<svg class="w-3 h-3 text-gray-900" fill="currentColor" viewBox="0 0 24 24">
						<path d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
					</svg>
				</div>
			{/if}

			<!-- Hover overlay -->
			<div class="absolute inset-0 bg-black/50 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
				<span class="px-4 py-2 bg-amber-500 text-gray-900 font-semibold rounded-lg text-sm">
					View Series
				</span>
			</div>
		</div>
	</div>

	<!-- Info bar -->
	<div class="p-3">
		<p class="text-sm text-gray-200 font-medium truncate" title={series.title}>
			{series.title}
		</p>
		<div class="flex items-center justify-between mt-1">
			<span class="text-xs text-gray-400">
				{#if series.year}({series.year}){/if}
				{#if series.publisher_name}
					{#if series.year} &middot; {/if}{series.publisher_name}
				{/if}
			</span>
		</div>

		<!-- Collection progress bar -->
		{#if series.total_issues > 0}
			<div class="mt-2">
				<div class="h-1 bg-gray-700 rounded-full overflow-hidden">
					<div
						class="h-full rounded-full transition-all {completionPct === 100 ? 'bg-green-500' : 'bg-amber-500'}"
						style="width: {completionPct}%"
					></div>
				</div>
				<p class="text-[10px] text-gray-500 mt-0.5 text-right">{completionPct}%</p>
			</div>
		{/if}
	</div>
</a>
