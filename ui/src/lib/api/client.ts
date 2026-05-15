const BASE_URL = '/api/v1';

export interface ApiError {
	code: string;
	message: string;
}

function handleResponse(res: Response): void {
	if (res.status === 401 && typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
		window.location.href = '/login';
	}
}

export class ApiClient {
	static async get<T>(path: string): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			credentials: 'include'
		});
		if (!res.ok) {
			handleResponse(res);
			const body = await res.json().catch(() => null);
			throw new Error(body?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async post<T>(path: string, body?: unknown): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'POST',
			headers: body ? { 'Content-Type': 'application/json' } : {},
			body: body ? JSON.stringify(body) : undefined,
			credentials: 'include'
		});
		if (!res.ok) {
			handleResponse(res);
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async put<T>(path: string, body?: unknown): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: body ? JSON.stringify(body) : undefined,
			credentials: 'include'
		});
		if (!res.ok) {
			handleResponse(res);
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async delete<T>(path: string): Promise<T> {
		const res = await fetch(`${BASE_URL}${path}`, {
			method: 'DELETE',
			credentials: 'include'
		});
		if (!res.ok) {
			handleResponse(res);
			const data = await res.json().catch(() => null);
			throw new Error(data?.error?.message || `HTTP ${res.status}`);
		}
		return res.json();
	}

	static async shutdownServer(): Promise<void> {
		try {
			await fetch(`${BASE_URL}/admin/shutdown`, {
				method: 'POST',
				credentials: 'include'
			});
		} catch {
			// Expected — server may close connection during shutdown
		}
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
	cover_image_url?: string;
	tracked: boolean;
	issue_count: number;
	file_count: number;
	parent_series_id?: number;
	annual_series?: Series[];
	metron_id?: number;
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
	cover_url?: string;
	writers?: string;
	artists?: string;
	read_status: 'unread' | 'reading' | 'read';
	skip_status?: 'skipped' | 'ignored' | null;
	last_read_page?: number;
	has_file: boolean;
	file_id?: number;
	series_title?: string;
	created_at: string;
	updated_at: string;
}

export interface CalendarRefreshResponse {
	job_id: number;
	tracked_series: number;
	matched_series: number;
	message: string;
}

export interface TrackFromPullListResponse {
	series: Series;
	tracked: boolean;
	want_list_added: number;
}

// --- Response types ---

export interface FileRenameResponse extends ComicFile {}

export interface SeriesFilesResponse {
	files: ComicFile[];
}

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
	files_removed: number;
	series_created: number;
	issues_created: number;
	series_refreshed: number;
	issues_newly_missing: number;
	backlog_runs_created: number;
	errors: number;
}

export interface LibraryStats {
	total_files: number;
	total_series: number;
}

// --- ComicVine / Metadata types ---

export interface MetadataSearchResult {
	comicvine_id?: number;
	metron_id?: number;
	sources?: Array<'comicvine' | 'metron'>;
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
	metron_username: string;
	metron_token_masked: string;
	metron_token_set: boolean;
	metron_burst_remaining: number;
	metron_sustained_remaining: number;
	prowlarr_url: string;
	prowlarr_category: string;
	prowlarr_configured: boolean;
	library_dir: string;
	pull_list_enabled: boolean;
	pull_list_day: number;
	pull_list_hour: number;
	pull_list_last_run: string;
	auto_search_on_add: boolean;
	auto_scan_enabled: boolean;
	auto_scan_interval: number;
	auto_scan_last_run: string;
	missing_search_enabled: boolean;
	missing_search_interval: number;
	missing_search_last_run: string;
	post_process_script: string;
	scan_auto_queue_backlog: boolean;
	scan_cv_refresh_ttl_hours: number;
	backup_on_start: boolean;
	backup_retention: number;
}

export interface APIKeyTestResult {
	valid: boolean;
	message: string;
	hourly_remaining?: number;
}

// --- Job types ---

export type JobType = 'scan' | 'scan_force_cv' | 'metadata_refresh' | 'search' | 'pull_list_search' | 'longbox_metadata' | 'mylar_metadata' | 'missing_search' | 'hash_backfill' | 'folder_images' | 'reorganize' | 'adopt_folders';
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

export type ProcurementStatus = 'none' | 'pending' | 'submitted' | 'acquired' | 'failed';

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
	procurement_status?: ProcurementStatus;
	procurement_submitted_at?: string;
	procurement_last_error?: string;
}

// Result of POST /pull-list/want-track — the full want+track acquisition flow.
export interface WantTrackResult {
	series_id: number;
	folder_path: string;
	metadata_written: boolean;
	files_moved: number;
	issues_queued: number;
	warnings?: string[];
}

// Body of a 409 MERGE_REQUIRED from /pull-list/want-track. Note: requested_series_id
// is omitted when the conflict fires before a series row exists (the want-track case).
export interface MergeConflictBody {
	error: { code: string; message: string };
	requested_series_id?: number;
	conflicting_series_id: number;
	conflicting_series_title: string;
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

export interface PullListIssue {
	comicvine_id?: number;
	comicvine_url?: string;
	series_name: string;
	series_cv_id?: number;
	issue_number: string;
	title?: string;
	description?: string;
	store_date: string;
	cover_date?: string;
	cover_url?: string;
	writers?: string;
	artists?: string;
	publisher?: string;
	local_series_id?: number;
	local_issue_id?: number;
	has_file: boolean;
	file_id?: number;
	tracked: boolean;
	wanted: boolean;
}

export interface ReleaseDebugInfo {
	source: string;
	walksoftly_count: number;
	walksoftly_error?: string;
	cv_fallback_count?: number;
	local_count: number;
	total_results: number;
	tracked_count: number;
	week_num?: number;
}

export interface ReleasesResponse {
	releases: PullListIssue[];
	total: number;
	debug?: ReleaseDebugInfo;
}

// --- ComicVine Volume Issues (browse preview) ---

export interface ComicVineIssue {
	comicvine_id: number;
	issue_number: string;
	title?: string;
	cover_date?: string;
	store_date?: string;
	cover_url?: string;
	description?: string;
}

export interface VolumeIssuesResponse {
	issues: ComicVineIssue[];
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

// --- Indexer types ---

export interface Indexer {
	id: number;
	name: string;
	url: string;
	api_key: string;
	type: 'newznab' | 'nzbhydra2' | 'prowlarr';
	priority: number;
	enabled: boolean;
	categories: string;
	created_at: string;
	updated_at: string;
}

export interface IndexerListResponse {
	indexers: Indexer[];
}

export interface IndexerTestResult {
	success: boolean;
	message: string;
}

// --- Download Client types ---

export interface DownloadClient {
	id: number;
	name: string;
	type: 'sabnzbd';
	url: string;
	api_key: string;
	category: string;
	priority: number;
	enabled: boolean;
	created_at: string;
	updated_at: string;
}

export interface DownloadClientListResponse {
	download_clients: DownloadClient[];
}

export interface DownloadClientTestResult {
	success: boolean;
	message: string;
	version?: string;
}

// --- Search Result types ---

export interface SearchResult {
	title: string;
	nzb_url: string;
	guid: string;
	size: number;
	publish_date: string;
	grabs: number;
	indexer_name: string;
	indexer_id: number;
	score: number;
}

export interface SearchResponse {
	results: SearchResult[];
	total: number;
}

// --- Download History types ---

export type DownloadStatus = 'grabbed' | 'downloading' | 'completed' | 'failed' | 'import_failed';

export interface DownloadHistoryItem {
	id: number;
	issue_id?: number;
	indexer_id?: number;
	download_client_id?: number;
	nzb_name: string;
	nzb_url: string;
	nzb_guid: string;
	external_id: string;
	status: DownloadStatus;
	size: number;
	message?: string;
	grabbed_at: string;
	completed_at?: string;
	series_title?: string;
	issue_number?: string;
	indexer_name?: string;
	created_at: string;
	updated_at: string;
}

export interface DownloadHistoryResponse {
	items: DownloadHistoryItem[];
	total: number;
	page: number;
	per_page: number;
}

// --- Slack Settings types ---

export interface SlackSettings {
	slack_enabled: boolean;
	slack_bot_token: string;
	slack_token_set: boolean;
	slack_channel: string;
	toggles: Record<string, boolean>;
}

export interface SlackTestResult {
	success: boolean;
	message: string;
}

// --- SSE Event types ---

export interface SSEEvent {
	type: string;
	data: any;
}

export interface WriteMetadataResult {
	file_id: number;
	file_name: string;
	success: boolean;
	message: string;
	skipped: boolean;
}

export interface WriteMetadataResponse {
	results: WriteMetadataResult[];
	total: number;
	succeeded: number;
	failed: number;
	skipped: number;
}

// --- ComicVine raw search result (used for story arc search) ---

export interface CVSearchResult {
	id: number;
	name: string;
	description?: string;
	image?: { small_url?: string; thumb_url?: string };
	resource_type?: string;
}

// --- Story Arc types ---

export interface StoryArc {
	id: number;
	name: string;
	comicvine_id?: number;
	description?: string;
	issue_count: number;
	owned_count: number;
	created_at: string;
	updated_at: string;
}

export interface StoryArcIssue {
	story_arc_id: number;
	issue_id: number;
	sequence_number?: number;
	series_title?: string;
	issue_number?: string;
	cover_url?: string;
	has_file: boolean;
	read_status?: string;
}

export interface StoryArcListResponse {
	story_arcs: StoryArc[];
	total: number;
}

export interface StoryArcDetailResponse {
	story_arc: StoryArc;
	issues: StoryArcIssue[];
}

// --- Blocklist types ---

export interface BlocklistEntry {
	id: number;
	nzb_guid: string;
	nzb_name: string;
	reason: string;
	blocked_at: string;
}

export interface BlocklistResponse {
	items: BlocklistEntry[];
	total: number;
	page: number;
	per_page: number;
}

// --- Backup types ---

export interface BackupInfo {
	name: string;
	size: number;
	created_at: string;
}

export interface BackupListResponse {
	backups: BackupInfo[];
}

// --- Backlog types ---

export type BacklogRunStatus = 'planning' | 'ready' | 'attention' | 'paused' | 'completed';

export interface BacklogRun {
	id: number;
	series_id: number;
	series_title: string;
	status: BacklogRunStatus | string;
	include_variants: boolean;
	total_issues: number;
	queued_issues: number;
	completed_issues: number;
	failed_issues: number;
	paused: boolean;
	created_at: string;
	updated_at: string;
}

export type BacklogItemStatus =
	| 'pending'
	| 'searching'
	| 'queued'
	| 'downloading'
	| 'completed'
	| 'failed'
	| 'error'
	| 'canceled';

export interface BacklogItem {
	id: number;
	backlog_run_id: number;
	series_id: number;
	issue_id: number;
	issue_number?: string;
	series_title?: string;
	variant_name?: string;
	priority: number;
	status: BacklogItemStatus | string;
	retry_count: number;
	retry_at?: string;
	last_error?: string;
	sab_nzo_id?: string;
	nzb_guid?: string;
	download_history_id?: number;
	created_at: string;
	updated_at: string;
}

export interface BacklogRunListResponse {
	items: BacklogRun[];
	total: number;
	page: number;
	per_page: number;
}

export interface BacklogItemListResponse {
	items: BacklogItem[];
	total: number;
	page: number;
	per_page: number;
}

// --- Duplicate types ---

export interface DuplicateGroup {
	key: string;
	files: ComicFile[];
}

export interface DuplicatesResponse {
	by_hash: DuplicateGroup[];
	by_issue: DuplicateGroup[];
	total_hash_dupes: number;
	total_issue_dupes: number;
}
