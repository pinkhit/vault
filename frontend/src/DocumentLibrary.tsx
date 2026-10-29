import type { DocumentPage, DocumentRecord } from './api';
import { formatBytes } from './format';
import IndexingBadge from './IndexingBadge';

interface DocumentLibraryProps {
  page: DocumentPage;
  offset: number;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  onPageChange: (offset: number) => void;
  onPreview: (document: DocumentRecord) => void;
}

export default function DocumentLibrary({
  page,
  offset,
  loading,
  error,
  onRefresh,
  onPageChange,
  onPreview,
}: DocumentLibraryProps) {
  function renderDocuments() {
    if (error) {
      return (
        <div className="empty error" role="alert">
          {error}
          <br />
          <button className="secondary" onClick={onRefresh}>
            Try again
          </button>
        </div>
      );
    }
    if (loading) {
      return (
        <p className="empty" role="status">
          Loading documents…
        </p>
      );
    }
    if (page.documents.length === 0) {
      return (
        <div className="empty">
          <span className="empty-icon" aria-hidden="true">
            ↥
          </span>
          <h3>
            {offset === 0 ? 'Your library starts here' : 'No more documents'}
          </h3>
          <p>
            {offset === 0
              ? 'Upload a fictional invoice, contract, or receipt to get started.'
              : 'Return to the previous page or refresh the library.'}
          </p>
        </div>
      );
    }
    return (
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">Document</th>
              <th scope="col">Size</th>
              <th scope="col">Uploaded</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {page.documents.map((document) => (
              <tr key={document.id}>
                <td>
                  <span className="document-name">{document.name}</span>
                  <IndexingBadge status={document.indexing_status} />
                  <details>
                    <summary>SHA-256 checksum</summary>
                    <code>{document.sha256}</code>
                    <p>{document.media_type}</p>
                  </details>
                </td>
                <td className="nowrap">{formatBytes(document.size)}</td>
                <td className="nowrap">
                  <time dateTime={document.created_at}>
                    {new Date(document.created_at).toLocaleDateString(
                      undefined,
                      { year: 'numeric', month: 'short', day: 'numeric' },
                    )}
                  </time>
                </td>
                <td>
                  <button
                    className="secondary preview-button"
                    onClick={() => onPreview(document)}
                    aria-label={`Preview ${document.name}`}
                  >
                    Preview
                  </button>
                  <a
                    className="download"
                    href={`/api/documents/${document.id}/download`}
                    aria-label={`Download ${document.name}`}
                  >
                    Download <span aria-hidden="true">↓</span>
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <section
      className="document-card"
      aria-labelledby="library-title"
      aria-busy={loading}
    >
      <div className="section-heading">
        <div>
          <h2 id="library-title">Document library</h2>
          <p>Newest uploads first · Refresh to update indexing status</p>
        </div>
        <button className="secondary" disabled={loading} onClick={onRefresh}>
          Refresh
        </button>
      </div>
      {renderDocuments()}
      <div className="pagination">
        <span>
          {page.documents.length > 0 && !loading && !error
            ? `Showing ${offset + 1}–${offset + page.documents.length}`
            : 'Original files · Local storage'}
        </span>
        <div>
          <button
            className="secondary"
            disabled={offset === 0 || loading}
            onClick={() => onPageChange(Math.max(0, offset - 50))}
          >
            Previous
          </button>
          <button
            className="secondary"
            disabled={!page.has_more || loading || !!error}
            onClick={() => onPageChange(offset + 50)}
          >
            Next
          </button>
        </div>
      </div>
    </section>
  );
}
