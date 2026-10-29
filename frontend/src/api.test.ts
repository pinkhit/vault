import { expect, it, vi } from 'vitest';
import { deleteDocument, getSession, searchDocuments } from './api';

it('sends deletion with the current session CSRF token and handles an empty success response', async () => {
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: 'current-session-csrf' })),
    )
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  try {
    await getSession();
    await expect(deleteDocument('id/with?separators')).resolves.toBeUndefined();
    const [path, options] = fetchMock.mock.calls[1]!;
    expect(path).toBe('/api/documents/id%2Fwith%3Fseparators');
    expect(options?.method).toBe('DELETE');
    expect(options?.credentials).toBe('same-origin');
    expect(new Headers(options?.headers).get('X-CSRF-Token')).toBe(
      'current-session-csrf',
    );
  } finally {
    vi.unstubAllGlobals();
  }
});

it('encodes search input as one same-origin parameter and propagates cancellation', async () => {
  const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(JSON.stringify({ results: [], has_more: false }), {
      status: 200,
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
  try {
    const controller = new AbortController();
    const query = 'invoice &user_id=other # "合同"';
    await searchDocuments(query, 20, controller.signal);
    const [path, options] = fetchMock.mock.calls[0]!;
    const url = new URL(String(path), 'http://localhost');
    expect(url.pathname).toBe('/api/search');
    expect(url.searchParams.get('q')).toBe(query);
    expect([...url.searchParams.keys()]).toEqual(['q', 'limit', 'offset']);
    expect(options?.credentials).toBe('same-origin');
    expect(options?.signal).toBe(controller.signal);
  } finally {
    vi.unstubAllGlobals();
  }
});
