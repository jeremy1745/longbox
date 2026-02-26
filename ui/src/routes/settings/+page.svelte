<script lang="ts">
	import { ApiClient, type Settings, type APIKeyTestResult, type OrganizeTemplateResponse, type OrganizeTemplatePreviewResponse, type RenamePreview } from '$lib/api/client';

	let settings = $state<Settings | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Library directory state
	let libraryDirInput = $state('');
	let libraryDirSaving = $state(false);
	let libraryDirMessage = $state<string | null>(null);

	// ComicVine API key state
	let apiKeyInput = $state('');
	let saving = $state(false);
	let testing = $state(false);
	let testResult = $state<APIKeyTestResult | null>(null);
	let saveMessage = $state<string | null>(null);

	// File organization state
	let templateInput = $state('');
	let templateLoading = $state(true);
	let templateSaving = $state(false);
	let templateMessage = $state<string | null>(null);
	let previewSamples = $state<RenamePreview[]>([]);
	let previewLoading = $state(false);
	let previewDebounce: ReturnType<typeof setTimeout> | null = null;

	const defaultTemplate = '{series}/{series} #{number|pad:3}.{format}';

	const variables = [
		{ name: '{series}', desc: 'Series title' },
		{ name: '{sort_series}', desc: 'Sort-friendly title' },
		{ name: '{number}', desc: 'Issue number' },
		{ name: '{title}', desc: 'Issue title' },
		{ name: '{year}', desc: 'Series start year' },
		{ name: '{publisher}', desc: 'Publisher name' },
		{ name: '{format}', desc: 'File extension' },
		{ name: '{cover_date}', desc: 'Cover date' },
		{ name: '{store_date}', desc: 'Store date' },
		{ name: '{writers}', desc: 'First writer' },
		{ name: '{artists}', desc: 'First artist' },
	];

	const filters = [
		{ name: 'pad:N', example: '{number|pad:3}', desc: 'Zero-pad to N digits' },
		{ name: 'lower', example: '{series|lower}', desc: 'Lowercase' },
		{ name: 'upper', example: '{series|upper}', desc: 'Uppercase' },
	];

	async function loadSettings() {
		loading = true;
		error = null;
		try {
			settings = await ApiClient.get<Settings>('/settings');
			if (settings?.library_dir) {
				libraryDirInput = settings.library_dir;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load settings';
		} finally {
			loading = false;
		}
	}

	async function saveLibraryDir() {
		if (!libraryDirInput.trim()) return;
		libraryDirSaving = true;
		libraryDirMessage = null;
		try {
			await ApiClient.put<any>('/settings/library-dir', {
				library_dir: libraryDirInput.trim()
			});
			libraryDirMessage = 'Library directory updated! A scan has been started.';
			await loadSettings();
		} catch (e) {
			libraryDirMessage = e instanceof Error ? e.message : 'Failed to save library directory';
		} finally {
			libraryDirSaving = false;
		}
	}

	async function loadTemplate() {
		templateLoading = true;
		try {
			const data = await ApiClient.get<OrganizeTemplateResponse>('/library/organize/template');
			templateInput = data.template;
		} catch {
			templateInput = defaultTemplate;
		} finally {
			templateLoading = false;
		}
	}

	async function saveTemplate() {
		if (!templateInput.trim()) return;
		templateSaving = true;
		templateMessage = null;
		try {
			await ApiClient.put('/library/organize/template', { template: templateInput.trim() });
			templateMessage = 'Template saved successfully!';
		} catch (e) {
			templateMessage = e instanceof Error ? e.message : 'Failed to save template';
		} finally {
			templateSaving = false;
		}
	}

	async function loadPreviewSamples() {
		if (!templateInput.trim()) {
			previewSamples = [];
			return;
		}
		previewLoading = true;
		try {
			const data = await ApiClient.post<OrganizeTemplatePreviewResponse>('/library/organize/preview-template', {
				template: templateInput.trim()
			});
			previewSamples = data.samples || [];
		} catch {
			previewSamples = [];
		} finally {
			previewLoading = false;
		}
	}

	function onTemplateInput() {
		if (previewDebounce) clearTimeout(previewDebounce);
		previewDebounce = setTimeout(() => loadPreviewSamples(), 500);
	}

	function insertVariable(varName: string) {
		templateInput += varName;
		onTemplateInput();
	}

	function basename(path: string): string {
		return path.split('/').pop() || path;
	}

	async function saveAPIKey() {
		if (!apiKeyInput.trim()) return;
		saving = true;
		saveMessage = null;
		testResult = null;
		try {
			const result = await ApiClient.put<any>('/settings/comicvine-api-key', {
				api_key: apiKeyInput.trim()
			});
			apiKeyInput = '';
			saveMessage = 'API key saved successfully!';
			await loadSettings();
		} catch (e) {
			saveMessage = e instanceof Error ? e.message : 'Failed to save API key';
		} finally {
			saving = false;
		}
	}

	async function testAPIKey() {
		testing = true;
		testResult = null;
		try {
			testResult = await ApiClient.post<APIKeyTestResult>('/settings/comicvine-api-key/test');
		} catch (e) {
			testResult = {
				valid: false,
				message: e instanceof Error ? e.message : 'Test failed'
			};
		} finally {
			testing = false;
		}
	}

	$effect(() => {
		loadSettings();
		loadTemplate();
	});
</script>

<div class="space-y-8">
	<div>
		<h1 class="text-3xl font-bold">Settings</h1>
		<p class="text-gray-400 mt-1">Configure LongBox</p>
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
	{:else}
		<!-- Library Directory Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Library Directory</h2>
			<p class="text-sm text-gray-400 mb-6">
				The folder where LongBox looks for your comic book files (CBZ, CBR, CB7, PDF).
				All subfolders are scanned automatically.
			</p>

			<!-- Current path display -->
			<div class="mb-6">
				<div class="flex items-center gap-3">
					<span class="text-sm text-gray-400">Current path:</span>
					<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
						{settings?.library_dir || 'Not set'}
					</code>
				</div>
			</div>

			<!-- Directory input -->
			<div class="space-y-3">
				<label for="library-dir" class="block text-sm font-medium text-gray-300">
					Change Library Directory
				</label>
				<div class="flex gap-3">
					<input
						id="library-dir"
						type="text"
						bind:value={libraryDirInput}
						placeholder="/path/to/your/comics"
						class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
							text-gray-100 placeholder-gray-500 font-mono text-sm
							focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
						onkeydown={(e) => e.key === 'Enter' && saveLibraryDir()}
					/>
					<button
						onclick={saveLibraryDir}
						disabled={libraryDirSaving || !libraryDirInput.trim() || libraryDirInput.trim() === settings?.library_dir}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
					>
						{libraryDirSaving ? 'Saving...' : 'Save & Scan'}
					</button>
				</div>
			</div>

			{#if libraryDirMessage}
				<p class="mt-3 text-sm {libraryDirMessage.includes('updated') ? 'text-green-400' : 'text-red-400'}">
					{libraryDirMessage}
				</p>
			{/if}
		</div>

		<!-- File Organization Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">File Organization</h2>
				<a
					href="/settings/organize"
					class="px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-200 text-sm font-medium rounded-lg transition-colors"
				>
					Preview & Organize
				</a>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Define a naming template to organize your comic files into a consistent folder structure.
				Use <code class="text-amber-400">/</code> in the template to create subdirectories.
			</p>

			<!-- Template input -->
			{#if templateLoading}
				<div class="text-gray-400 text-sm">Loading template...</div>
			{:else}
				<div class="space-y-4">
					<div>
						<label for="naming-template" class="block text-sm font-medium text-gray-300 mb-2">
							Naming Template
						</label>
						<div class="flex gap-3">
							<input
								id="naming-template"
								type="text"
								bind:value={templateInput}
								oninput={onTemplateInput}
								placeholder={defaultTemplate}
								class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
									text-gray-100 placeholder-gray-500 font-mono text-sm
									focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
							/>
							<button
								onclick={saveTemplate}
								disabled={templateSaving || !templateInput.trim()}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
									disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
							>
								{templateSaving ? 'Saving...' : 'Save'}
							</button>
						</div>
						{#if templateMessage}
							<p class="mt-2 text-sm {templateMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">
								{templateMessage}
							</p>
						{/if}
					</div>

					<!-- Variable chips -->
					<div>
						<p class="text-xs text-gray-400 mb-2">Click to insert variable:</p>
						<div class="flex flex-wrap gap-1.5">
							{#each variables as v}
								<button
									onclick={() => insertVariable(v.name)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-amber-400 rounded font-mono transition-colors"
									title={v.desc}
								>
									{v.name}
								</button>
							{/each}
						</div>
					</div>

					<!-- Filters reference -->
					<div>
						<p class="text-xs text-gray-400 mb-2">Filters (pipe syntax):</p>
						<div class="flex flex-wrap gap-3 text-xs text-gray-500">
							{#each filters as f}
								<span>
									<code class="text-gray-300">{f.example}</code> — {f.desc}
								</span>
							{/each}
						</div>
					</div>

					<!-- Live preview -->
					{#if previewSamples.length > 0}
						<div class="mt-4 pt-4 border-t border-gray-700">
							<p class="text-xs text-gray-400 mb-2">Preview (sample renames):</p>
							<div class="space-y-2">
								{#each previewSamples as sample}
									<div class="text-xs bg-gray-900/50 rounded p-2 space-y-1">
										<div class="text-gray-500 truncate" title={sample.current_path}>
											{basename(sample.current_path)}
										</div>
										<div class="text-green-400 truncate" title={sample.new_path}>
											&rarr; {sample.new_path.split('/').slice(-2).join('/')}
										</div>
									</div>
								{/each}
							</div>
						</div>
					{:else if previewLoading}
						<div class="mt-4 pt-4 border-t border-gray-700">
							<p class="text-xs text-gray-400">Loading preview...</p>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- ComicVine API Key Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">ComicVine API</h2>
			<p class="text-sm text-gray-400 mb-6">
				Connect to ComicVine to fetch rich metadata for your comics including descriptions,
				creators, cover art, and complete issue lists.
				<a href="https://comicvine.gamespot.com/api/" target="_blank" rel="noopener"
					class="text-amber-400 hover:text-amber-300">Get a free API key</a>.
			</p>

			<!-- Current status -->
			<div class="mb-6 space-y-2">
				<div class="flex items-center gap-3">
					<span class="text-sm text-gray-400">Status:</span>
					{#if settings?.comicvine_api_key_set}
						<span class="inline-flex items-center gap-1.5 text-sm text-green-400">
							<span class="w-2 h-2 bg-green-400 rounded-full"></span>
							Connected
						</span>
					{:else}
						<span class="inline-flex items-center gap-1.5 text-sm text-yellow-400">
							<span class="w-2 h-2 bg-yellow-400 rounded-full"></span>
							Not configured
						</span>
					{/if}
				</div>

				{#if settings?.comicvine_api_key_set}
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Key:</span>
						<code class="text-sm text-gray-300 bg-gray-700 px-2 py-0.5 rounded">
							{settings.comicvine_api_key_masked}
						</code>
					</div>
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Source:</span>
						<span class="text-sm text-gray-300 capitalize">{settings.comicvine_api_key_source}</span>
					</div>
					<div class="flex items-center gap-3">
						<span class="text-sm text-gray-400">Hourly requests remaining:</span>
						<span class="text-sm text-gray-300">{settings.comicvine_hourly_remaining}</span>
					</div>
				{/if}
			</div>

			<!-- API Key input -->
			<div class="space-y-3">
				<label for="api-key" class="block text-sm font-medium text-gray-300">
					{settings?.comicvine_api_key_set ? 'Update API Key' : 'Enter API Key'}
				</label>
				<div class="flex gap-3">
					<input
						id="api-key"
						type="password"
						bind:value={apiKeyInput}
						placeholder="Enter your ComicVine API key"
						class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
							text-gray-100 placeholder-gray-500
							focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
						onkeydown={(e) => e.key === 'Enter' && saveAPIKey()}
					/>
					<button
						onclick={saveAPIKey}
						disabled={saving || !apiKeyInput.trim()}
						class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
					>
						{saving ? 'Saving...' : 'Save'}
					</button>
				</div>
			</div>

			{#if saveMessage}
				<p class="mt-3 text-sm {saveMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">
					{saveMessage}
				</p>
			{/if}

			<!-- Test button -->
			{#if settings?.comicvine_api_key_set}
				<div class="mt-4 pt-4 border-t border-gray-700">
					<button
						onclick={testAPIKey}
						disabled={testing}
						class="px-4 py-2 bg-gray-700 hover:bg-gray-600 disabled:bg-gray-600
							disabled:cursor-not-allowed text-gray-200 font-medium rounded-lg transition-colors text-sm"
					>
						{testing ? 'Testing...' : 'Test API Key'}
					</button>

					{#if testResult}
						<div class="mt-3 p-3 rounded-lg {testResult.valid ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
							<p class="text-sm {testResult.valid ? 'text-green-400' : 'text-red-400'}">
								{testResult.message}
							</p>
							{#if testResult.valid && testResult.hourly_remaining !== undefined}
								<p class="text-xs text-gray-400 mt-1">
									{testResult.hourly_remaining} hourly requests remaining
								</p>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- About Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">About</h2>
			<div class="space-y-2 text-sm text-gray-400">
				<p><span class="text-gray-300 font-medium">LongBox</span> — Comic Library Manager</p>
				<p>Metadata provided by <a href="https://comicvine.gamespot.com" target="_blank" rel="noopener" class="text-amber-400 hover:text-amber-300">ComicVine</a></p>
			</div>
		</div>
	{/if}
</div>
