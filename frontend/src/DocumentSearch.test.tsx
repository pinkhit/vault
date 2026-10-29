import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import DocumentSearch from './DocumentSearch';
import { deferred } from './test-utils';
import {
  APIError,
  searchDocuments,
  type SearchHit,
  type SearchPage,
} from './api';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  searchDocuments: vi.fn(),
}));

const document = {
  id: 'a'.repeat(32),
  owner_id: 'other-user',
  name: 'Shared invoice.pdf',
  size: 42,
  media_type: 'application/pdf',
  sha256: 'b'.repeat(64),
  created_at: '2026-01-01T00:00:00Z',
  indexing_status: 'ready' as const,
};
const hit: SearchHit = {
  document,
  snippet: 'Invoice DEMO-2026-001',
  match: 'content',
  page: 3,
  section: 2,
};
const results: SearchPage = {
  results: [hit],
  has_more: false,
};
const onUnauthorized = vi.fn();
const onClear = vi.fn();

function view(revision = 0) {
  return (
    <DocumentSearch
      revision={revision}
      onUnauthorized={onUnauthorized}
      onClear={onClear}
      onPreview={vi.fn()}
    >
      <p>Library content</p>
    </DocumentSearch>
  );
}

function submit(query: string) {
  fireEvent.change(
    screen.getByRole('searchbox', { name: 'Search documents' }),
    { target: { value: query } },
  );
  fireEvent.click(screen.getByRole('button', { name: /^Search$/ }));
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(searchDocuments).mockResolvedValue(results);
});

it('submits only on request, renders shared sources/references and clears to the library', async () => {
  render(view());
  expect(screen.getByText('Library content')).toBeVisible();
  expect(searchDocuments).not.toHaveBeenCalled();
  submit('DEMO-2026-001');
  expect(
    await screen.findByRole('link', { name: 'Download Shared invoice.pdf' }),
  ).toHaveAttribute('href', `/api/documents/${document.id}/download`);
  expect(screen.getByText('Invoice DEMO-2026-001')).toBeVisible();
  expect(screen.getByText(/Page 3 · Section 2/)).toBeVisible();
  expect(screen.getByText('Text searchable')).toBeVisible();
  expect(screen.queryByText('Library content')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
  expect(screen.getByText('Library content')).toBeVisible();
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
  expect(screen.getByRole('searchbox')).toHaveFocus();
  expect(onClear).toHaveBeenCalledOnce();
});

it('renders filenames and snippets as text without creating active elements', async () => {
  const name = '<img src=x onerror=alert(1)>.txt';
  const snippet = '<script>alert(1)</script><svg onload=alert(1)>';
  vi.mocked(searchDocuments).mockResolvedValue({
    results: [{ ...hit, document: { ...document, name }, snippet }],
    has_more: false,
  });
  const { container } = render(view());
  submit('script');
  expect(await screen.findByText(snippet)).toBeVisible();
  expect(
    screen.getByRole('link', { name: `Download ${name}` }),
  ).toHaveAttribute('href', `/api/documents/${document.id}/download`);
  expect(container.querySelector('script, img, svg')).toBeNull();
});

it('aborts old queries and ignores late responses after a newer search or clear', async () => {
  const old = deferred<SearchPage>();
  const latest = deferred<SearchPage>();
  vi.mocked(searchDocuments)
    .mockReturnValueOnce(old.promise)
    .mockReturnValueOnce(latest.promise);
  render(view());
  submit('old query');
  expect(screen.getByRole('status')).toHaveTextContent('Searching documents');
  await waitFor(() => expect(searchDocuments).toHaveBeenCalledTimes(1));
  const oldSignal = vi.mocked(searchDocuments).mock.calls[0]![2];
  submit('new query');
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => {
    latest.resolve(results);
  });
  expect(screen.getByText('Invoice DEMO-2026-001')).toBeVisible();
  await act(async () => {
    old.resolve({
      results: [{ ...hit, snippet: 'stale private snippet' }],
      has_more: false,
    });
  });
  expect(screen.queryByText('stale private snippet')).not.toBeInTheDocument();
  const clearing = deferred<SearchPage>();
  vi.mocked(searchDocuments).mockReturnValueOnce(clearing.promise);
  submit('clear pending');
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
  await act(async () => {
    clearing.resolve(results);
  });
  expect(screen.getByText('Library content')).toBeVisible();
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
});

it('shows errors, retries, and handles empty results without keeping old snippets', async () => {
  vi.mocked(searchDocuments)
    .mockResolvedValueOnce(results)
    .mockRejectedValueOnce(new APIError('Use a valid query.', 400))
    .mockResolvedValueOnce({ results: [], has_more: false });
  render(view());
  submit('invoice');
  await screen.findByText('Invoice DEMO-2026-001');
  submit('invalid query');
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Use a valid query.',
  );
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Try search again' }));
  expect(await screen.findByText('No matching documents')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Next results' })).toBeDisabled();
});

it('paginates, resets a changed query, and revalidates after refresh without showing cached results', async () => {
  vi.mocked(searchDocuments)
    .mockResolvedValueOnce({ ...results, has_more: true })
    .mockResolvedValue(results);
  const { rerender } = render(view());
  submit('invoice');
  await screen.findByText('Invoice DEMO-2026-001');
  fireEvent.click(screen.getByRole('button', { name: 'Next results' }));
  await waitFor(() =>
    expect(searchDocuments).toHaveBeenLastCalledWith(
      'invoice',
      20,
      expect.any(AbortSignal),
    ),
  );
  await screen.findByText('Showing 21–21');
  submit('supplier');
  await waitFor(() =>
    expect(searchDocuments).toHaveBeenLastCalledWith(
      'supplier',
      0,
      expect.any(AbortSignal),
    ),
  );
  await screen.findByText('Invoice DEMO-2026-001');
  const refresh = deferred<SearchPage>();
  vi.mocked(searchDocuments).mockReturnValueOnce(refresh.promise);
  rerender(view(1));
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
  await act(async () => {
    refresh.resolve({ results: [], has_more: false });
  });
  expect(screen.getByText('No matching documents')).toBeVisible();
});

it('rejects excessive UTF-8 input and expires the view when authentication fails', async () => {
  render(view());
  submit('合同'.repeat(100));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Search is too long',
  );
  expect(searchDocuments).not.toHaveBeenCalled();
  vi.mocked(searchDocuments).mockRejectedValue(
    new APIError('authentication required', 401),
  );
  submit('invoice');
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalledOnce());
  expect(screen.queryByText('Invoice DEMO-2026-001')).not.toBeInTheDocument();
});

it('refreshes indexing status and keeps metadata-only results downloadable', async () => {
  vi.mocked(searchDocuments)
    .mockResolvedValueOnce({
      results: [
        {
          ...hit,
          match: 'metadata',
          document: { ...document, indexing_status: 'processing' },
        },
      ],
      has_more: false,
    })
    .mockResolvedValueOnce({
      results: [
        {
          ...hit,
          match: 'metadata',
          document: { ...document, indexing_status: 'unsupported' },
        },
      ],
      has_more: false,
    });
  render(view());
  submit('invoice');
  expect(await screen.findByText('Indexing text…')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Refresh results' }));
  expect(await screen.findByText('No searchable text')).toBeVisible();
  expect(
    screen.getByRole('link', { name: 'Download Shared invoice.pdf' }),
  ).toHaveAttribute('href', `/api/documents/${document.id}/download`);
});
