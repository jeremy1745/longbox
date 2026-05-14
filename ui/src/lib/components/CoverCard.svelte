<script lang="ts">
	import type { ComicFile } from '$lib/api/client';

	let { file }: { file: ComicFile } = $props();

	function formatSize(bytes: number): string {
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}
</script>

<div class="group relative rounded-lg overflow-hidden bg-gray-800 shadow-lg hover:shadow-xl transition-shadow">
	<div class="aspect-[2/3] bg-gray-700">
		<img
			src="/api/v1/covers/file/{file.id}"
			alt={file.file_name}
			class="w-full h-full object-cover"
			loading="lazy"
			onerror={(e) => {
				const target = e.currentTarget as HTMLImageElement;
				target.style.display = 'none';
				target.nextElementSibling?.classList.remove('hidden');
			}}
		/>
		<div class="hidden w-full h-full flex items-center justify-center text-gray-400 text-sm p-4 text-center">
			{file.file_name}
		</div>
	</div>
	<div class="p-3">
		<p class="text-sm text-gray-200 font-medium truncate" title={file.file_name}>
			{file.file_name}
		</p>
		<div class="flex items-center justify-between mt-1">
			<span class="text-xs text-gray-400 uppercase">{file.file_format}</span>
			<span class="text-xs text-gray-400">{formatSize(file.file_size)}</span>
		</div>
	</div>
</div>
