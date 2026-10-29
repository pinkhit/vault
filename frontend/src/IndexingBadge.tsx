import type { IndexingState } from './api';

const labels = {
  pending: 'Preparing search',
  processing: 'Preparing search…',
  failed: 'Text search unavailable',
  unsupported: 'Filename search only',
};

export default function IndexingBadge({ status }: { status?: IndexingState }) {
  if (!status || status === 'ready') return null;
  return (
    <span className={`indexing-badge indexing-${status ?? 'unknown'}`}>
      {labels[status]}
    </span>
  );
}
