import { useEffect, useState } from 'react';
import { APIError, getStorageOverview, type StorageReport } from './api';
import { formatDiskBytes } from './format';

export default function StorageOverview({
  onUnauthorized,
}: {
  onUnauthorized: () => void;
}) {
  const [revision, setRevision] = useState(0);
  const [response, setResponse] = useState<{
    revision: number;
    report?: StorageReport;
    error?: string;
  } | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    getStorageOverview(controller.signal)
      .then((report) => {
        if (!controller.signal.aborted) setResponse({ revision, report });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && error.status === 401) onUnauthorized();
        else
          setResponse({
            revision,
            error:
              error instanceof Error
                ? error.message
                : 'Storage overview could not be loaded. Try again.',
          });
      });
    return () => controller.abort();
  }, [revision, onUnauthorized]);

  const current = response?.revision === revision ? response : null;
  const report = current?.report;
  const capacity = report?.filesystem.capacity;
  const loading = current === null;
  const host = report?.host_storage;
  const hostUsed = host
    ? host.total_bytes - host.available_including_reclaimable_bytes
    : 0;
  // Logical bytes are a proxy, not measured allocation. Keep the estimated
  // split within the independently measured used space, even across snapshots.
  const vaultEstimate = Math.min(
    report?.documents.logical_bytes ?? 0,
    hostUsed,
  );
  const otherUsedEstimate = hostUsed - vaultEstimate;
  const tinyVault =
    !!host && vaultEstimate > 0 && vaultEstimate / host.total_bytes < 0.001;

  return (
    <section
      className="storage-card"
      id="storage-overview"
      aria-labelledby="storage-title"
      aria-busy={loading}
    >
      <div className="section-heading">
        <div>
          <h2 id="storage-title">Storage overview</h2>
          <p>Administrator access · All FamilyVault documents</p>
        </div>
        <button
          className="secondary"
          disabled={loading}
          onClick={() => setRevision((value) => value + 1)}
        >
          Refresh storage
        </button>
      </div>
      <div className="storage-content">
        {loading && <p role="status">Loading storage usage…</p>}
        {current?.error && (
          <p className="error" role="alert">
            {current.error}
          </p>
        )}
        {report && (
          <>
            <h3>{host ? 'Mac storage' : 'Host storage'}</h3>
            {host ? (
              <>
                <p className="storage-source">
                  macOS measurement · Updated{' '}
                  <time dateTime={host.collected_at}>
                    {new Date(host.collected_at).toLocaleString()}
                  </time>
                </p>
                <p className="storage-disk-summary">
                  {formatDiskBytes(
                    host.total_bytes -
                      host.available_including_reclaimable_bytes,
                  )}{' '}
                  used of {formatDiskBytes(host.total_bytes)}
                </p>
                <svg
                  className="storage-bar"
                  viewBox="0 0 1000 30"
                  preserveAspectRatio="none"
                  role="img"
                  aria-label={`Mac storage: ${formatDiskBytes(vaultEstimate)} vault estimate, ${formatDiskBytes(otherUsedEstimate)} other used estimate, ${formatDiskBytes(host.available_including_reclaimable_bytes - host.free_bytes)} reclaimable, ${formatDiskBytes(host.free_bytes)} free now`}
                >
                  <rect
                    className="storage-vault"
                    x={0}
                    y={0}
                    width={(vaultEstimate / host.total_bytes) * 1000}
                    height={30}
                  />
                  <rect
                    className="storage-used"
                    x={(vaultEstimate / host.total_bytes) * 1000}
                    y={0}
                    width={(otherUsedEstimate / host.total_bytes) * 1000}
                    height={30}
                  />
                  <rect
                    className="storage-reclaimable"
                    x={
                      ((host.total_bytes -
                        host.available_including_reclaimable_bytes) /
                        host.total_bytes) *
                      1000
                    }
                    y={0}
                    width={
                      ((host.available_including_reclaimable_bytes -
                        host.free_bytes) /
                        host.total_bytes) *
                      1000
                    }
                    height={30}
                  />
                  <rect
                    className="storage-free"
                    x={
                      ((host.total_bytes - host.free_bytes) /
                        host.total_bytes) *
                      1000
                    }
                    y={0}
                    width={(host.free_bytes / host.total_bytes) * 1000}
                    height={30}
                  />
                  {tinyVault && (
                    <line
                      className="storage-vault-marker"
                      x1={0.5}
                      x2={0.5}
                      y1={0}
                      y2={30}
                      strokeWidth={2}
                      vectorEffect="non-scaling-stroke"
                    >
                      <title>
                        Vault estimate is smaller than the bar’s display scale.
                      </title>
                    </line>
                  )}
                </svg>
                <dl className="storage-metrics storage-legend">
                  <div>
                    <dt>FamilyVault (estimated)</dt>
                    <dd>≈ {formatDiskBytes(vaultEstimate)}</dd>
                  </div>
                  <div>
                    <dt>Other used (estimated)</dt>
                    <dd>≈ {formatDiskBytes(otherUsedEstimate)}</dd>
                  </div>
                  <div>
                    <dt>Reclaimable by macOS</dt>
                    <dd>
                      {formatDiskBytes(
                        host.available_including_reclaimable_bytes -
                          host.free_bytes,
                      )}
                    </dd>
                  </div>
                  <div>
                    <dt>Free now</dt>
                    <dd>{formatDiskBytes(host.free_bytes)}</dd>
                  </div>
                </dl>
                <p className="storage-note">
                  Measured on the Mac volume containing Docker’s disk image.
                  macOS may include reclaimable caches in its available-space
                  figure; these can change. The vault segment estimates its
                  physical share using logical document bytes. Actual allocation
                  can differ due to database/index overhead, compression and
                  snapshots. Other used includes remaining Mac and Docker usage,
                  including vault overhead not counted by this estimate.
                </p>
                {tinyVault && (
                  <p className="storage-note">
                    The vault estimate is too small to see at this scale; the
                    purple line marks its position without enlarging the
                    segment.
                  </p>
                )}
                {report.documents.logical_bytes > hostUsed && (
                  <p className="storage-note">
                    The displayed vault estimate is capped at measured used
                    space. Logical document sizes exceed that measurement, so
                    the physical split is uncertain.
                  </p>
                )}
                {host.free_bytes < 1_073_741_824 ||
                host.free_bytes / host.total_bytes < 0.1 ? (
                  <p className="storage-warning" role="alert">
                    Low free space on the Mac’s storage volume.
                  </p>
                ) : null}
              </>
            ) : (
              <p className="storage-warning" role="status">
                Host storage is unavailable. Start the macOS storage collector
                to measure the actual disk. Docker’s virtual capacity does not
                establish available physical disk space.
              </p>
            )}
            <dl className="storage-metrics">
              <div>
                <dt>Estimated space for more vault data</dt>
                <dd>
                  {report.growth_available_bytes != null
                    ? formatDiskBytes(report.growth_available_bytes)
                    : 'Unknown'}
                </dd>
              </div>
            </dl>
            <p className="storage-note">
              A conservative estimate from Mac free space now and Docker volume
              space. This is shared space, not reserved for FamilyVault;
              database, index and filesystem overhead also consume it.
              Reclaimable space is excluded from this estimate.
            </p>
            <details className="storage-details">
              <summary>Docker volume limits</summary>
              <p className="storage-source">
                {report.filesystem.source === 'container-filesystem'
                  ? 'Container filesystem'
                  : 'Native filesystem'}{' '}
                · Cached for up to 30 seconds
              </p>
              <p className="storage-note">
                These values do not measure the Mac’s physical disk. They
                describe the filesystem visible to the server.
              </p>
              {capacity ? (
                <>
                  <dl className="storage-metrics">
                    <div>
                      <dt>Volume capacity</dt>
                      <dd>{formatDiskBytes(capacity.total_bytes)}</dd>
                    </div>
                    <div>
                      <dt>Volume used</dt>
                      <dd>{formatDiskBytes(capacity.used_bytes)}</dd>
                    </div>
                    <div>
                      <dt>Volume available</dt>
                      <dd>{formatDiskBytes(capacity.available_bytes)}</dd>
                    </div>
                  </dl>
                  <p className="storage-note">
                    {formatDiskBytes(capacity.reserved_bytes)} of free space is
                    reserved or unavailable inside this filesystem.
                  </p>
                  {capacity.low_space && (
                    <p className="storage-warning" role="alert">
                      Low available space inside the document volume:{' '}
                      {formatDiskBytes(capacity.available_bytes)} remains.
                    </p>
                  )}
                </>
              ) : (
                <p className="storage-warning" role="status">
                  Filesystem capacity is unavailable. Document totals are still
                  available; free space could not be checked.
                </p>
              )}
            </details>
            <h3>FamilyVault documents</h3>
            <dl className="storage-metrics">
              <div>
                <dt>Logical document bytes</dt>
                <dd>{formatDiskBytes(report.documents.logical_bytes)}</dd>
              </div>
              <div>
                <dt>Document count</dt>
                <dd>{report.documents.count.toLocaleString()}</dd>
              </div>
            </dl>
            <p className="storage-note">
              Original file sizes recorded in SQLite. This excludes the
              database, search index, temporary files and filesystem allocation
              overhead.
            </p>
            {report.documents.by_type.length ? (
              <div className="table-wrap">
                <table className="storage-types">
                  <caption>Usage by detected file type</caption>
                  <thead>
                    <tr>
                      <th scope="col">Detected type</th>
                      <th scope="col">Documents</th>
                      <th scope="col">Logical size</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.documents.by_type.map((item) => (
                      <tr key={item.media_type}>
                        <th scope="row">{item.media_type}</th>
                        <td>{item.count.toLocaleString()}</td>
                        <td>{formatDiskBytes(item.logical_bytes)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="storage-note">No documents stored yet.</p>
            )}
          </>
        )}
      </div>
    </section>
  );
}
