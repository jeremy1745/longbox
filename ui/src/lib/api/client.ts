const BASE_URL = '/api/v1';

export interface ApiError {
	code: string;
	message: string;
}

export class ApiClient {
	static async get<T>(path: string): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`);
		if (!res.ok) {
			const body = await res.json().catch(() => null);
			throw new Error(body?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async post<T>(path: string, body?: unknown): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'POST',
			headers: body ? { 'Content-Type': 'application/json' } : {},
			body: body ? JSON.stringify(body) : undefined
		});
		if (!res.ok) {
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async put<T>(path: string, body?: unknown): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: body ? JSON.stringify(body) : undefined
		});
		if (!res.ok) {
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async delete<T>(path: string): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'DELETE'
		});
		if (!res.ok) {
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}
}

// --- Types ---

export interface ComicFile {
	id: number;
	issue_id?: number;
	file_path: string;
	file_name: string;
	file_size: number;
	file_format: string;
	cover_path?: string;
	parsed_series?: string;
	parsed_number?: string;
	parsed_year?: number;
	created_at: string;
	updated_at: string;
}

export interface Series {
	id: number;
	title: string;
	sort_title: string;
	year?: number;
	publisher_name?: string;
	comicvine_id?: number;
	description?: string;
	status: string;
	total_issues: number;
	cover_file_id?: number;
	tracked: boolean;
	issue_count: number;
	file_count: number;
	created_at: string;
	updated_at: string;
}

export interface Issue {
	id: number;
	series_id: number;
	issue_number: string;
	sort_number: number;
	title?: string;
	description?: string;
	cover_date?: string;
	store_date?: string;
	writers?: string;
	artists?: string;
	read_status: 'unread' | 'reading' | 'read';
	last_read_page?: number;
	has_file: boolean;
	file_id?: number;
	series_title?: string;
	created_at: string;
	updated_at: string;
}

// --- Response types ---

export interface FileListResponse {
	files: ComicFile[];
	total: number;
	page: number;
	per_page: number;
}

export interface SeriesListResponse {
	series: Series[];
	total: number;
	page: number;
	per_page: number;
}

export interface IssueListResponse {
	issues: Issue[];
	total: number;
}

export interface ScanResult {
	files_found: number;
	files_added: number;
	files_skipped: number;
	series_created: number;
	issues_created: number;
	errors: number;
}

export interface LibraryStats {
	total_files: number;
	total_series: number;
}

// --- ComicVine / Metadata types ---

export interface MetadataSearchResult {
	comicvine_id: number;
	name: string;
	start_year: string;
	issue_count: number;
	publisher: string;
	description: string;
	image_url: string;
	resource_type: string;
	match_score: number;
}

export interface MetadataSearchResponse {
	results: MetadataSearchResult[];
	total: number;
	page: number;
}

export interface Settings {
	comicvine_api_key_masked: string;
	comicvine_api_key_source: string;
	comicvine_api_key_set: boolean;
	comicvine_hourly_remaining: number;
	library_dir: string;
}

export interface APIKeyTestResult {
	valid: boolean;
	message: string;
	hourly_remaining?: number;
}

// --- Job types ---

export type JobType = 'scan' | 'metadata_refresh';
export type JobStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';

export interface Job {
	id: number;
	type: JobType;
	status: JobStatus;
	progress: number;
	total_items: number;
	processed_items: number;
	message?: string;
	started_at?: string;
	completed_at?: string;
	created_at: string;
}

export interface JobListResponse {
	jobs: Job[];
	active: Job[];
}

// --- Want List types ---

export interface WantListItem {
	id: number;
	issue_id: number;
	priority: number;
	notes?: string;
	added_at: string;
	issue_number: string;
	series_id: number;
	series_title: string;
	cover_url?: string;
	store_date?: string;
	cover_date?: string;
}

export interface WantListResponse {
	items: WantListItem[];
	total: number;
	page: number;
	per_page: number;
}

// --- Calendar types ---

export interface CalendarResponse {
	issues: Issue[];
	total: number;
}

// --- File Organization types ---

export interface RenamePreview {
	file_id: number;
	current_path: string;
	new_path: string;
	status: 'move' | 'skip' | 'conflict' | 'unlinked';
	reason?: string;
}

export interface OrganizePreviewResponse {
	previews: RenamePreview[];
	total: number;
	moves: number;
	skips: number;
	conflicts: number;
	unlinked: number;
}

export interface OrganizeTemplatePreviewResponse {
	samples: RenamePreview[];
	total: number;
}

export interface RenameResult {
	total_files: number;
	moved: number;
	skipped: number;
	errors: number;
	error_details?: string[];
}

export interface OrganizeTemplateResponse {
	template: string;
}

// --- Reader types ---

export interface PageInfo {
	index: number;
	name: string;
	size: number;
}

export interface ReaderPagesResponse {
	issue_id: number;
	file_id: number;
	page_count: number;
	pages: PageInfo[];
	last_read_page: number | null;
}

export interface ProgressUpdateResponse {
	last_read_page: number;
	read_status: 'unread' | 'reading' | 'read';
}

// --- SSE Event types ---

export interface SSEEvent {
	type: string;
	data: any;
}
