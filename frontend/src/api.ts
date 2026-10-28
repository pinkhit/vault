export interface DocumentRecord {
  id: string;
  name: string;
  size: number;
  media_type: string;
  sha256: string;
  created_at: string;
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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
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
    throw new Error(`${message}${id ? ` Reference: ${id}` : ''}`);
  }
  return response.json() as Promise<T>;
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
