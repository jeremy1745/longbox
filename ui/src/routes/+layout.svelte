<script lang="ts">
	import '../app.css';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { checkAuthStatus, getAuthState, logout } from '$lib/stores/auth.svelte';
	import { ensureJobWatcher, getJobState, isScanJob } from '$lib/stores/jobs.svelte';
	let { children } = $props();

	const auth = getAuthState();
	const jobState = getJobState();

	const navLinks = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/library', label: 'Library' },
		{ href: '/wanted', label: 'Wanted' },
		{ href: '/backlog', label: 'Backlog' },
		{ href: '/downloads', label: 'Downloads' },
		{ href: '/story-arcs', label: 'Story Arcs' },
		{ href: '/calendar', label: 'Pull List' },
		{ href: '/jobs', label: 'Jobs' },
		{ href: '/settings', label: 'Settings' },
	];

	function isActive(href: string, currentPath: string): boolean {
		if (href === '/') return currentPath === '/';
		return currentPath.startsWith(href);
	}

	const isAuthPage = $derived(
		$page.url.pathname === '/login' || $page.url.pathname === '/setup'
	);

	const showJobBanner = $derived(
		!isAuthPage &&
		$page.url.pathname !== '/' &&
		!!jobState.activeJob &&
		isScanJob(jobState.activeJob) &&
		jobState.activeJob.status === 'running'
	);

	async function handleLogout() {
		await logout();
		goto('/login');
	}

	$effect(() => {
		checkAuthStatus();
	});

	onMount(() => {
		ensureJobWatcher();
	});

	// Auth redirect logic
	$effect(() => {
		if (!auth.checked) return;
		const path = $page.url.pathname;

		if (auth.authEnabled && !auth.user && path !== '/login' && path !== '/setup') {
			goto('/login');
		} else if (auth.user && path === '/login') {
			goto('/');
		} else if (auth.authEnabled && path === '/setup') {
			goto('/login');
		}
	});
</script>

{#if !auth.checked}
	<div class="min-h-screen bg-gray-900"></div>
{:else if isAuthPage}
	{@render children()}
{:else}
	<div class="min-h-screen bg-gray-900 text-gray-100">
		<nav class="bg-gray-800 border-b border-gray-700">
			<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
				<div class="flex items-center justify-between h-16">
					<div class="flex items-center gap-6">
						<a href="/" class="flex items-center gap-2">
							<span class="text-2xl font-bold text-amber-400">LongBox</span>
						</a>
						<div class="flex items-center gap-1">
							{#each navLinks as link}
								<a
									href={link.href}
									class="px-3 py-2 rounded-md text-sm font-medium transition-colors
										{isActive(link.href, $page.url.pathname)
											? 'bg-gray-900 text-white'
											: 'text-gray-300 hover:bg-gray-700 hover:text-white'}"
								>
									{link.label}
								</a>
							{/each}
						</div>
					</div>
					{#if auth.authEnabled && auth.user}
						<div class="flex items-center gap-3">
							<span class="text-sm text-gray-400">{auth.user.username}</span>
							<button
								onclick={handleLogout}
								class="px-3 py-1.5 text-sm bg-gray-700 hover:bg-gray-600 text-gray-300 rounded-lg transition-colors"
							>
								Logout
							</button>
						</div>
					{/if}
				</div>
			</div>
		</nav>
		{#if showJobBanner}
			<div class="bg-amber-500/10 border-b border-amber-500/40">
				<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-3 flex items-center gap-4">
					<div>
						<p class="text-sm font-semibold text-amber-300">Library scan running…</p>
						{#if jobState.activeJob?.message}
							<p class="text-xs text-amber-200/80 mt-1 truncate">{jobState.activeJob.message}</p>
						{/if}
					</div>
					<div class="flex-1 flex items-center gap-3">
						<div class="w-full bg-gray-800 rounded-full h-2">
							<div
								class="bg-amber-500 h-2 rounded-full transition-all duration-300"
								style="width: {Math.min(jobState.activeJob?.progress ?? 0, 100)}%"
							></div>
						</div>
						<span class="text-xs text-amber-200/80 w-12 text-right">
							{jobState.activeJob?.progress ?? 0}%
						</span>
					</div>
				</div>
			</div>
		{/if}
		<main class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
			{@render children()}
		</main>
	</div>
{/if}
