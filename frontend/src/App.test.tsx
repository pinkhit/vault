import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import {
  getHealth,
  getSession,
  login,
  logout,
  listDocuments,
  uploadDocument,
  APIError,
  searchDocuments,
  getPreview,
  getStorageOverview,
} from './api';

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>();
  return {
    ...actual,
    getHealth: vi.fn(),
    getSession: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    listDocuments: vi.fn(),
    uploadDocument: vi.fn(),
    searchDocuments: vi.fn(),
    getPreview: vi.fn(),
    getStorageOverview: vi.fn(),
  };
});
const session = {
  user: {
    id: 'c'.repeat(32),
    username: 'alice',
    is_admin: false,
    enabled: true,
  },
  csrf_token: 'e'.repeat(64),
  expires_at: '2026-10-29T00:00:00Z',
};
const document = {
  owner_id: 'c'.repeat(32),
  id: 'a'.repeat(32),
  name: 'Fictional invoice.txt',
  size: 21,
  sha256: 'b'.repeat(64),
  media_type: 'text/plain',
  created_at: '2026-01-01T00:00:00Z',
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getSession).mockResolvedValue(session);
  vi.mocked(getHealth).mockResolvedValue({
    status: 'ok',
    mode: 'local-development',
    max_upload_bytes: 1024,
  });
  vi.mocked(listDocuments).mockResolvedValue({
    documents: [],
    has_more: false,
  });
});

describe('document browser', () => {
  it('exposes storage only to administrators and fetches it only when opened', async () => {
    const first = render(<App />);
    await screen.findByText('Your library starts here');
    expect(
      screen.queryByRole('button', { name: 'Storage overview' }),
    ).not.toBeInTheDocument();
    expect(getStorageOverview).not.toHaveBeenCalled();
    first.unmount();
    vi.mocked(getSession).mockResolvedValue({
      ...session,
      user: { ...session.user, is_admin: true },
    });
    vi.mocked(getStorageOverview).mockResolvedValue({
      documents: { count: 0, logical_bytes: 0, by_type: [] },
      filesystem: {
        source: 'container-filesystem',
        checked_at: '2026-10-29T01:00:00Z',
        capacity: null,
      },
    });
    render(<App />);
    const button = await screen.findByRole('button', {
      name: 'Storage overview',
    });
    expect(getStorageOverview).not.toHaveBeenCalled();
    fireEvent.click(button);
    await screen.findByText('No documents stored yet.');
    fireEvent.click(button);
    expect(
      screen.queryByText('No documents stored yet.'),
    ).not.toBeInTheDocument();
  });
  it('opens previews from the library and search results, then clears them on session revocation', async () => {
    vi.mocked(listDocuments).mockResolvedValue({
      documents: [document],
      has_more: false,
    });
    vi.mocked(getPreview).mockResolvedValue({
      kind: 'text',
      text: 'Private preview',
      truncated: false,
    });
    vi.mocked(searchDocuments).mockResolvedValue({
      results: [
        { document, snippet: 'Private snippet', match: 'content', page: 2 },
      ],
      has_more: false,
    });
    render(<App />);
    fireEvent.click(
      await screen.findByRole('button', { name: `Preview ${document.name}` }),
    );
    expect(await screen.findByText('Private preview')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Close preview' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox'), {
      target: { value: 'private' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Search$/ }));
    fireEvent.click(
      await screen.findByRole('button', { name: `Preview ${document.name}` }),
    );
    await screen.findByText('Private preview');
    expect(getPreview).toHaveBeenLastCalledWith(
      document.id,
      2,
      expect.any(AbortSignal),
    );
    vi.mocked(getPreview).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Refresh preview' }));
    await screen.findByRole('heading', { name: 'Sign in' });
    expect(screen.queryByText('Private preview')).not.toBeInTheDocument();
    expect(screen.queryByText('Private snippet')).not.toBeInTheDocument();
  });
  it('shows the empty state and development warning', async () => {
    render(<App />);
    expect(await screen.findByText('Your library starts here')).toBeVisible();
    expect(screen.getByText('Synthetic documents only.')).toBeVisible();
    expect(
      screen.getByRole('button', { name: 'Upload document' }),
    ).toBeDisabled();
  });
  it('uploads a selected file, confirms integrity and refreshes the list', async () => {
    vi.mocked(uploadDocument).mockResolvedValue(document);
    render(<App />);
    await screen.findByText('API connected');
    const file = new File(['fictional invoice'], 'invoice.txt', {
      type: 'text/plain',
    });
    fireEvent.change(screen.getByLabelText('Choose a fictional document'), {
      target: { files: [file] },
    });
    vi.mocked(listDocuments).mockResolvedValue({
      documents: [document],
      has_more: false,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Upload document' }));
    expect(
      await screen.findByText(
        'Fictional invoice.txt uploaded. SHA-256 verified.',
      ),
    ).toBeVisible();
    expect(
      await screen.findByRole('link', {
        name: 'Download Fictional invoice.txt',
      }),
    ).toHaveAttribute('href', `/api/documents/${document.id}/download`);
    expect(uploadDocument).toHaveBeenCalledWith(file);
  });
  it('shows server errors and allows retrying the list', async () => {
    vi.mocked(listDocuments).mockRejectedValueOnce(
      new Error('Server unavailable'),
    );
    render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Server unavailable',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Your library starts here')).toBeVisible();
  });
  it('rejects empty or oversized files before uploading', async () => {
    render(<App />);
    await screen.findByText('API connected');
    fireEvent.change(screen.getByLabelText('Choose a fictional document'), {
      target: { files: [new File([], 'empty.txt')] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Upload document' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Choose a non-empty file.',
    );
    fireEvent.change(screen.getByLabelText('Choose a fictional document'), {
      target: { files: [new File(['x'.repeat(1025)], 'large.txt')] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Upload document' }));
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Choose a file smaller',
      ),
    );
    expect(uploadDocument).not.toHaveBeenCalled();
  });
  it('keeps a failed upload available for retry', async () => {
    vi.mocked(uploadDocument).mockRejectedValue(new Error('Disk is full'));
    render(<App />);
    await screen.findByText('API connected');
    fireEvent.change(screen.getByLabelText('Choose a fictional document'), {
      target: { files: [new File(['test'], 'test.txt')] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Upload document' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Disk is full');
    expect(
      screen.getByRole('button', { name: 'Upload document' }),
    ).toBeEnabled();
  });
});

describe('authentication', () => {
  it('does not fetch documents until authenticated', async () => {
    vi.mocked(getSession).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    render(<App />);
    expect(
      await screen.findByRole('heading', { name: 'Sign in' }),
    ).toBeVisible();
    expect(listDocuments).not.toHaveBeenCalled();
  });

  it('signs in and loads the authenticated library', async () => {
    vi.mocked(getSession).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    vi.mocked(login).mockResolvedValue(session);
    render(<App />);
    await screen.findByRole('heading', { name: 'Sign in' });
    fireEvent.change(screen.getByLabelText('Username'), {
      target: { value: 'alice' },
    });
    fireEvent.change(screen.getByLabelText('Password'), {
      target: { value: 'fictional-password' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByText('Your library starts here')).toBeVisible();
    expect(login).toHaveBeenCalledWith('alice', 'fictional-password');
  });

  it('shows generic login errors and clears the password', async () => {
    vi.mocked(getSession).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    vi.mocked(login).mockRejectedValue(
      new APIError('invalid username or password', 401),
    );
    render(<App />);
    await screen.findByRole('heading', { name: 'Sign in' });
    fireEvent.change(screen.getByLabelText('Username'), {
      target: { value: 'alice' },
    });
    fireEvent.change(screen.getByLabelText('Password'), {
      target: { value: 'wrong' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'invalid username or password',
    );
    expect(screen.getByLabelText('Password')).toHaveValue('');
  });

  it('clears the library after sign-out', async () => {
    vi.mocked(logout).mockResolvedValue();
    render(<App />);
    await screen.findByText('Your library starts here');
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(
      await screen.findByRole('heading', { name: 'Sign in' }),
    ).toBeVisible();
    expect(logout).toHaveBeenCalledWith(false);
    expect(screen.queryByText('Document library')).not.toBeInTheDocument();
  });

  it('returns to sign-in when the session expires', async () => {
    vi.mocked(listDocuments).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    render(<App />);
    expect(
      await screen.findByRole('heading', { name: 'Sign in' }),
    ).toBeVisible();
  });

  it('clears search results on sign-out and on search session expiry', async () => {
    vi.mocked(searchDocuments).mockResolvedValue({
      results: [
        {
          document,
          snippet: 'private search snippet',
          match: 'content',
          section: 1,
        },
      ],
      has_more: false,
    });
    vi.mocked(logout).mockResolvedValue();
    const { unmount } = render(<App />);
    await screen.findByText('Your library starts here');
    fireEvent.change(screen.getByRole('searchbox'), {
      target: { value: 'private' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Search$/ }));
    await screen.findByText('private search snippet');
    fireEvent.click(screen.getByRole('button', { name: /^Sign out$/ }));
    await screen.findByRole('heading', { name: 'Sign in' });
    expect(
      screen.queryByText('private search snippet'),
    ).not.toBeInTheDocument();
    unmount();
    vi.mocked(searchDocuments).mockRejectedValue(
      new APIError('authentication required', 401),
    );
    render(<App />);
    await screen.findByText('Your library starts here');
    fireEvent.change(screen.getByRole('searchbox'), {
      target: { value: 'private' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Search$/ }));
    await screen.findByRole('heading', { name: 'Sign in' });
    expect(screen.queryByText('Search results')).not.toBeInTheDocument();
  });
});
