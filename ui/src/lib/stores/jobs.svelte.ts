import { ApiClient, type Job, type JobListResponse, type JobType } from '$lib/api/client';

export const SCAN_JOB_TYPES: JobType[] = ['scan', 'scan_force_cv'];

export function isScanJob(job: Job | null | undefined): job is Job {
  return !!job && SCAN_JOB_TYPES.includes(job.type);
}

let activeJob = $state<Job | null>(null);
let jobs = $state<Job[]>([]);
let initialized = $state(false);
let loading = $state(false);
let eventSource: EventSource | null = null;

export function getJobState() {
  return {
    get activeJob() {
      return activeJob;
    },
    get jobs() {
      return jobs;
    },
    get loading() {
      return loading;
    },
    get initialized() {
      return initialized;
    }
  };
}

export async function ensureJobWatcher() {
  if (initialized || typeof window === 'undefined') return;
  initialized = true;
  await refreshJobs();
  connectSSE();
}

export function setActiveJob(job: Job | null) {
  activeJob = job;
}

function upsertJob(job: Job) {
  const existingIndex = jobs.findIndex((j) => j.id === job.id);
  if (existingIndex >= 0) {
    jobs = [...jobs.slice(0, existingIndex), job, ...jobs.slice(existingIndex + 1)];
  } else {
    jobs = [job, ...jobs].slice(0, 100);
  }
}

async function refreshJobs() {
  loading = true;
  try {
    const data = await ApiClient.get<JobListResponse>('/jobs?limit=50');
    jobs = data.jobs ?? [];
    const runningScan = (data.active ?? []).find(
      (job) => isScanJob(job) && job.status === 'running'
    );
    if (runningScan) {
      activeJob = runningScan;
    } else if (!activeJob) {
      const recentScan = jobs.find((job) => isScanJob(job));
      if (recentScan) {
        activeJob = recentScan;
      }
    }
  } catch (err) {
    console.error('Failed to load jobs', err);
  } finally {
    loading = false;
  }
}

function connectSSE() {
  if (typeof window === 'undefined' || eventSource) return;

  const es = new EventSource('/api/v1/events');
  eventSource = es;

  es.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data);
      if ((payload.type === 'job:updated' || payload.type === 'job:created') && payload.data) {
        const job = payload.data as Job;
        upsertJob(job);
        if (isScanJob(job)) {
          activeJob = job;
        }
      }
    } catch {
      // ignore malformed events
    }
  };

  es.onerror = () => {
    es.close();
    eventSource = null;
    setTimeout(connectSSE, 2000);
  };
}
