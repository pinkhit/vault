export interface DocumentRecord {
  id: string;
  name: string;
  size: number;
  media_type: string;
  sha256: string;
  created_at: string;
  owner_id: string;
  indexing_status?: IndexingState;
}

export type IndexingState =
  'pending' | 'processing' | 'ready' | 'failed' | 'unsupported';

export interface SearchHit {
  document: DocumentRecord;
  snippet: string;
  match: 'filename' | 'metadata' | 'content';
  page?: number;
  section?: number;
}

export interface SearchPage {
  results: SearchHit[];
  has_more: boolean;
}

export const SEARCH_PAGE_SIZE = 20;
export const MAX_SEARCH_OFFSET = 10000;
export interface DocumentPage {
  documents: DocumentRecord[];
  has_more: boolean;
}

export interface Preview {
  kind: 'text' | 'json' | 'csv' | 'image' | 'pdf' | 'xlsx';
  text?: string;
  rows?: string[][];
  image?: string;
  page?: number;
  pages?: number;
  sheet_name?: string;
  truncated: boolean;
}

export const getPreview = (id: string, page: number, signal?: AbortSignal) =>
  request<Preview>(
    `/api/documents/${encodeURIComponent(id)}/preview?page=${page}`,
    { signal },
  );
export interface Health {
  status: string;
  mode: string;
  max_upload_bytes: number;
}

export interface StorageReport {
  host_storage?: {
    version: number;
    collected_at: string;
    total_bytes: number;
    free_bytes: number;
    available_including_reclaimable_bytes: number;
  } | null;
  growth_available_bytes?: number | null;
  documents: {
    count: number;
    logical_bytes: number;
    by_type: { media_type: string; count: number; logical_bytes: number }[];
  };
  filesystem: {
    source: 'container-filesystem' | 'native-filesystem';
    checked_at: string;
    capacity: {
      total_bytes: number;
      used_bytes: number;
      available_bytes: number;
      reserved_bytes: number;
      low_space: boolean;
    } | null;
  };
}

export const getStorageOverview = (signal?: AbortSignal) =>
  request<StorageReport>('/api/admin/storage', { signal });

export interface User {
  id: string;
  username: string;
  is_admin: boolean;
  enabled: boolean;
}

export interface Session {
  user: User;
  csrf_token: string;
  expires_at: string;
}

export class APIError extends Error {
  constructor(
    message: string,
    public readonly status: number,
  ) {
    super(message);
  }
}

// Only the CSRF token is readable by JavaScript; the session cookie is HttpOnly.
// Neither value is stored in localStorage or sessionStorage.
let csrfToken = '';

function responseError(
  body: unknown,
  status: number,
  id: string | null,
): APIError {
  const message =
    typeof body === 'object' &&
    body !== null &&
    'error' in body &&
    typeof body.error === 'string'
      ? body.error
      : `Request failed (${status}).`;
  return new APIError(`${message}${id ? ` Reference: ${id}` : ''}`, status);
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.method && !['GET', 'HEAD'].includes(init.method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken);
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  });
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => null);
    throw responseError(
      body,
      response.status,
      response.headers.get('X-Request-ID'),
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export async function getSession(signal?: AbortSignal): Promise<Session> {
  const session = await request<Session>('/api/auth/session', { signal });
  if (!signal?.aborted) csrfToken = session.csrf_token;
  return session;
}

export async function login(
  username: string,
  password: string,
): Promise<Session> {
  const session = await request<Session>('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  csrfToken = session.csrf_token;
  return session;
}

export async function logout(allDevices = false): Promise<void> {
  await request<void>(
    allDevices ? '/api/auth/revoke-sessions' : '/api/auth/logout',
    { method: 'POST' },
  );
  csrfToken = '';
}

export const getHealth = (signal?: AbortSignal) =>
  request<Health>('/api/health', { signal });
export const listDocuments = (offset: number, signal?: AbortSignal) =>
  request<DocumentPage>(`/api/documents?limit=50&offset=${offset}`, { signal });

export const deleteDocument = (id: string) =>
  request<void>(`/api/documents/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });

export const searchDocuments = (
  query: string,
  offset: number,
  signal?: AbortSignal,
) => {
  const parameters = new URLSearchParams({
    q: query,
    limit: String(SEARCH_PAGE_SIZE),
    offset: String(offset),
  });
  return request<SearchPage>(`/api/search?${parameters}`, { signal });
};

export async function uploadDocument(
  file: File,
  onProgress?: (percent: number | null) => void,
): Promise<DocumentRecord> {
  // Capture this session's token before hashing; a later sign-in must not inherit this upload.
  const token = csrfToken;
  const digest = await crypto.subtle.digest(
    'SHA-256',
    await file.arrayBuffer(),
  );
  const checksum = Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
  const body = new FormData();
  body.append('file', file);
  const document = await new Promise<DocumentRecord>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.upload.addEventListener('progress', (event) => {
      onProgress?.(
        event.lengthComputable && event.total > 0
          ? Math.min(100, Math.floor((event.loaded / event.total) * 100))
          : null,
      );
    });
    xhr.open('POST', '/api/documents');
    xhr.withCredentials = true;
    xhr.responseType = 'json';
    if (token) xhr.setRequestHeader('X-CSRF-Token', token);
    xhr.setRequestHeader('X-Checksum-SHA256', checksum);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300)
        resolve(xhr.response as DocumentRecord);
      else
        reject(
          responseError(
            xhr.response,
            xhr.status,
            xhr.getResponseHeader('X-Request-ID'),
          ),
        );
    };
    xhr.onerror = xhr.onabort = () =>
      reject(
        new Error(
          'Upload interrupted. Refresh your library before trying again.',
        ),
      );
    xhr.send(body);
  });
  if (document.sha256 !== checksum) {
    throw new Error(
      'Your file was uploaded, but verification failed. Refresh your library before trying again.',
    );
  }
  return document;
}
