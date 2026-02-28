<script lang="ts">
	import '../app.css';
	import { page } from '$app/stores';
	let { children } = $props();

	const navLinks = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/library', label: 'Library' },
		{ href: '/wanted', label: 'Wanted' },
		{ href: '/downloads', label: 'Downloads' },
		{ href: '/calendar', label: 'Pull List' },
		{ href: '/jobs', label: 'Jobs' },
		{ href: '/settings', label: 'Settings' },
	];

	function isActive(href: string, currentPath: string): boolean {
		if (href === '/') return currentPath === '/';
		return currentPath.startsWith(href);
	}
</script>

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
			</div>
		</div>
	</nav>
	<main class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
		{@render children()}
	</main>
</div>
