<script lang="ts">
	import {
		ApiClient,
		type Settings,
		type APIKeyTestResult,
		type OrganizeTemplateResponse,
		type OrganizeTemplatePreviewResponse,
		type RenamePreview,
		type Indexer,
		type IndexerListResponse,
		type IndexerTestResult,
		type DownloadClient,
		type DownloadClientListResponse,
		type DownloadClientTestResult,
		type SlackSettings,
		type SlackTestResult,
	} from '$lib/api/client';
	import { getAuthState, type AuthUser } from '$lib/stores/auth.svelte';

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

	// Indexer state
	let indexers = $state<Indexer[]>([]);
	let indexerEditing = $state<Partial<Indexer> | null>(null);
	let indexerSaving = $state(false);
	let indexerMessage = $state<string | null>(null);
	let indexerTesting = $state<number | null>(null);
	let indexerTestResult = $state<IndexerTestResult | null>(null);

	// Download client state
	let dlClients = $state<DownloadClient[]>([]);
	let dlClientEditing = $state<Partial<DownloadClient> | null>(null);
	let dlClientSaving = $state(false);
	let dlClientMessage = $state<string | null>(null);
	let dlClientTesting = $state<number | null>(null);
	let dlClientTestResult = $state<DownloadClientTestResult | null>(null);

	// Pull list schedule state
	let pullListSaving = $state(false);
	let pullListMessage = $state<string | null>(null);

	// Slack notification state
	let slackSettings = $state<SlackSettings | null>(null);
	let slackSaving = $state(false);
	let slackMessage = $state<string | null>(null);
	let slackWebhookInput = $state('');
	let slackTesting = $state(false);
	let slackTestResult = $state<SlackTestResult | null>(null);

	// Mylar3 metadata state
	let mylarWriting = $state(false);
	let mylarMessage = $state<string | null>(null);

	const auth = getAuthState();

	// User management state
	let users = $state<AuthUser[]>([]);
	let userEditing = $state<{ username: string; password: string } | null>(null);
	let userSaving = $state(false);
	let userMessage = $state<string | null>(null);
	let passwordChanging = $state<number | null>(null);
	let newPasswordInput = $state('');
	let passwordMessage = $state<string | null>(null);

	const defaultTemplate = '{series}/{series} #{number|pad:3}.{format}';

	const dayNames = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

	const variables = [
		{ name: '{series}', desc: 'Series title' },
		{ name: '{sort_series}', desc: 'Sort-friendly title' },
		{ name: '{number}', desc: 'Issue number' },
		{ name: '{title}', desc: 'Issue title' },
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

	async function writeMylarMetadata() {
		mylarWriting = true;
		mylarMessage = null;
		try {
			const result = await ApiClient.post<{ job_id: number; total_series: number; message: string }>(
				'/library/write-mylar-metadata'
			);
			mylarMessage = `${result.message} (${result.total_series} series, Job #${result.job_id})`;
		} catch (e) {
			mylarMessage = e instanceof Error ? e.message : 'Failed to start';
		} finally {
			mylarWriting = false;
		}
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

	// --- Indexer functions ---

	async function loadIndexers() {
		try {
			const data = await ApiClient.get<IndexerListResponse>('/indexers');
			indexers = data.indexers || [];
		} catch { /* ignore */ }
	}

	function newIndexer() {
		indexerEditing = { name: '', url: '', api_key: '', type: 'newznab', priority: 50, categories: '7030' };
		indexerMessage = null;
	}

	function editIndexer(idx: Indexer) {
		indexerEditing = { ...idx, api_key: '' };
		indexerMessage = null;
	}

	async function saveIndexer() {
		if (!indexerEditing) return;
		indexerSaving = true;
		indexerMessage = null;
		try {
			if (indexerEditing.id) {
				const body: Record<string, any> = {};
				if (indexerEditing.name) body.name = indexerEditing.name;
				if (indexerEditing.url) body.url = indexerEditing.url;
				if (indexerEditing.api_key) body.api_key = indexerEditing.api_key;
				if (indexerEditing.type) body.type = indexerEditing.type;
				if (indexerEditing.priority != null) body.priority = indexerEditing.priority;
				if (indexerEditing.categories) body.categories = indexerEditing.categories;
				await ApiClient.put(`/indexers/${indexerEditing.id}`, body);
			} else {
				await ApiClient.post('/indexers', indexerEditing);
			}
			indexerEditing = null;
			await loadIndexers();
		} catch (e) {
			indexerMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			indexerSaving = false;
		}
	}

	async function deleteIndexer(id: number) {
		try {
			await ApiClient.delete(`/indexers/${id}`);
			await loadIndexers();
		} catch (e) {
			indexerMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function testIndexer(id: number) {
		indexerTesting = id;
		indexerTestResult = null;
		try {
			indexerTestResult = await ApiClient.post<IndexerTestResult>(`/indexers/${id}/test`);
		} catch (e) {
			indexerTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			indexerTesting = null;
		}
	}

	// --- Download Client functions ---

	async function loadDlClients() {
		try {
			const data = await ApiClient.get<DownloadClientListResponse>('/download-clients');
			dlClients = data.download_clients || [];
		} catch { /* ignore */ }
	}

	function newDlClient() {
		dlClientEditing = { name: '', url: '', api_key: '', type: 'sabnzbd', category: 'comics', priority: 50 };
		dlClientMessage = null;
	}

	function editDlClient(dc: DownloadClient) {
		dlClientEditing = { ...dc, api_key: '' };
		dlClientMessage = null;
	}

	async function saveDlClient() {
		if (!dlClientEditing) return;
		dlClientSaving = true;
		dlClientMessage = null;
		try {
			if (dlClientEditing.id) {
				const body: Record<string, any> = {};
				if (dlClientEditing.name) body.name = dlClientEditing.name;
				if (dlClientEditing.url) body.url = dlClientEditing.url;
				if (dlClientEditing.api_key) body.api_key = dlClientEditing.api_key;
				if (dlClientEditing.category) body.category = dlClientEditing.category;
				if (dlClientEditing.priority != null) body.priority = dlClientEditing.priority;
				await ApiClient.put(`/download-clients/${dlClientEditing.id}`, body);
			} else {
				await ApiClient.post('/download-clients', dlClientEditing);
			}
			dlClientEditing = null;
			await loadDlClients();
		} catch (e) {
			dlClientMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			dlClientSaving = false;
		}
	}

	async function deleteDlClient(id: number) {
		try {
			await ApiClient.delete(`/download-clients/${id}`);
			await loadDlClients();
		} catch (e) {
			dlClientMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	async function testDlClient(id: number) {
		dlClientTesting = id;
		dlClientTestResult = null;
		try {
			dlClientTestResult = await ApiClient.post<DownloadClientTestResult>(`/download-clients/${id}/test`);
		} catch (e) {
			dlClientTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			dlClientTesting = null;
		}
	}

	// --- Pull List Schedule ---

	async function savePullListSchedule(field: string, value: any) {
		pullListSaving = true;
		pullListMessage = null;
		try {
			await ApiClient.put('/settings/pull-list-schedule', { [field]: value });
			await loadSettings();
			pullListMessage = 'Schedule updated!';
		} catch (e) {
			pullListMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			pullListSaving = false;
		}
	}

	// --- Slack notification functions ---

	async function loadSlackSettings() {
		try {
			slackSettings = await ApiClient.get<SlackSettings>('/settings/slack');
		} catch { /* ignore */ }
	}

	async function saveSlackSetting(key: string, value: boolean) {
		slackSaving = true;
		slackMessage = null;
		try {
			await ApiClient.put('/settings/slack', { [key]: value });
			await loadSlackSettings();
			slackMessage = 'Setting updated!';
		} catch (e) {
			slackMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			slackSaving = false;
		}
	}

	async function saveSlackWebhook() {
		if (!slackWebhookInput.trim()) return;
		slackSaving = true;
		slackMessage = null;
		try {
			await ApiClient.put('/settings/slack', { slack_webhook_url: slackWebhookInput.trim() });
			slackWebhookInput = '';
			await loadSlackSettings();
			slackMessage = 'Webhook URL saved!';
		} catch (e) {
			slackMessage = e instanceof Error ? e.message : 'Save failed';
		} finally {
			slackSaving = false;
		}
	}

	async function testSlackWebhook() {
		slackTesting = true;
		slackTestResult = null;
		try {
			slackTestResult = await ApiClient.post<SlackTestResult>('/settings/slack/test');
		} catch (e) {
			slackTestResult = { success: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			slackTesting = false;
		}
	}

	const slackEventToggles = [
		{ key: 'slack_notify_scan_complete', label: 'Scan Complete', desc: 'When a library scan finishes' },
		{ key: 'slack_notify_metadata_refresh_complete', label: 'Metadata Refresh Complete', desc: 'When a metadata refresh finishes' },
		{ key: 'slack_notify_pull_list_search_complete', label: 'Pull List Search Complete', desc: 'When a pull list search finishes' },
		{ key: 'slack_notify_download_grabbed', label: 'Download Grabbed', desc: 'When an NZB is grabbed from an indexer' },
		{ key: 'slack_notify_download_complete', label: 'Download Complete', desc: 'When a download finishes successfully' },
		{ key: 'slack_notify_download_failed', label: 'Download Failed', desc: 'When a download fails' },
	];

	// --- User management functions ---

	async function loadUsers() {
		try {
			const data = await ApiClient.get<{ users: AuthUser[] }>('/auth/users');
			users = data.users || [];
		} catch { /* ignore */ }
	}

	function newUser() {
		userEditing = { username: '', password: '' };
		userMessage = null;
	}

	async function saveUser() {
		if (!userEditing) return;
		userSaving = true;
		userMessage = null;
		try {
			await ApiClient.post('/auth/users', userEditing);
			userEditing = null;
			await loadUsers();
			userMessage = 'User created successfully!';
		} catch (e) {
			userMessage = e instanceof Error ? e.message : 'Failed to create user';
		} finally {
			userSaving = false;
		}
	}

	async function deleteUser(id: number) {
		userMessage = null;
		try {
			await ApiClient.delete(`/auth/users/${id}`);
			await loadUsers();
		} catch (e) {
			userMessage = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	function startPasswordChange(id: number) {
		passwordChanging = id;
		newPasswordInput = '';
		passwordMessage = null;
	}

	async function savePasswordChange() {
		if (passwordChanging == null || !newPasswordInput) return;
		passwordMessage = null;
		try {
			await ApiClient.put(`/auth/users/${passwordChanging}/password`, { password: newPasswordInput });
			passwordChanging = null;
			newPasswordInput = '';
			passwordMessage = 'Password changed successfully!';
		} catch (e) {
			passwordMessage = e instanceof Error ? e.message : 'Failed to change password';
		}
	}

	$effect(() => {
		loadSettings();
		loadTemplate();
		loadIndexers();
		loadDlClients();
		loadSlackSettings();
		if (auth.user?.is_admin) {
			loadUsers();
		}
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

		<!-- Mylar3 Metadata Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Mylar3 Metadata</h2>
			<p class="text-sm text-gray-400 mb-6">
				Write Mylar3-compatible metadata files to each series folder. Creates a
				<code class="text-amber-400 bg-gray-900 px-1 rounded">cvinfo</code> file (ComicVine URL) and downloads a
				<code class="text-amber-400 bg-gray-900 px-1 rounded">poster.jpg</code> (series cover image)
				for every series matched to ComicVine.
			</p>

			<button
				onclick={writeMylarMetadata}
				disabled={mylarWriting}
				class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
			>
				{mylarWriting ? 'Starting...' : 'Write Mylar3 Metadata'}
			</button>

			{#if mylarMessage}
				<p class="mt-3 text-sm {mylarMessage.includes('Failed') ? 'text-red-400' : 'text-green-400'}">
					{mylarMessage}
				</p>
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

		<!-- Indexers Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">Indexers</h2>
				<button
					onclick={newIndexer}
					class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
				>
					Add Indexer
				</button>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Configure Usenet indexers (Newznab, NZBHydra2, Prowlarr) to search for comics.
			</p>

			{#if indexerMessage}
				<p class="mb-4 text-sm text-red-400">{indexerMessage}</p>
			{/if}

			{#if indexerTestResult}
				<div class="mb-4 p-3 rounded-lg {indexerTestResult.success ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
					<p class="text-sm {indexerTestResult.success ? 'text-green-400' : 'text-red-400'}">{indexerTestResult.message}</p>
				</div>
			{/if}

			<!-- Indexer edit form -->
			{#if indexerEditing}
				<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Name</label>
							<input type="text" bind:value={indexerEditing.name} placeholder="My Indexer"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Type</label>
							<select bind:value={indexerEditing.type}
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500">
								<option value="newznab">Newznab</option>
								<option value="nzbhydra2">NZBHydra2</option>
								<option value="prowlarr">Prowlarr</option>
							</select>
						</div>
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">URL</label>
						<input type="text" bind:value={indexerEditing.url} placeholder="https://my-indexer.com"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">API Key</label>
						<input type="password" bind:value={indexerEditing.api_key} placeholder={indexerEditing.id ? '(leave blank to keep current)' : 'API key'}
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Categories</label>
							<input type="text" bind:value={indexerEditing.categories} placeholder="7030"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Priority</label>
							<input type="number" bind:value={indexerEditing.priority} min="1" max="100"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
					</div>
					<div class="flex gap-2 pt-2">
						<button onclick={saveIndexer} disabled={indexerSaving}
							class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
							{indexerSaving ? 'Saving...' : 'Save'}
						</button>
						<button onclick={() => indexerEditing = null}
							class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
							Cancel
						</button>
					</div>
				</div>
			{/if}

			<!-- Indexer list -->
			{#if indexers.length > 0}
				<div class="divide-y divide-gray-700">
					{#each indexers as idx (idx.id)}
						<div class="py-3 flex items-center justify-between gap-4">
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-gray-200">{idx.name}</span>
									<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">{idx.type}</span>
									{#if !idx.enabled}
										<span class="text-xs px-1.5 py-0.5 rounded bg-red-900/50 text-red-400">Disabled</span>
									{/if}
								</div>
								<p class="text-xs text-gray-500 mt-0.5 truncate font-mono">{idx.url}</p>
							</div>
							<div class="flex items-center gap-1">
								<button onclick={() => testIndexer(idx.id)} disabled={indexerTesting === idx.id}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									{indexerTesting === idx.id ? '...' : 'Test'}
								</button>
								<button onclick={() => editIndexer(idx)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									Edit
								</button>
								<button onclick={() => deleteIndexer(idx.id)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
									Delete
								</button>
							</div>
						</div>
					{/each}
				</div>
			{:else if !indexerEditing}
				<p class="text-sm text-gray-500">No indexers configured. Add one to enable Usenet search.</p>
			{/if}
		</div>

		<!-- Download Clients Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">Download Clients</h2>
				<button
					onclick={newDlClient}
					class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
				>
					Add Client
				</button>
			</div>
			<p class="text-sm text-gray-400 mb-6">
				Configure SABnzbd to download grabbed NZBs.
			</p>

			{#if dlClientMessage}
				<p class="mb-4 text-sm text-red-400">{dlClientMessage}</p>
			{/if}

			{#if dlClientTestResult}
				<div class="mb-4 p-3 rounded-lg {dlClientTestResult.success ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}">
					<p class="text-sm {dlClientTestResult.success ? 'text-green-400' : 'text-red-400'}">{dlClientTestResult.message}</p>
				</div>
			{/if}

			<!-- Download client edit form -->
			{#if dlClientEditing}
				<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
					<div>
						<label class="block text-xs text-gray-400 mb-1">Name</label>
						<input type="text" bind:value={dlClientEditing.name} placeholder="My SABnzbd"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">URL</label>
						<input type="text" bind:value={dlClientEditing.url} placeholder="http://localhost:8080/sabnzbd"
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div>
						<label class="block text-xs text-gray-400 mb-1">API Key</label>
						<input type="password" bind:value={dlClientEditing.api_key} placeholder={dlClientEditing.id ? '(leave blank to keep current)' : 'API key'}
							class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
					</div>
					<div class="grid grid-cols-2 gap-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Category</label>
							<input type="text" bind:value={dlClientEditing.category} placeholder="comics"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Priority</label>
							<input type="number" bind:value={dlClientEditing.priority} min="1" max="100"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
					</div>
					<div class="flex gap-2 pt-2">
						<button onclick={saveDlClient} disabled={dlClientSaving}
							class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
							{dlClientSaving ? 'Saving...' : 'Save'}
						</button>
						<button onclick={() => dlClientEditing = null}
							class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
							Cancel
						</button>
					</div>
				</div>
			{/if}

			<!-- Download client list -->
			{#if dlClients.length > 0}
				<div class="divide-y divide-gray-700">
					{#each dlClients as dc (dc.id)}
						<div class="py-3 flex items-center justify-between gap-4">
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-gray-200">{dc.name}</span>
									<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">{dc.type}</span>
									{#if dc.category}
										<span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">cat: {dc.category}</span>
									{/if}
									{#if !dc.enabled}
										<span class="text-xs px-1.5 py-0.5 rounded bg-red-900/50 text-red-400">Disabled</span>
									{/if}
								</div>
								<p class="text-xs text-gray-500 mt-0.5 truncate font-mono">{dc.url}</p>
							</div>
							<div class="flex items-center gap-1">
								<button onclick={() => testDlClient(dc.id)} disabled={dlClientTesting === dc.id}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									{dlClientTesting === dc.id ? '...' : 'Test'}
								</button>
								<button onclick={() => editDlClient(dc)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
									Edit
								</button>
								<button onclick={() => deleteDlClient(dc.id)}
									class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
									Delete
								</button>
							</div>
						</div>
					{/each}
				</div>
			{:else if !dlClientEditing}
				<p class="text-sm text-gray-500">No download clients configured. Add SABnzbd to enable NZB grabbing.</p>
			{/if}
		</div>

		<!-- Pull List Automation Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Pull List Automation</h2>
			<p class="text-sm text-gray-400 mb-6">
				Automatically search for and download missing issues from tracked series on a weekly schedule.
			</p>

			{#if pullListMessage}
				<p class="mb-4 text-sm {pullListMessage.includes('updated') || pullListMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{pullListMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<!-- Enable toggle -->
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Weekly Search</p>
						<p class="text-xs text-gray-500 mt-0.5">Automatically search indexers for wanted issues</p>
					</div>
					<button
						onclick={() => savePullListSchedule('enabled', !settings?.pull_list_enabled)}
						disabled={pullListSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{settings?.pull_list_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{settings?.pull_list_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if settings?.pull_list_enabled}
					<!-- Day selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Day</label>
						<select
							value={settings?.pull_list_day ?? 3}
							onchange={(e) => savePullListSchedule('day', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							{#each dayNames as name, i}
								<option value={i}>{name}</option>
							{/each}
						</select>
					</div>

					<!-- Hour selector -->
					<div class="flex items-center gap-4">
						<label class="text-sm text-gray-300 w-20">Hour</label>
						<select
							value={settings?.pull_list_hour ?? 6}
							onchange={(e) => savePullListSchedule('hour', parseInt((e.target as HTMLSelectElement).value))}
							class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500"
						>
							{#each Array.from({length: 24}, (_, i) => i) as hour}
								<option value={hour}>{hour === 0 ? '12 AM' : hour < 12 ? `${hour} AM` : hour === 12 ? '12 PM' : `${hour - 12} PM`}</option>
							{/each}
						</select>
					</div>

					<!-- Last run info -->
					{#if settings?.pull_list_last_run}
						<div class="flex items-center gap-3 pt-2 border-t border-gray-700">
							<span class="text-sm text-gray-400">Last run:</span>
							<span class="text-sm text-gray-300">{settings.pull_list_last_run}</span>
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- Notifications Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<h2 class="text-xl font-semibold mb-4">Notifications</h2>
			<p class="text-sm text-gray-400 mb-6">
				Send notifications to a Slack channel when key events occur.
			</p>

			{#if slackMessage}
				<p class="mb-4 text-sm {slackMessage.includes('updated') || slackMessage.includes('saved') || slackMessage.includes('Saved') || slackMessage.includes('Updated') ? 'text-green-400' : 'text-red-400'}">
					{slackMessage}
				</p>
			{/if}

			<div class="space-y-4">
				<!-- Global enable toggle -->
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-medium text-gray-200">Enable Slack Notifications</p>
						<p class="text-xs text-gray-500 mt-0.5">Send event notifications to a Slack channel</p>
					</div>
					<button
						onclick={() => saveSlackSetting('slack_enabled', !slackSettings?.slack_enabled)}
						disabled={slackSaving}
						class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
							{slackSettings?.slack_enabled ? 'bg-amber-500' : 'bg-gray-600'}"
					>
						<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
							{slackSettings?.slack_enabled ? 'translate-x-6' : 'translate-x-1'}"></span>
					</button>
				</div>

				{#if slackSettings?.slack_enabled}
					<!-- Webhook URL -->
					<div class="space-y-2">
						<label class="text-sm font-medium text-gray-300">Webhook URL</label>
						<div class="flex gap-2">
							<input
								type="password"
								bind:value={slackWebhookInput}
								placeholder={slackSettings?.slack_webhook_set ? '••••••••••••' : 'https://hooks.slack.com/services/...'}
								class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 placeholder:text-gray-500 focus:outline-none focus:ring-2 focus:ring-amber-500"
							/>
							<button
								onclick={saveSlackWebhook}
								disabled={slackSaving || !slackWebhookInput.trim()}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 disabled:text-gray-400 text-gray-900 text-sm font-semibold rounded transition-colors"
							>
								Save
							</button>
						</div>
						{#if slackSettings?.slack_webhook_set}
							<p class="text-xs text-green-400">Configured</p>
						{/if}
					</div>

					<!-- Test button -->
					{#if slackSettings?.slack_webhook_set}
						<div class="flex items-center gap-3">
							<button
								onclick={testSlackWebhook}
								disabled={slackTesting}
								class="px-4 py-2 bg-gray-600 hover:bg-gray-500 disabled:bg-gray-700 disabled:text-gray-500 text-gray-100 text-sm rounded transition-colors"
							>
								{slackTesting ? 'Sending...' : 'Send Test Message'}
							</button>
							{#if slackTestResult}
								<span class="text-sm {slackTestResult.success ? 'text-green-400' : 'text-red-400'}">
									{slackTestResult.message}
								</span>
							{/if}
						</div>

						<!-- Per-event toggles -->
						<div class="pt-4 border-t border-gray-700 space-y-3">
							<p class="text-sm font-medium text-gray-300">Event Notifications</p>
							{#each slackEventToggles as toggle}
								<div class="flex items-center justify-between">
									<div>
										<p class="text-sm text-gray-200">{toggle.label}</p>
										<p class="text-xs text-gray-500 mt-0.5">{toggle.desc}</p>
									</div>
									<button
										onclick={() => saveSlackSetting(toggle.key, !(slackSettings?.toggles?.[toggle.key] ?? true))}
										disabled={slackSaving}
										class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors
											{(slackSettings?.toggles?.[toggle.key] ?? true) ? 'bg-amber-500' : 'bg-gray-600'}"
									>
										<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform
											{(slackSettings?.toggles?.[toggle.key] ?? true) ? 'translate-x-6' : 'translate-x-1'}"></span>
									</button>
								</div>
							{/each}
						</div>
					{/if}
				{/if}
			</div>
		</div>

		<!-- User Management Section -->
		<div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-xl font-semibold">User Management</h2>
				{#if auth.user?.is_admin}
					<button
						onclick={newUser}
						class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 text-gray-900 text-sm font-semibold rounded-lg transition-colors"
					>
						Add User
					</button>
				{/if}
			</div>

			{#if !auth.authEnabled}
				<p class="text-sm text-gray-400 mb-4">
					Authentication is currently disabled. Create an admin account to enable it.
				</p>
				<a
					href="/setup"
					class="inline-block px-4 py-2 bg-amber-500 hover:bg-amber-600 text-gray-900 font-semibold rounded-lg transition-colors text-sm"
				>
					Enable Authentication
				</a>
			{:else if auth.user?.is_admin}
				<p class="text-sm text-gray-400 mb-6">
					Manage user accounts. Only admins can add or remove users.
				</p>

				{#if userMessage}
					<p class="mb-4 text-sm {userMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">{userMessage}</p>
				{/if}

				{#if passwordMessage}
					<p class="mb-4 text-sm {passwordMessage.includes('success') ? 'text-green-400' : 'text-red-400'}">{passwordMessage}</p>
				{/if}

				<!-- New user form -->
				{#if userEditing}
					<div class="mb-6 p-4 bg-gray-900/50 rounded-lg border border-gray-600 space-y-3">
						<div>
							<label class="block text-xs text-gray-400 mb-1">Username</label>
							<input type="text" bind:value={userEditing.username} placeholder="username"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div>
							<label class="block text-xs text-gray-400 mb-1">Password</label>
							<input type="password" bind:value={userEditing.password} placeholder="minimum 8 characters"
								autocomplete="new-password"
								class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
						</div>
						<div class="flex gap-2 pt-2">
							<button onclick={saveUser} disabled={userSaving || !userEditing.username.trim() || !userEditing.password}
								class="px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded-lg text-sm transition-colors">
								{userSaving ? 'Creating...' : 'Create User'}
							</button>
							<button onclick={() => userEditing = null}
								class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded-lg text-sm transition-colors">
								Cancel
							</button>
						</div>
					</div>
				{/if}

				<!-- User list -->
				{#if users.length > 0}
					<div class="divide-y divide-gray-700">
						{#each users as u (u.id)}
							<div class="py-3">
								<div class="flex items-center justify-between gap-4">
									<div class="flex-1 min-w-0">
										<div class="flex items-center gap-2">
											<span class="font-medium text-gray-200">{u.username}</span>
											{#if u.is_admin}
												<span class="text-xs px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-400">Admin</span>
											{/if}
										</div>
									</div>
									<div class="flex items-center gap-1">
										<button onclick={() => startPasswordChange(u.id)}
											class="px-2 py-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 rounded transition-colors">
											Password
										</button>
										{#if u.id !== auth.user?.id}
											<button onclick={() => deleteUser(u.id)}
												class="px-2 py-1 text-xs bg-gray-700 hover:bg-red-900/50 text-gray-300 hover:text-red-400 rounded transition-colors">
												Delete
											</button>
										{/if}
									</div>
								</div>

								<!-- Inline password change -->
								{#if passwordChanging === u.id}
									<div class="mt-3 flex gap-2">
										<input type="password" bind:value={newPasswordInput} placeholder="New password (min 8 chars)"
											autocomplete="new-password"
											class="flex-1 px-3 py-1.5 bg-gray-700 border border-gray-600 rounded text-sm text-gray-100 focus:outline-none focus:ring-2 focus:ring-amber-500" />
										<button onclick={savePasswordChange} disabled={!newPasswordInput || newPasswordInput.length < 8}
											class="px-3 py-1.5 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600 text-gray-900 font-semibold rounded text-sm transition-colors">
											Save
										</button>
										<button onclick={() => passwordChanging = null}
											class="px-3 py-1.5 bg-gray-700 hover:bg-gray-600 text-gray-200 rounded text-sm transition-colors">
											Cancel
										</button>
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			{:else}
				<p class="text-sm text-gray-400">Authentication is enabled. Contact an admin to manage users.</p>
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
