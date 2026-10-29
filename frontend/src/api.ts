export interface DocumentRecord {
  id: string;
  name: string;
  size: number;
  media_type: string;
  sha256: string;
  created_at: string;
  owner_id: string;
}
export interface DocumentPage {
  documents: DocumentRecord[];
  has_more: boolean;
}
export interface Health {
  status: string;
  mode: string;
  max_upload_bytes: number;
}

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
    const message =
      typeof body === 'object' &&
      body !== null &&
      'error' in body &&
      typeof body.error === 'string'
        ? body.error
        : `Request failed (${response.status}).`;
    const id = response.headers.get('X-Request-ID');
    throw new APIError(
      `${message}${id ? ` Reference: ${id}` : ''}`,
      response.status,
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

export async function uploadDocument(file: File): Promise<DocumentRecord> {
  const digest = await crypto.subtle.digest(
    'SHA-256',
    await file.arrayBuffer(),
  );
  const checksum = Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('');
  const body = new FormData();
  body.append('file', file);
  const document = await request<DocumentRecord>('/api/documents', {
    method: 'POST',
    body,
    headers: { 'X-Checksum-SHA256': checksum },
  });
  if (document.sha256 !== checksum) {
    throw new Error(
      'Upload was stored but checksum confirmation failed. Refresh before retrying.',
    );
  }
  return document;
}
