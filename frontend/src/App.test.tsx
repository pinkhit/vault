import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { getHealth, listDocuments, uploadDocument } from './api';

vi.mock('./api', () => ({
  getHealth: vi.fn(),
  listDocuments: vi.fn(),
  uploadDocument: vi.fn(),
}));
const document = {
  id: 'a'.repeat(32),
  name: 'Fictional invoice.txt',
  size: 21,
  sha256: 'b'.repeat(64),
  media_type: 'text/plain',
  created_at: '2026-01-01T00:00:00Z',
};

beforeEach(() => {
  vi.resetAllMocks();
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
