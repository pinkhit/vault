import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  getHealth,
  listDocuments,
  uploadDocument,
  type DocumentPage,
  type Health,
} from './api';
import DocumentLibrary from './DocumentLibrary';
import { formatBytes } from './format';

const errorMessage = (error: unknown) =>
  error instanceof Error
    ? error.message
    : 'Something went wrong. Please try again.';

export default function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [healthError, setHealthError] = useState('');
  const [page, setPage] = useState<DocumentPage>({
    documents: [],
    has_more: false,
  });
  const [offset, setOffset] = useState(0);
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState('');
  const [notice, setNotice] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    setHealthError('');
    getHealth(controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) {
          setHealth(result);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setHealthError(errorMessage(error));
        }
      });
    return () => controller.abort();
  }, [revision]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setListError('');
    listDocuments(offset, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) {
          setPage(result);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setListError(errorMessage(error));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [offset, revision]);

  function refresh() {
    setRevision((value) => value + 1);
  }

  async function onUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file || !health || uploading) {
      return;
    }
    setUploadError('');
    setNotice('');
    if (file.size === 0) {
      setUploadError('Choose a non-empty file.');
      return;
    }
    if (file.size > health.max_upload_bytes) {
      setUploadError(
        `Choose a file smaller than ${formatBytes(health.max_upload_bytes)}.`,
      );
      return;
    }
    setUploading(true);
    try {
      const result = await uploadDocument(file);
      setNotice(`${result.name} uploaded. SHA-256 verified.`);
      setFile(null);
      if (fileInput.current) {
        fileInput.current.value = '';
      }
      setOffset(0);
      refresh();
    } catch (error: unknown) {
      setUploadError(errorMessage(error));
    } finally {
      setUploading(false);
    }
  }

  return (
    <>
      <header className="topbar">
        <a className="brand" href="/" aria-label="FamilyVault home">
          <span className="brand-mark" aria-hidden="true">
            F
          </span>
          FamilyVault
        </a>
        <span className="local-badge">
          <span aria-hidden="true" />
          Local development
        </span>
      </header>
      <div className="warning">
        <strong>Synthetic documents only.</strong> This prototype has no
        accounts or access controls. Keep it on localhost.
      </div>
      <main>
        <div className="page-heading">
          <div>
            <p className="eyebrow">YOUR LOCAL REPOSITORY</p>
            <h1>Documents</h1>
            <p>Keep files together. Retrieve the original, every time.</p>
          </div>
          <span className="connection">
            {healthError
              ? 'API unavailable'
              : health
                ? 'API connected'
                : 'Connecting…'}
          </span>
        </div>
        {healthError && (
          <p className="error" role="alert">
            {healthError}{' '}
            <button className="text-button" onClick={refresh}>
              Retry connection
            </button>
          </p>
        )}
        <section className="upload-card" aria-labelledby="upload-title">
          <div>
            <h2 id="upload-title">Add a document</h2>
            <p>
              Original bytes stored locally. SHA-256 checked on upload and
              download.
            </p>
            <p className="muted">
              One file at a time
              {health
                ? ` · Up to ${formatBytes(health.max_upload_bytes)}`
                : ''}{' '}
              · No empty files
            </p>
          </div>
          <form onSubmit={(event) => void onUpload(event)}>
            <label htmlFor="document-file">Choose a fictional document</label>
            <div className="upload-actions">
              <input
                ref={fileInput}
                id="document-file"
                type="file"
                disabled={uploading}
                onChange={(event) => {
                  setFile(event.target.files?.[0] ?? null);
                  setUploadError('');
                  setNotice('');
                }}
              />
              <button
                className="primary"
                type="submit"
                disabled={!file || !health || uploading || !!healthError}
              >
                {uploading ? 'Verifying & uploading…' : 'Upload document'}
              </button>
            </div>
          </form>
        </section>
        <div role="status">
          {notice && <p className="success">{notice}</p>}
          {uploading && (
            <p className="muted">
              Calculating checksum and transferring your document…
            </p>
          )}
        </div>
        {uploadError && (
          <p className="error" role="alert">
            {uploadError}
          </p>
        )}
        <DocumentLibrary
          page={page}
          offset={offset}
          loading={loading}
          error={listError}
          onRefresh={refresh}
          onPageChange={setOffset}
        />
        <footer>
          FamilyVault <span>Milestone 1 · Local file server</span>
        </footer>
      </main>
    </>
  );
}
