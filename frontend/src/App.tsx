import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  getHealth,
  getSession,
  logout,
  APIError,
  type Session,
  listDocuments,
  uploadDocument,
  type DocumentPage,
  type Health,
  type DocumentRecord,
} from './api';
import DocumentLibrary from './DocumentLibrary';
import DeleteDocumentButton from './DeleteDocumentButton';
import DocumentSearch from './DocumentSearch';
import DocumentPreview from './DocumentPreview';
import StorageOverview from './StorageOverview';
import { formatBytes } from './format';
import LoginForm from './LoginForm';
import Brand from './Brand';
import Icon from './Icon';

const errorMessage = (error: unknown) =>
  error instanceof Error
    ? error.message
    : 'Something went wrong. Please try again.';

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setChecking(true);
    setError('');
    getSession(controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setSession(result);
      })
      .catch((error: unknown) => {
        if (
          !controller.signal.aborted &&
          !(error instanceof APIError && error.status === 401)
        ) {
          setError(errorMessage(error));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setChecking(false);
      });
    return () => controller.abort();
  }, [retry]);

  if (checking)
    return (
      <main>
        <p role="status">Checking your session…</p>
      </main>
    );
  if (error)
    return (
      <main>
        <p className="error" role="alert">
          {error}
        </p>
        <button
          className="secondary"
          onClick={() => setRetry((value) => value + 1)}
        >
          Retry connection
        </button>
      </main>
    );
  if (!session) return <LoginForm onLogin={setSession} />;
  return (
    <DocumentBrowser
      key={session.user.id}
      session={session}
      onSignOut={() => setSession(null)}
    />
  );
}

function DocumentBrowser({
  session,
  onSignOut,
}: {
  session: Session;
  onSignOut: () => void;
}) {
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
  const [uploadProgress, setUploadProgress] = useState<number | null>(0);
  const [uploadError, setUploadError] = useState('');
  const [notice, setNotice] = useState('');
  const [signingOut, setSigningOut] = useState(false);
  const [showStorage, setShowStorage] = useState(false);
  const [libraryRevision, setLibraryRevision] = useState(0);
  const [preview, setPreview] = useState<{
    document: DocumentRecord;
    page: number;
  } | null>(null);
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
          if (error instanceof APIError && error.status === 401) {
            onSignOut();
          } else {
            setListError(errorMessage(error));
          }
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
    setLoading(true);
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
    setUploadProgress(0);
    try {
      const result = await uploadDocument(file, setUploadProgress);
      setNotice(`${result.name} uploaded.`);
      setFile(null);
      if (fileInput.current) {
        fileInput.current.value = '';
      }
      setOffset(0);
      refresh();
    } catch (error: unknown) {
      if (error instanceof APIError && error.status === 401) onSignOut();
      else setUploadError(errorMessage(error));
    } finally {
      setUploading(false);
    }
  }

  async function signOut(allDevices = false) {
    setSigningOut(true);
    try {
      await logout(allDevices);
      onSignOut();
    } catch (error: unknown) {
      if (error instanceof APIError && error.status === 401) onSignOut();
      else setUploadError(errorMessage(error));
    } finally {
      setSigningOut(false);
    }
  }

  function renderDelete(document: DocumentRecord) {
    return (
      <DeleteDocumentButton
        document={document}
        user={session.user}
        onUnauthorized={onSignOut}
        onDeleted={() => {
          setNotice(`${document.name} deleted.`);
          setPreview((current) =>
            current?.document.id === document.id ? null : current,
          );
          setOffset(0);
          refresh();
        }}
      />
    );
  }

  return (
    <div className="vault-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <aside className="sidebar">
        <Brand />
        <p className="sidebar-label">WORKSPACE</p>
        <nav aria-label="Main navigation">
          <button
            className={!showStorage ? 'nav-item active' : 'nav-item'}
            aria-current={!showStorage ? 'page' : undefined}
            onClick={() => {
              setShowStorage(false);
              setOffset(0);
              setLibraryRevision((value) => value + 1);
              refresh();
            }}
          >
            <Icon name="documents" /> Documents
          </button>
          {session.user.is_admin && (
            <button
              className={showStorage ? 'nav-item active' : 'nav-item'}
              aria-label="Storage overview"
              aria-expanded={showStorage}
              aria-controls="storage-overview"
              onClick={() => setShowStorage((value) => !value)}
            >
              <Icon name="storage" /> Storage
            </button>
          )}
        </nav>
      </aside>
      <div className="workspace">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">YOUR WORKSPACE</p>
            <h1>{showStorage ? 'Storage' : 'Documents'}</h1>
          </div>
          <details className="account-menu">
            <summary aria-label="Account options">
              <span className="avatar" aria-hidden="true">
                {session.user.username.slice(0, 1).toUpperCase()}
              </span>
              <span className="account-name">{session.user.username}</span>
              <Icon name="chevron" />
            </summary>
            <div className="account-options">
              <p>{session.user.is_admin ? 'Administrator' : 'Your account'}</p>
              <button
                disabled={uploading || signingOut}
                onClick={() => void signOut()}
              >
                Sign out
              </button>
              <button
                disabled={uploading || signingOut}
                onClick={() => void signOut(true)}
              >
                Sign out all devices
              </button>
            </div>
          </details>
        </header>
        <main id="main-content" className="workspace-main">
          <div role="status">
            {notice && <p className="success">{notice}</p>}
            {uploading && (
              <div className="upload-progress">
                <progress
                  aria-label="Upload progress"
                  max={100}
                  value={uploadProgress ?? undefined}
                />
                <span>
                  {uploadProgress === null
                    ? 'Uploading…'
                    : uploadProgress === 100
                      ? 'Finishing upload…'
                      : `Uploading ${uploadProgress}%`}
                </span>
              </div>
            )}
          </div>
          {uploadError && (
            <p className="error" role="alert">
              {uploadError}
            </p>
          )}
          {showStorage && session.user.is_admin ? (
            <StorageOverview onUnauthorized={onSignOut} />
          ) : (
            <DocumentSearch
              key={libraryRevision}
              revision={revision}
              onUnauthorized={onSignOut}
              onClear={refresh}
              onPreview={(document, page = 1) => setPreview({ document, page })}
              renderDelete={renderDelete}
            >
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
                    {health
                      ? `Up to ${formatBytes(health.max_upload_bytes)} per file`
                      : 'Checking upload availability…'}
                  </p>
                </div>
                <form onSubmit={(event) => void onUpload(event)}>
                  <label className="sr-only" htmlFor="document-file">
                    Choose a document
                  </label>
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
                      <Icon name="upload" />{' '}
                      {uploading ? 'Uploading…' : 'Upload document'}
                    </button>
                  </div>
                </form>
              </section>
              <DocumentLibrary
                page={page}
                offset={offset}
                loading={loading}
                error={listError}
                onRefresh={refresh}
                onPageChange={setOffset}
                onPreview={(document) => setPreview({ document, page: 1 })}
                renderDelete={renderDelete}
              />
            </DocumentSearch>
          )}
          {preview && (
            <DocumentPreview
              key={`${preview.document.id}:${preview.page}`}
              document={preview.document}
              initialPage={preview.page}
              onClose={() => setPreview(null)}
              onUnauthorized={onSignOut}
            />
          )}
          <footer>
            <span>SafeVault</span>
            <span>Your document workspace</span>
          </footer>
        </main>
      </div>
    </div>
  );
}
