<script lang="ts">
	import type { ComicFile } from '$lib/api/client';
	import { createEventDispatcher } from 'svelte';

	export type FileFolderGroup = {
		id: string;
		label: string;
		path: string;
		files: ComicFile[];
		totalSize: number;
		formatCounts: Record<string, number>;
	};

	let { group }: { group: FileFolderGroup } = $props();

	const dispatch = createEventDispatcher<{ select: FileFolderGroup }>();

	const prettySize = $derived(() => {
		const bytes = group.totalSize;
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
		return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
	});

	function handleSelect() {
		dispatch('select', group);
	}
</script>

<button
	type="button"
	onclick={handleSelect}
	class="group block rounded-xl bg-gray-800/90 border border-gray-700 hover:border-amber-400/60 hover:bg-gray-800 shadow-lg transition-all w-full text-left"
>
	<div class="flex items-center gap-3 p-4">
		<div class="flex-shrink-0 w-12 h-12 rounded-lg bg-gradient-to-br from-gray-700 to-gray-900 flex items-center justify-center text-amber-300">
			<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v7a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
			</svg>
		</div>
		<div class="flex-1 min-w-0">
			<p class="text-sm font-semibold text-gray-100 truncate">{group.label}</p>
			<p class="text-xs text-gray-500 truncate" title={group.path}>{group.path}</p>
			<p class="text-xs text-gray-400 mt-1">
				{group.files.length} file{group.files.length === 1 ? '' : 's'} · {prettySize}
			</p>
			{#if Object.keys(group.formatCounts).length > 0}
				<div class="flex flex-wrap gap-1 mt-2">
					{#each Object.entries(group.formatCounts) as [format, count]}
						<span class="text-[10px] px-2 py-0.5 rounded-full bg-gray-900/80 text-gray-300">
							{format.toUpperCase()} · {count}
						</span>
					{/each}
				</div>
			{/if}
		</div>
		<div class="opacity-0 group-hover:opacity-100 transition-opacity text-amber-300">
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 5l7 7-7 7" />
			</svg>
		</div>
	</div>
</button>
