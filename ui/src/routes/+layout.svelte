<script lang="ts">
	import '../app.css';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { checkAuthStatus, getAuthState, logout } from '$lib/stores/auth.svelte';
	let { children } = $props();

	const auth = getAuthState();

	const navLinks = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/library', label: 'Library' },
		{ href: '/files', label: 'Files' },
		{ href: '/wanted', label: 'Wanted' },
		{ href: '/downloads', label: 'Downloads' },
		{ href: '/story-arcs', label: 'Story Arcs' },
		{ href: '/browse', label: 'Browse' },
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

	async function handleLogout() {
		await logout();
		goto('/login');
	}

	$effect(() => {
		checkAuthStatus();
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
		<main class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
			{@render children()}
		</main>
	</div>
{/if}
