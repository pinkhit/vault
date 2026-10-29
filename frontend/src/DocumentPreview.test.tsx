import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import DocumentPreview from './DocumentPreview';
import { APIError, getPreview, type Preview } from './api';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  getPreview: vi.fn(),
}));

const document = {
  id: 'a'.repeat(32),
  owner_id: 'b'.repeat(32),
  name: '<img src=x onerror=alert(1)>.txt',
  size: 100,
  sha256: 'c'.repeat(64),
  media_type: 'text/plain',
  created_at: '2026-01-01T00:00:00Z',
};
const onClose = vi.fn();
const onUnauthorized = vi.fn();
const view = () => (
  <DocumentPreview
    document={document}
    onClose={onClose}
    onUnauthorized={onUnauthorized}
  />
);

beforeEach(() => {
  vi.resetAllMocks();
});

it('renders malicious text and filenames literally, reports clipping and provides a download', async () => {
  vi.mocked(getPreview).mockResolvedValue({
    kind: 'text',
    text: '<svg onload=alert(1)>Private</svg>',
    truncated: true,
  });
  const { container } = render(view());
  expect(
    await screen.findByText('<svg onload=alert(1)>Private</svg>'),
  ).toBeVisible();
  expect(screen.getByRole('dialog')).toHaveAccessibleName(
    `Preview: ${document.name}`,
  );
  expect(container.querySelector('svg, script, img')).toBeNull();
  expect(screen.getByText(/Preview shortened/)).toBeVisible();
  expect(
    screen.getByRole('link', { name: 'Download original' }),
  ).toHaveAttribute('href', `/api/documents/${document.id}/download`);
  fireEvent.click(screen.getByRole('button', { name: 'Close preview' }));
  expect(onClose).toHaveBeenCalledOnce();
});

it('shows CSV cells as literal values, including formulas and markup', async () => {
  vi.mocked(getPreview).mockResolvedValue({
    kind: 'csv',
    rows: [['<script>alert(1)</script>', '=SUM(1,2)']],
    truncated: false,
  });
  const { container } = render(view());
  expect(
    await screen.findByRole('cell', { name: '<script>alert(1)</script>' }),
  ).toBeVisible();
  expect(screen.getByRole('cell', { name: '=SUM(1,2)' })).toBeVisible();
  expect(container.querySelector('script, iframe, object')).toBeNull();
});

it('loads PDF pages without embedded PDF content and hides old pages while fetching', async () => {
  const first: Preview = {
    kind: 'pdf',
    page: 1,
    pages: 2,
    image: 'first',
    truncated: false,
  };
  vi.mocked(getPreview).mockResolvedValueOnce(first);
  let resolve!: (preview: Preview) => void;
  vi.mocked(getPreview).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  render(view());
  await screen.findByRole('img');
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
  expect(screen.queryByRole('img')).not.toBeInTheDocument();
  expect(screen.getByRole('status')).toHaveTextContent('Preparing preview');
  await act(async () => {
    resolve({ ...first, page: 2, image: 'second' });
  });
  expect(screen.getByRole('img')).toHaveAttribute(
    'src',
    'data:image/png;base64,second',
  );
  expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
  expect(getPreview).toHaveBeenLastCalledWith(
    document.id,
    2,
    expect.any(AbortSignal),
  );
});

it('clears prior content on refresh errors, supports retry and handles unsupported files', async () => {
  vi.mocked(getPreview).mockResolvedValueOnce({
    kind: 'text',
    text: 'Private original',
    truncated: false,
  });
  vi.mocked(getPreview).mockRejectedValueOnce(
    new APIError('resource not found', 404),
  );
  vi.mocked(getPreview).mockRejectedValueOnce(
    new APIError('download the original', 415),
  );
  render(view());
  await screen.findByText('Private original');
  fireEvent.click(screen.getByRole('button', { name: 'Refresh preview' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'resource not found',
  );
  expect(screen.queryByText('Private original')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Refresh preview' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'download the original',
  );
  expect(screen.getByRole('link', { name: 'Download original' })).toBeVisible();
});

it('returns to sign-in on expired/revoked sessions and cancels dismissed requests', async () => {
  vi.mocked(getPreview).mockRejectedValueOnce(
    new APIError('authentication required', 401),
  );
  const { unmount } = render(view());
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalledOnce());
  unmount();
  let resolve!: (preview: Preview) => void;
  vi.mocked(getPreview).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const second = render(view());
  const signal = vi.mocked(getPreview).mock.calls.at(-1)![2]!;
  second.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => {
    resolve({ kind: 'text', text: 'late private response', truncated: false });
  });
  expect(screen.queryByText('late private response')).not.toBeInTheDocument();
});
