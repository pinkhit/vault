import { useEffect, useRef, useState } from 'react';
import { APIError, getPreview, type DocumentRecord, type Preview } from './api';

export default function DocumentPreview({
  document,
  initialPage = 1,
  onClose,
  onUnauthorized,
}: {
  document: DocumentRecord;
  initialPage?: number;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [page, setPage] = useState(initialPage);
  const [revision, setRevision] = useState(0);
  const [response, setResponse] = useState<{
    page: number;
    revision: number;
    preview?: Preview;
    error?: string;
  } | null>(null);

  useEffect(() => {
    dialog.current?.showModal();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    getPreview(document.id, page, controller.signal)
      .then((preview) => {
        if (!controller.signal.aborted)
          setResponse({ page, revision, preview });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && error.status === 401) {
          onUnauthorized();
        } else {
          setResponse({
            page,
            revision,
            error:
              error instanceof Error
                ? error.message
                : 'Preview failed. Try again or download the original.',
          });
        }
      });
    return () => controller.abort();
  }, [document.id, page, revision, onUnauthorized]);

  const current =
    response?.page === page && response.revision === revision ? response : null;
  const preview = current?.preview;
  const loading = current === null;

  return (
    <dialog
      ref={dialog}
      className="preview-dialog"
      aria-labelledby="preview-title"
      onClose={onClose}
      onKeyDown={(event) => {
        if (event.key !== 'Tab') return;
        const controls = dialog.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled), a[href]',
        );
        const first = controls?.[0];
        const last = controls?.[controls.length - 1];
        const active = window.document.activeElement;
        if (event.shiftKey && active === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && active === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
    >
      <div className="preview-heading">
        <div>
          <h2 id="preview-title">Preview: {document.name}</h2>
          <p>Read-only preview · Download to keep the original</p>
        </div>
        <button
          className="secondary"
          onClick={() => dialog.current?.close()}
          autoFocus
        >
          Close preview
        </button>
      </div>
      <div className="preview-content" aria-busy={loading}>
        {loading && <p role="status">Preparing preview…</p>}
        {current?.error && (
          <p className="error" role="alert">
            {current.error}
          </p>
        )}
        {preview && (
          <>
            {preview.kind === 'text' || preview.kind === 'json' ? (
              <pre className="preview-text">{preview.text}</pre>
            ) : preview.kind === 'csv' ? (
              <div className="preview-table">
                <table>
                  <caption>CSV rows (values shown as text)</caption>
                  <tbody>
                    {preview.rows?.map((row, index) => (
                      <tr key={index}>
                        {row.map((cell, column) => (
                          <td key={column}>{cell}</td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <img
                className="preview-image"
                src={`data:image/png;base64,${preview.image}`}
                alt={
                  preview.kind === 'pdf'
                    ? `Page ${preview.page} of ${document.name}`
                    : document.name
                }
              />
            )}
            {preview.truncated && (
              <p className="preview-note" role="status">
                Preview shortened to keep it responsive. Download the original
                for the complete document.
              </p>
            )}
          </>
        )}
      </div>
      <div className="preview-actions">
        <a
          className="download"
          href={`/api/documents/${encodeURIComponent(document.id)}/download`}
        >
          Download original
        </a>
        <button
          className="secondary"
          disabled={loading}
          onClick={() => setRevision((value) => value + 1)}
        >
          Refresh preview
        </button>
        {preview?.kind === 'pdf' && (
          <div className="preview-pages">
            <button
              className="secondary"
              disabled={page <= 1}
              onClick={() => setPage(page - 1)}
            >
              Previous page
            </button>
            <span role="status">
              Page {preview.page} of {preview.pages}
            </span>
            <button
              className="secondary"
              disabled={page >= (preview.pages ?? 1)}
              onClick={() => setPage(page + 1)}
            >
              Next page
            </button>
          </div>
        )}
      </div>
    </dialog>
  );
}
