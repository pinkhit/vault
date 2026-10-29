import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { APIError, getStorageOverview, type StorageReport } from './api';
import StorageOverview from './StorageOverview';
import { deferred } from './test-utils';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  getStorageOverview: vi.fn(),
}));

const report: StorageReport = {
  host_storage: {
    version: 1,
    collected_at: '2026-10-29T01:00:00Z',
    total_bytes: 1000 ** 4,
    free_bytes: 200 * 1000 ** 3,
    available_including_reclaimable_bytes: 250 * 1000 ** 3,
  },
  growth_available_bytes: 80 * 1024 ** 3,
  documents: {
    count: 3,
    logical_bytes: 4096,
    by_type: [{ media_type: 'text/plain', count: 3, logical_bytes: 4096 }],
  },
  filesystem: {
    source: 'container-filesystem',
    checked_at: '2026-10-29T01:00:00Z',
    capacity: {
      total_bytes: 128 * 1024 ** 3,
      used_bytes: 32 * 1024 ** 3,
      available_bytes: 80 * 1024 ** 3,
      reserved_bytes: 16 * 1024 ** 3,
      low_space: false,
    },
  },
};
const onUnauthorized = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getStorageOverview).mockResolvedValue(report);
});

it('labels the logical-byte vault estimate separately from measured Mac capacity', async () => {
  render(<StorageOverview onUnauthorized={onUnauthorized} />);
  expect(await screen.findByText('750.0 GB used of 1.0 TB')).toBeVisible();
  expect(
    screen.getByRole('img', { name: /Mac storage:/ }),
  ).toHaveAccessibleName(
    /4.1 KB vault estimate, 750.0 GB other used estimate, 50.0 GB reclaimable, 200.0 GB free now/,
  );
  expect(
    screen.getByText('Estimated space for more vault data').closest('div'),
  ).toHaveTextContent('85.9 GB');
  expect(
    screen.getByText(
      /estimates its physical share using logical document bytes/,
    ),
  ).toBeVisible();
  expect(
    screen.getByText('SafeVault (estimated)').closest('div'),
  ).toHaveTextContent('≈ 4.1 KB');
  expect(screen.getByText(/green line marks its position/)).toBeVisible();
  fireEvent.click(screen.getByText('Docker volume limits'));
  expect(screen.getByText('137.4 GB')).toBeVisible();
  expect(screen.getByText(/Container filesystem/)).toBeVisible();
  expect(
    screen.getByRole('table', { name: 'Usage by detected file type' }),
  ).toContainHTML('text/plain');
});

it.each([
  { logical: 100 * 1000 ** 3, vaultWidth: 100, otherWidth: 650, capped: false },
  { logical: 0, vaultWidth: 0, otherWidth: 750, capped: false },
  { logical: 900 * 1000 ** 3, vaultWidth: 750, otherWidth: 0, capped: true },
])(
  'keeps the measured total intact for a $logical-byte logical estimate',
  async ({ logical, vaultWidth, otherWidth, capped }) => {
    vi.mocked(getStorageOverview).mockResolvedValue({
      ...report,
      documents: { ...report.documents, logical_bytes: logical },
    });
    render(<StorageOverview onUnauthorized={onUnauthorized} />);
    await screen.findByText('SafeVault (estimated)');
    const bar = screen.getByRole('img', { name: /Mac storage:/ });
    const widths = [...bar.querySelectorAll('rect')].map((rect) =>
      Number(rect.getAttribute('width')),
    );
    expect(widths).toHaveLength(4);
    expect(widths[0]).toBeCloseTo(vaultWidth);
    expect(widths[1]).toBeCloseTo(otherWidth);
    expect(widths.reduce((sum, width) => sum + width, 0)).toBeCloseTo(1000);
    expect(bar.querySelector('.storage-vault-marker')).toBeNull();
    expect(screen.getByText('750.0 GB used of 1.0 TB')).toBeVisible();
    if (capped)
      expect(
        screen.getByText(/estimate is capped at measured used space/),
      ).toBeVisible();
    else
      expect(
        screen.queryByText(/estimate is capped at measured used space/),
      ).not.toBeInTheDocument();
  },
);

it('shows unavailable capacity without fabricating zero values and keeps logical totals', async () => {
  vi.mocked(getStorageOverview).mockResolvedValue({
    ...report,
    filesystem: { ...report.filesystem, capacity: null },
  });
  render(<StorageOverview onUnauthorized={onUnauthorized} />);
  await screen.findByText('750.0 GB used of 1.0 TB');
  fireEvent.click(screen.getByText('Docker volume limits'));
  expect(
    await screen.findByText(/Filesystem capacity is unavailable/),
  ).toHaveTextContent('Filesystem capacity is unavailable');
  expect(screen.queryByText('Volume capacity')).not.toBeInTheDocument();
  expect(screen.getByText('Stored file size')).toBeVisible();
  expect(screen.getByRole('table')).toBeVisible();
});

it('handles an empty native library and displays a genuine low-space warning', async () => {
  vi.mocked(getStorageOverview).mockResolvedValue({
    documents: { count: 0, logical_bytes: 0, by_type: [] },
    filesystem: {
      ...report.filesystem,
      source: 'native-filesystem',
      capacity: {
        ...report.filesystem.capacity!,
        available_bytes: 512 * 1024 ** 2,
        low_space: true,
      },
    },
  });
  render(<StorageOverview onUnauthorized={onUnauthorized} />);
  await screen.findByText('No documents stored yet.');
  fireEvent.click(screen.getByText('Docker volume limits'));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Low available space',
  );
  expect(screen.getByRole('alert')).toHaveTextContent('536.9 MB');
  expect(screen.getByText('No documents stored yet.')).toBeVisible();
  expect(screen.getByText(/Native filesystem/)).toBeVisible();
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
});

it('clears old metrics before refresh and does not retain them after permission denial', async () => {
  vi.mocked(getStorageOverview).mockRejectedValueOnce(
    new Error('Temporary failure'),
  );
  render(<StorageOverview onUnauthorized={onUnauthorized} />);
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Temporary failure',
  );
  fireEvent.click(screen.getByRole('button', { name: 'Refresh storage' }));
  await screen.findByText('750.0 GB used of 1.0 TB');
  vi.mocked(getStorageOverview).mockRejectedValueOnce(
    new APIError('permission denied', 403),
  );
  fireEvent.click(screen.getByRole('button', { name: 'Refresh storage' }));
  expect(screen.queryByText('750.0 GB used of 1.0 TB')).not.toBeInTheDocument();
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'permission denied',
  );
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
});

it('clears authentication on expiry and cancels requests when hidden', async () => {
  vi.mocked(getStorageOverview).mockRejectedValueOnce(
    new APIError('authentication required', 401),
  );
  const first = render(<StorageOverview onUnauthorized={onUnauthorized} />);
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalledOnce());
  first.unmount();
  const pending = deferred<StorageReport>();
  vi.mocked(getStorageOverview).mockReturnValueOnce(pending.promise);
  const second = render(<StorageOverview onUnauthorized={onUnauthorized} />);
  const signal = vi.mocked(getStorageOverview).mock.calls.at(-1)![0]!;
  second.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => {
    pending.resolve(report);
  });
  expect(screen.queryByText('750.0 GB used of 1.0 TB')).not.toBeInTheDocument();
});

it('never presents virtual free space as host space when the collector is unavailable', async () => {
  vi.mocked(getStorageOverview).mockResolvedValue({
    ...report,
    host_storage: null,
    growth_available_bytes: null,
  });
  render(<StorageOverview onUnauthorized={onUnauthorized} />);
  expect(await screen.findByText(/Host storage is unavailable/)).toBeVisible();
  expect(screen.getByText('Unknown')).toBeVisible();
  expect(
    screen.queryByRole('img', { name: /Mac storage:/ }),
  ).not.toBeInTheDocument();
  expect(screen.getByText('Stored file size')).toBeVisible();
  expect(screen.queryByText('SafeVault (estimated)')).not.toBeInTheDocument();
});
