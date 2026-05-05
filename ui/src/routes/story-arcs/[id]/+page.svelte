<script lang="ts">
	import { page } from '$app/stores';
	import { ApiClient, type StoryArc, type StoryArcIssue, type StoryArcDetailResponse } from '$lib/api/client';
	import { proxiedCoverURL } from '$lib/cover';

	let arc = $state<StoryArc | null>(null);
	let issues = $state<StoryArcIssue[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	let arcId = $derived($page.params.id);

	async function loadArc() {
		loading = true;
		error = null;
		try {
			const data = await ApiClient.get<StoryArcDetailResponse>(`/story-arcs/${arcId}`);
			arc = data.story_arc;
			issues = data.issues || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load story arc';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if (arcId) loadArc();
	});
</script>

{#if loading}
	<div class="flex items-center justify-center py-20">
		<div class="text-gray-400">Loading...</div>
	</div>
{:else if error}
	<div class="bg-red-900/30 border border-red-700 rounded-lg p-4">
		<p class="text-red-400">{error}</p>
	</div>
{:else if arc}
	<div class="space-y-6">
		<a href="/story-arcs" class="text-amber-400 hover:text-amber-300 text-sm">&larr; Back to Story Arcs</a>

		<div>
			<h1 class="text-3xl font-bold">{arc.name}</h1>
			{#if arc.description}
				<p class="text-gray-400 mt-2 leading-relaxed">{arc.description}</p>
			{/if}
			<div class="flex items-center gap-4 mt-3 text-sm text-gray-400">
				<span>{arc.issue_count} issue{arc.issue_count !== 1 ? 's' : ''}</span>
				<span>{arc.owned_count} owned</span>
				{#if arc.comicvine_id}
					<a
						href="https://comicvine.gamespot.com/story-arc/4045-{arc.comicvine_id}"
						target="_blank"
						rel="noopener"
						class="text-amber-400 hover:text-amber-300"
					>
						ComicVine
					</a>
				{/if}
			</div>
			{#if arc.issue_count > 0}
				<div class="flex items-center gap-3 mt-3 max-w-md">
					<div class="flex-1 bg-gray-700 rounded-full h-2.5 overflow-hidden">
						<div
							class="bg-amber-500 h-full rounded-full transition-all"
							style="width: {Math.round((arc.owned_count / arc.issue_count) * 100)}%"
						></div>
					</div>
					<span class="text-sm text-amber-400 font-medium">{Math.round((arc.owned_count / arc.issue_count) * 100)}%</span>
				</div>
			{/if}
		</div>

		<!-- Reading Order -->
		<div>
			<h2 class="text-xl font-semibold mb-4">Reading Order</h2>
			{#if issues.length === 0}
				<p class="text-gray-400">No issues linked to this story arc yet.</p>
			{:else}
				<div class="space-y-2">
					{#each issues as issue, i (issue.issue_id)}
						<div class="flex items-center gap-4 p-3 bg-gray-800 rounded-lg border border-gray-700
							{issue.has_file ? '' : 'opacity-60'}">
							<span class="text-gray-500 text-sm font-mono w-8 text-right flex-shrink-0">
								{issue.sequence_number ?? i + 1}
							</span>
							{#if issue.cover_url}
								<img
									src={proxiedCoverURL(issue.cover_url)}
									alt=""
									class="w-10 h-14 object-cover rounded flex-shrink-0"
									loading="lazy"
								/>
							{:else}
								<div class="w-10 h-14 bg-gray-700 rounded flex-shrink-0"></div>
							{/if}
							<div class="flex-1 min-w-0">
								<p class="text-gray-200 font-medium">
									{issue.series_title || 'Unknown Series'}
									{#if issue.issue_number}
										<span class="text-gray-400">#{issue.issue_number}</span>
									{/if}
								</p>
							</div>
							<div class="flex items-center gap-2 flex-shrink-0">
								{#if issue.has_file}
									<a
										href="/reader/{issue.issue_id}"
										class="px-3 py-1 text-xs bg-amber-500 hover:bg-amber-600 text-gray-900 font-semibold rounded-lg transition-colors"
									>
										Read
									</a>
								{:else}
									<span class="text-xs text-gray-500">Missing</span>
								{/if}
								{#if issue.read_status === 'read'}
									<span class="text-xs px-2 py-0.5 bg-green-900/50 text-green-400 rounded-full">Read</span>
								{:else if issue.read_status === 'reading'}
									<span class="text-xs px-2 py-0.5 bg-amber-900/50 text-amber-400 rounded-full">Reading</span>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</div>
{/if}
