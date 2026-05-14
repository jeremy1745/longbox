<script lang="ts">
	import { ApiClient, type OrganizePreviewResponse, type OrganizeTemplateResponse, type RenamePreview, type RenameResult } from '$lib/api/client';

	let template = $state('');
	let loading = $state(true);
	let previews = $state<RenamePreview[]>([]);
	let previewGenerated = $state(false);
	let previewLoading = $state(false);
	let executing = $state(false);
	let result = $state<RenameResult | null>(null);
	let error = $state<string | null>(null);

	// Summary counts
	let moves = $state(0);
	let skips = $state(0);
	let conflicts = $state(0);
	let unlinked = $state(0);

	// Filter
	let statusFilter = $state<string>('all');

	let filteredPreviews = $derived(() => {
		if (statusFilter === 'all') return previews;
		return previews.filter(p => p.status === statusFilter);
	});

	async function loadTemplate() {
		loading = true;
		try {
			const data = await ApiClient.get<OrganizeTemplateResponse>('/library/organize/template');
			template = data.template;
		} catch {
			template = '';
		} finally {
			loading = false;
		}
	}

	async function generatePreview() {
		previewLoading = true;
		error = null;
		result = null;
		previewGenerated = false;
		try {
			const data = await ApiClient.post<OrganizePreviewResponse>('/library/organize/preview');
			previews = data.previews || [];
			moves = data.moves;
			skips = data.skips;
			conflicts = data.conflicts;
			unlinked = data.unlinked;
			previewGenerated = true;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to generate preview';
		} finally {
			previewLoading = false;
		}
	}

	let showConfirm = $state(false);

	async function executeOrganize() {
		showConfirm = false;
		executing = true;
		error = null;
		try {
			result = await ApiClient.post<RenameResult>('/library/organize/execute');
			previewGenerated = false;
			previews = [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to organize files';
		} finally {
			executing = false;
		}
	}

	function basename(path: string): string {
		return path.split('/').pop() || path;
	}

	function parentPath(path: string): string {
		const parts = path.split('/');
		return parts.slice(-2, -1)[0] || '';
	}

	function statusColor(status: string): string {
		switch (status) {
			case 'move': return 'bg-green-900/50 text-green-400';
			case 'skip': return 'bg-gray-700 text-gray-400';
			case 'conflict': return 'bg-red-900/50 text-red-400';
			case 'unlinked': return 'bg-yellow-900/50 text-yellow-400';
			default: return 'bg-gray-700 text-gray-400';
		}
	}

	$effect(() => {
		loadTemplate();
	});
</script>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<div class="flex items-center gap-3">
				<a href="/settings" class="text-gray-400 hover:text-gray-200 transition-colors" title="Back to Settings">
					<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
					</svg>
				</a>
				<h1 class="text-3xl font-bold">Organize Library</h1>
			</div>
			<p class="text-gray-400 mt-1 ml-8">Preview and apply file renames based on your naming template</p>
		</div>
	</div>

	<!-- Current template -->
	{#if !loading}
		<div class="bg-gray-800 rounded-lg border border-gray-700 px-4 py-3 flex items-center justify-between">
			<div class="flex items-center gap-3">
				<span class="text-sm text-gray-400">Template:</span>
				<code class="text-sm text-amber-400 font-mono">{template}</code>
			</div>
			<a href="/settings" class="text-xs text-gray-400 hover:text-gray-200 transition-colors">Edit</a>
		</div>
	{/if}

	<!-- Error display -->
	{#if error}
		<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
			<p class="text-red-400">{error}</p>
		</div>
	{/if}

	<!-- Execute result -->
	{#if result}
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-lg font-semibold mb-4 text-green-400">Organization Complete</h2>
			<div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
				<div class="text-center">
					<p class="text-2xl font-bold text-green-400">{result.moved}</p>
					<p class="text-xs text-gray-400">Moved</p>
				</div>
				<div class="text-center">
					<p class="text-2xl font-bold text-gray-400">{result.skipped}</p>
					<p class="text-xs text-gray-400">Skipped</p>
				</div>
				<div class="text-center">
					<p class="text-2xl font-bold text-red-400">{result.errors}</p>
					<p class="text-xs text-gray-400">Errors</p>
				</div>
				<div class="text-center">
					<p class="text-2xl font-bold text-gray-300">{result.total_files}</p>
					<p class="text-xs text-gray-400">Total</p>
				</div>
			</div>
			{#if result.error_details && result.error_details.length > 0}
				<div class="mt-4 pt-4 border-t border-gray-700">
					<p class="text-sm font-medium text-red-400 mb-2">Errors:</p>
					<div class="space-y-1">
						{#each result.error_details as detail}
							<p class="text-xs text-red-300/70 font-mono">{detail}</p>
						{/each}
					</div>
				</div>
			{/if}
		</div>
	{/if}

	<!-- Actions -->
	<div class="flex items-center gap-3">
		<button
			onclick={generatePreview}
			disabled={previewLoading || loading}
			class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
				disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
		>
			{previewLoading ? 'Generating...' : 'Generate Preview'}
		</button>

		{#if previewGenerated && moves > 0}
			<button
				onclick={() => showConfirm = true}
				disabled={executing}
				class="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:bg-gray-600
					disabled:cursor-not-allowed text-white font-semibold rounded-lg transition-colors"
			>
				{executing ? 'Organizing...' : `Apply Changes (${moves} files)`}
			</button>
		{/if}
	</div>

	<!-- Confirmation dialog -->
	{#if showConfirm}
		<div class="bg-amber-900/30 border border-amber-700 rounded-lg p-4">
			<p class="text-amber-300 font-medium mb-3">
				Are you sure you want to move {moves} file{moves !== 1 ? 's' : ''}?
			</p>
			<p class="text-sm text-gray-400 mb-4">
				Files will be physically renamed and moved on disk. This cannot be undone automatically.
			</p>
			<div class="flex gap-3">
				<button
					onclick={executeOrganize}
					class="px-4 py-2 bg-green-600 hover:bg-green-700 text-white font-semibold rounded-lg transition-colors"
				>
					Yes, Organize Files
				</button>
				<button
					onclick={() => showConfirm = false}
					class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 font-medium rounded-lg transition-colors"
				>
					Cancel
				</button>
			</div>
		</div>
	{/if}

	<!-- Preview results -->
	{#if previewGenerated}
		<!-- Summary -->
		<div class="flex flex-wrap gap-3">
			<button
				onclick={() => statusFilter = 'all'}
				class="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors
					{statusFilter === 'all' ? 'bg-gray-600 text-white' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
			>
				All ({previews.length})
			</button>
			{#if moves > 0}
				<button
					onclick={() => statusFilter = 'move'}
					class="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors
						{statusFilter === 'move' ? 'bg-green-900/50 text-green-400' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				>
					Will Move ({moves})
				</button>
			{/if}
			{#if skips > 0}
				<button
					onclick={() => statusFilter = 'skip'}
					class="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors
						{statusFilter === 'skip' ? 'bg-gray-600 text-white' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				>
					Already Organized ({skips})
				</button>
			{/if}
			{#if conflicts > 0}
				<button
					onclick={() => statusFilter = 'conflict'}
					class="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors
						{statusFilter === 'conflict' ? 'bg-red-900/50 text-red-400' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				>
					Conflicts ({conflicts})
				</button>
			{/if}
			{#if unlinked > 0}
				<button
					onclick={() => statusFilter = 'unlinked'}
					class="px-3 py-1.5 rounded-lg text-sm font-medium transition-colors
						{statusFilter === 'unlinked' ? 'bg-yellow-900/50 text-yellow-400' : 'bg-gray-800 text-gray-400 hover:bg-gray-700'}"
				>
					Unlinked ({unlinked})
				</button>
			{/if}
		</div>

		<!-- Preview table -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 overflow-hidden">
			<div class="divide-y divide-gray-700/50">
				{#each filteredPreviews() as preview (preview.file_id)}
					<div class="px-4 py-3 hover:bg-gray-750 transition-colors">
						<div class="flex items-start gap-3">
							<span class="flex-shrink-0 mt-0.5 text-xs px-2 py-0.5 rounded-full {statusColor(preview.status)}">
								{preview.status}
							</span>
							<div class="flex-1 min-w-0 space-y-1">
								<div class="flex items-center gap-2">
									<span class="text-xs text-gray-500 flex-shrink-0">{parentPath(preview.current_path)}/</span>
									<span class="text-sm text-gray-300 truncate">{basename(preview.current_path)}</span>
								</div>
								{#if preview.status === 'move'}
									<div class="flex items-center gap-2">
										<svg class="w-3 h-3 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 8l4 4m0 0l-4 4m4-4H3" />
										</svg>
										<span class="text-xs text-green-400/70 flex-shrink-0">{parentPath(preview.new_path)}/</span>
										<span class="text-sm text-green-400 truncate">{basename(preview.new_path)}</span>
									</div>
								{/if}
								{#if preview.reason}
									<p class="text-xs text-gray-500">{preview.reason}</p>
								{/if}
							</div>
						</div>
					</div>
				{/each}
				{#if filteredPreviews().length === 0}
					<div class="px-4 py-8 text-center text-gray-500 text-sm">
						No files match this filter.
					</div>
				{/if}
			</div>
		</div>
	{:else if !previewLoading && !result}
		<div class="flex flex-col items-center justify-center py-16 text-gray-400">
			<svg class="w-16 h-16 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
					d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
			</svg>
			<p class="text-lg font-medium">Ready to organize</p>
			<p class="text-sm mt-2">Click "Generate Preview" to see how your files will be renamed.</p>
		</div>
	{/if}
</div>
