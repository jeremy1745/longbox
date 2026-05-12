export interface AuthUser {
	id: number;
	username: string;
	is_admin: boolean;
	created_at: string;
	updated_at: string;
}

let user = $state<AuthUser | null>(null);
let authEnabled = $state(false);
let loading = $state(true);
let checked = $state(false);

export function getAuthState() {
	return {
		get user() { return user; },
		get authEnabled() { return authEnabled; },
		get loading() { return loading; },
		get checked() { return checked; },
	};
}

export async function checkAuthStatus() {
	loading = true;
	try {
		const res = await fetch('/api/v1/auth/status', { credentials: 'include' });
		if (res.ok) {
			const data = await res.json();
			authEnabled = data.auth_enabled;
		}

		if (authEnabled) {
			const meRes = await fetch('/api/v1/auth/me', { credentials: 'include' });
			if (meRes.ok) {
				const meData = await meRes.json();
				user = meData.user;
			} else {
				user = null;
			}
		}
	} catch {
		// Network error — leave state as-is
	} finally {
		loading = false;
		checked = true;
	}
}

export async function login(username: string, password: string): Promise<string | null> {
	const res = await fetch('/api/v1/auth/login', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		credentials: 'include',
		body: JSON.stringify({ username, password }),
	});

	if (!res.ok) {
		const data = await res.json().catch(() => null);
		return data?.error?.message || `HTTP ${res.status}`;
	}

	const data = await res.json();
	user = data.user;
	authEnabled = true;
	return null;
}

export async function logout() {
	await fetch('/api/v1/auth/logout', {
		method: 'POST',
		credentials: 'include',
	}).catch(() => {});
	user = null;
}
