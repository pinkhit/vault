import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { APIError, getSession, uploadDocument } from './api';
import { deferred } from './test-utils';

class UploadRequest {
  upload = new EventTarget();
  open = vi.fn();
  send = vi.fn();
  setRequestHeader = vi.fn();
  getResponseHeader = vi.fn(() => 'upload-reference');
  withCredentials = false;
  responseType = '';
  status = 201;
  response: unknown = { id: 'file-id', sha256: '0'.repeat(64) };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
}

let xhr: UploadRequest;
let file: File;
beforeEach(async () => {
  xhr = new UploadRequest();
  vi.stubGlobal(
    'XMLHttpRequest',
    vi.fn(function () {
      return xhr;
    }),
  );
  vi.stubGlobal('crypto', {
    subtle: { digest: vi.fn().mockResolvedValue(new Uint8Array(32).buffer) },
  });
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ csrf_token: 'upload-session-token' })),
      ),
  );
  await getSession();
  file = new File(['example'], 'example.txt');
  Object.defineProperty(file, 'arrayBuffer', {
    value: async () => new ArrayBuffer(7),
  });
});
afterEach(() => vi.unstubAllGlobals());

it('keeps the original CSRF token when the session changes during file preparation', async () => {
  const hash = deferred<ArrayBuffer>();
  vi.mocked(crypto.subtle.digest).mockReturnValueOnce(hash.promise);
  const pending = uploadDocument(file);
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response(JSON.stringify({ csrf_token: 'different-session' })),
  );
  await getSession();
  hash.resolve(new Uint8Array(32).buffer);
  await waitFor(() => expect(xhr.send).toHaveBeenCalledOnce());
  expect(xhr.setRequestHeader).toHaveBeenCalledWith(
    'X-CSRF-Token',
    'upload-session-token',
  );
  xhr.status = 403;
  xhr.onload?.();
  await expect(pending).rejects.toMatchObject({ status: 403 });
});

it('reports real byte progress, keeps credentials/CSRF/checksum and waits for server confirmation', async () => {
  const progress = vi.fn();
  let finished = false;
  const pending = uploadDocument(file, progress).then((result) => {
    finished = true;
    return result;
  });
  await waitFor(() => expect(xhr.send).toHaveBeenCalledOnce());
  expect(xhr.open).toHaveBeenCalledWith('POST', '/api/documents');
  expect(xhr.withCredentials).toBe(true);
  expect(xhr.setRequestHeader).toHaveBeenCalledWith(
    'X-CSRF-Token',
    'upload-session-token',
  );
  expect(xhr.setRequestHeader).toHaveBeenCalledWith(
    'X-Checksum-SHA256',
    '0'.repeat(64),
  );
  for (const event of [
    { lengthComputable: true, loaded: 5, total: 20 },
    { lengthComputable: false },
    { lengthComputable: true, loaded: 20, total: 20 },
  ]) {
    xhr.upload.dispatchEvent(new ProgressEvent('progress', event));
  }
  expect(progress.mock.calls).toEqual([[25], [null], [100]]);
  expect(finished).toBe(false);
  xhr.onload?.();
  await expect(pending).resolves.toEqual(xhr.response);
});

it.each([401, 403, 500])(
  'preserves the API error status %s and request reference',
  async (status) => {
    const pending = uploadDocument(file);
    await waitFor(() => expect(xhr.send).toHaveBeenCalledOnce());
    xhr.status = status;
    xhr.response = { error: 'Upload denied' };
    xhr.onload?.();
    await expect(pending).rejects.toMatchObject({
      status,
      message: 'Upload denied Reference: upload-reference',
    });
    await expect(pending).rejects.toBeInstanceOf(APIError);
  },
);

it('fails on network interruption and on a mismatched stored checksum', async () => {
  const first = uploadDocument(file);
  await waitFor(() => expect(xhr.send).toHaveBeenCalledOnce());
  xhr.onerror?.();
  await expect(first).rejects.toThrow('Refresh your library');
  const second = uploadDocument(file);
  await waitFor(() => expect(xhr.send).toHaveBeenCalledTimes(2));
  xhr.response = { sha256: 'wrong' };
  xhr.onload?.();
  await expect(second).rejects.toThrow('verification failed');
});
