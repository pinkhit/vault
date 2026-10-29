import type { IndexingState } from './api';

const labels: Record<IndexingState, string> = {
  pending: 'Indexing queued',
  processing: 'Indexing text…',
  ready: 'Text searchable',
  failed: 'Text indexing failed',
  unsupported: 'No searchable text',
};

export default function IndexingBadge({ status }: { status?: IndexingState }) {
  return (
    <span className={`indexing-badge indexing-${status ?? 'unknown'}`}>
      {status ? labels[status] : 'Indexing status unavailable'}
    </span>
  );
}
