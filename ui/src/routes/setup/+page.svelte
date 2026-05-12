<script lang="ts">
	import { goto } from '$app/navigation';
	import { login, getAuthState } from '$lib/stores/auth.svelte';

	const auth = getAuthState();

	let username = $state('');
	let password = $state('');
	let confirmPassword = $state('');
	let error = $state<string | null>(null);
	let submitting = $state(false);

	async function handleSetup(e: Event) {
		e.preventDefault();
		if (!username.trim() || !password) return;

		if (password !== confirmPassword) {
			error = 'Passwords do not match';
			return;
		}

		if (password.length < 8) {
			error = 'Password must be at least 8 characters';
			return;
		}

		submitting = true;
		error = null;

		try {
			const res = await fetch('/api/v1/auth/register', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify({ username: username.trim(), password }),
			});

			if (!res.ok) {
				const data = await res.json().catch(() => null);
				error = data?.error?.message || `HTTP ${res.status}`;
				submitting = false;
				return;
			}

			// Auto-login after registration
			const loginErr = await login(username.trim(), password);
			if (loginErr) {
				error = loginErr;
				submitting = false;
				return;
			}

			goto('/');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Registration failed';
			submitting = false;
		}
	}

	$effect(() => {
		if (auth.checked && auth.authEnabled) {
			goto('/login');
		}
	});
</script>

<div class="min-h-screen bg-gray-900 flex items-center justify-center px-4">
	<div class="w-full max-w-sm">
		<div class="text-center mb-8">
			<h1 class="text-3xl font-bold text-amber-400">LongBox</h1>
			<p class="text-gray-400 mt-2">Create your admin account</p>
		</div>

		<form onsubmit={handleSetup} class="bg-gray-800 rounded-lg border border-gray-700 p-6 space-y-4">
			<p class="text-sm text-gray-400">
				This will be the first user account with admin privileges.
				Authentication will be enabled after creating this account.
			</p>

			{#if error}
				<div class="bg-red-900/30 border border-red-700 rounded-lg p-3">
					<p class="text-sm text-red-400">{error}</p>
				</div>
			{/if}

			<div>
				<label for="username" class="block text-sm font-medium text-gray-300 mb-1">Username</label>
				<input
					id="username"
					type="text"
					bind:value={username}
					autocomplete="username"
					autofocus
					class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
						text-gray-100 placeholder-gray-500
						focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
				/>
			</div>

			<div>
				<label for="password" class="block text-sm font-medium text-gray-300 mb-1">Password</label>
				<input
					id="password"
					type="password"
					bind:value={password}
					autocomplete="new-password"
					class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
						text-gray-100 placeholder-gray-500
						focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
				/>
			</div>

			<div>
				<label for="confirm-password" class="block text-sm font-medium text-gray-300 mb-1">Confirm Password</label>
				<input
					id="confirm-password"
					type="password"
					bind:value={confirmPassword}
					autocomplete="new-password"
					class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg
						text-gray-100 placeholder-gray-500
						focus:outline-none focus:ring-2 focus:ring-amber-500 focus:border-transparent"
				/>
			</div>

			<button
				type="submit"
				disabled={submitting || !username.trim() || !password || !confirmPassword}
				class="w-full px-4 py-2 bg-amber-500 hover:bg-amber-600 disabled:bg-gray-600
					disabled:cursor-not-allowed text-gray-900 font-semibold rounded-lg transition-colors"
			>
				{submitting ? 'Creating Account...' : 'Create Admin Account'}
			</button>
		</form>
	</div>
</div>
