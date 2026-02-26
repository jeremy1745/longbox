<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { ApiClient, type ReaderPagesResponse, type Issue } from '$lib/api/client';

	// --- State ---
	let pagesData = $state<ReaderPagesResponse | null>(null);
	let issue = $state<Issue | null>(null);
	let currentPage = $state(0);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let fitMode = $state<'width' | 'height' | 'original'>('width');
	let showControls = $state(true);
	let controlsTimeout: ReturnType<typeof setTimeout> | null = null;
	let progressTimeout: ReturnType<typeof setTimeout> | null = null;
	let imgLoading = $state(false);

	// --- Derived ---
	let issueId = $derived($page.params.id);
	let totalPages = $derived(pagesData?.page_count ?? 0);
	let isFirstPage = $derived(currentPage === 0);
	let isLastPage = $derived(currentPage >= totalPages - 1);
	let pageUrl = $derived(`/api/v1/reader/${issueId}/pages/${currentPage}`);

	// --- Load issue and pages ---
	async function loadReader() {
		loading = true;
		error = null;
		try {
			const [issueData, readerData] = await Promise.all([
				ApiClient.get<Issue>(`/issues/${issueId}`),
				ApiClient.get<ReaderPagesResponse>(`/reader/${issueId}/pages`)
			]);
			issue = issueData;
			pagesData = readerData;

			// Resume from last read page
			if (readerData.last_read_page != null && readerData.last_read_page > 0) {
				currentPage = readerData.last_read_page;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load reader';
		} finally {
			loading = false;
		}
	}

	// --- Navigation ---
	function goToPage(index: number) {
		if (index < 0 || index >= totalPages) return;
		currentPage = index;
		imgLoading = true;
		saveProgress(index);

		// Auto-mark as "read" when reaching the last page
		if (index === totalPages - 1 && issue && issue.read_status !== 'read') {
			ApiClient.put(`/issues/${issueId}/read-status`, { read_status: 'read' });
			issue.read_status = 'read';
		}
	}

	function nextPage() { goToPage(currentPage + 1); }
	function prevPage() { goToPage(currentPage - 1); }

	// --- Progress saving (debounced) ---
	function saveProgress(pageIndex: number) {
		if (progressTimeout) clearTimeout(progressTimeout);
		progressTimeout = setTimeout(() => {
			ApiClient.put(`/reader/${issueId}/progress`, { page: pageIndex });
		}, 1000);
	}

	// --- Keyboard navigation ---
	function handleKeydown(e: KeyboardEvent) {
		switch (e.key) {
			case 'ArrowRight':
			case ' ':
				e.preventDefault();
				nextPage();
				break;
			case 'ArrowLeft':
				e.preventDefault();
				prevPage();
				break;
			case 'Escape':
				e.preventDefault();
				goBack();
				break;
			case 'f':
				e.preventDefault();
				cycleFitMode();
				break;
		}
	}

	function cycleFitMode() {
		const modes: Array<'width' | 'height' | 'original'> = ['width', 'height', 'original'];
		const idx = modes.indexOf(fitMode);
		fitMode = modes[(idx + 1) % modes.length];
	}

	function goBack() {
		if (issue?.series_id) {
			goto(`/library/${issue.series_id}`);
		} else {
			goto('/library');
		}
	}

	// --- Click zone navigation ---
	function handleImageClick(e: MouseEvent) {
		const target = e.currentTarget as HTMLElement;
		const rect = target.getBoundingClientRect();
		const x = e.clientX - rect.left;
		const third = rect.width / 3;

		if (x < third) {
			prevPage();
		} else {
			nextPage();
		}
	}

	// --- Auto-hide controls ---
	function showControlsTemporarily() {
		showControls = true;
		if (controlsTimeout) clearTimeout(controlsTimeout);
		controlsTimeout = setTimeout(() => {
			showControls = false;
		}, 3000);
	}

	// --- Preload adjacent images ---
	$effect(() => {
		if (totalPages > 0) {
			if (currentPage < totalPages - 1) {
				const next = new Image();
				next.src = `/api/v1/reader/${issueId}/pages/${currentPage + 1}`;
			}
			if (currentPage > 0) {
				const prev = new Image();
				prev.src = `/api/v1/reader/${issueId}/pages/${currentPage - 1}`;
			}
		}
	});

	// --- Init ---
	$effect(() => {
		if (issueId) {
			loadReader();
		}
	});
</script>

<svelte:window onkeydown={handleKeydown} />

{#if loading}
	<div class="fixed inset-0 bg-black flex items-center justify-center z-50">
		<div class="text-gray-400 text-lg">Loading...</div>
	</div>
{:else if error}
	<div class="fixed inset-0 bg-black flex items-center justify-center z-50">
		<div class="text-center">
			<p class="text-red-400 text-lg mb-4">{error}</p>
			<button onclick={goBack}
				class="px-4 py-2 bg-amber-500 text-gray-900 font-semibold rounded-lg">
				Go Back
			</button>
		</div>
	</div>
{:else}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="fixed inset-0 bg-black z-50 flex flex-col"
		 onmousemove={showControlsTemporarily}>

		<!-- Top Bar -->
		<div class="absolute top-0 left-0 right-0 z-10 transition-opacity duration-300
			{showControls ? 'opacity-100' : 'opacity-0 pointer-events-none'}">
			<div class="bg-gradient-to-b from-black/80 to-transparent px-4 py-3
				flex items-center justify-between">
				<button onclick={goBack}
					class="text-gray-300 hover:text-white flex items-center gap-2 text-sm"
					title="Back to series">
					<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
							d="M15 19l-7-7 7-7" />
					</svg>
					<span class="truncate max-w-xs">{issue?.series_title} #{issue?.issue_number}</span>
				</button>
				<div class="flex items-center gap-3">
					<!-- Fit mode toggle -->
					<button onclick={cycleFitMode}
						class="text-xs px-2 py-1 rounded bg-gray-800/80 text-gray-300
							hover:text-white border border-gray-600"
						title="Cycle fit mode (F)">
						{fitMode === 'width' ? 'Fit Width' :
						 fitMode === 'height' ? 'Fit Height' : 'Original'}
					</button>
					<!-- Page indicator -->
					<span class="text-sm text-gray-300 tabular-nums">
						{currentPage + 1} / {totalPages}
					</span>
				</div>
			</div>
		</div>

		<!-- Page Image -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<div class="flex-1 overflow-auto flex items-center justify-center cursor-pointer"
			 onclick={handleImageClick}>
			{#if imgLoading}
				<div class="absolute inset-0 flex items-center justify-center pointer-events-none">
					<div class="w-8 h-8 border-2 border-gray-600 border-t-amber-500 rounded-full animate-spin"></div>
				</div>
			{/if}
			<img
				src={pageUrl}
				alt="Page {currentPage + 1}"
				class="select-none
					{fitMode === 'width' ? 'w-full h-auto max-w-none' :
					 fitMode === 'height' ? 'h-full w-auto' :
					 'max-w-none'}"
				draggable="false"
				onload={() => imgLoading = false}
				onerror={() => imgLoading = false}
			/>
		</div>

		<!-- Bottom Bar -->
		<div class="absolute bottom-0 left-0 right-0 z-10 transition-opacity duration-300
			{showControls ? 'opacity-100' : 'opacity-0 pointer-events-none'}">
			<div class="bg-gradient-to-t from-black/80 to-transparent px-4 py-3">
				<!-- Page slider -->
				<div class="flex items-center gap-3">
					<span class="text-xs text-gray-400 w-8 text-right tabular-nums">{currentPage + 1}</span>
					<input
						type="range"
						min="0"
						max={totalPages - 1}
						value={currentPage}
						oninput={(e) => goToPage(Number((e.target as HTMLInputElement).value))}
						class="flex-1 accent-amber-500 h-1"
					/>
					<span class="text-xs text-gray-400 w-8 tabular-nums">{totalPages}</span>
				</div>
				<!-- Keyboard hints -->
				<div class="flex justify-center gap-4 mt-2 text-xs text-gray-600">
					<span>&larr; Prev</span>
					<span>&rarr; / Space Next</span>
					<span>F Fit</span>
					<span>Esc Exit</span>
				</div>
			</div>
		</div>

		<!-- Edge navigation hints (visible on desktop hover) -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="absolute left-0 top-0 bottom-0 w-1/3 z-[5] group cursor-pointer"
			onclick={prevPage}>
			{#if !isFirstPage}
				<div class="absolute left-4 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100
					transition-opacity duration-200">
					<div class="bg-black/60 rounded-full p-2">
						<svg class="w-6 h-6 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
						</svg>
					</div>
				</div>
			{/if}
		</div>
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="absolute right-0 top-0 bottom-0 w-2/3 z-[5] group cursor-pointer"
			onclick={nextPage}>
			{#if !isLastPage}
				<div class="absolute right-4 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100
					transition-opacity duration-200">
					<div class="bg-black/60 rounded-full p-2">
						<svg class="w-6 h-6 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
						</svg>
					</div>
				</div>
			{/if}
		</div>
	</div>
{/if}
