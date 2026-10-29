import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react';
import {
  APIError,
  MAX_SEARCH_OFFSET,
  SEARCH_PAGE_SIZE,
  searchDocuments,
  type SearchPage,
  type DocumentRecord,
} from './api';
import { formatBytes } from './format';
import IndexingBadge from './IndexingBadge';
import Icon from './Icon';

type Query = { text: string; offset: number };
type SearchResponse = {
  query: Query;
  revision: number;
  page?: SearchPage;
  error?: string;
};

export default function DocumentSearch({
  revision,
  onUnauthorized,
  onClear,
  onPreview,
  renderDelete,
  children,
}: {
  revision: number;
  onUnauthorized: () => void;
  onClear: () => void;
  onPreview: (document: DocumentRecord, page?: number) => void;
  renderDelete?: (document: DocumentRecord) => ReactNode;
  children: ReactNode;
}) {
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState<Query | null>(null);
  const [response, setResponse] = useState<SearchResponse | null>(null);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!query) return;
    if (new TextEncoder().encode(query.text).length > 512) {
      setResponse({
        query,
        revision,
        error: 'Search is too long. Try fewer words.',
      });
      return;
    }
    const controller = new AbortController();
    searchDocuments(query.text, query.offset, controller.signal)
      .then((page) => {
        if (!controller.signal.aborted) setResponse({ query, revision, page });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && error.status === 401) {
          onUnauthorized();
        } else {
          setResponse({
            query,
            revision,
            error:
              error instanceof Error
                ? error.message
                : 'Search failed. Please try again.',
          });
        }
      });
    return () => controller.abort();
  }, [query, revision, onUnauthorized]);

  // A response belongs to one submission and refresh. Old requests cannot put
  // previous snippets back on screen, even if the transport ignores cancellation.
  const current =
    response?.query === query && response?.revision === revision
      ? response
      : null;
  const loading = query !== null && current === null;
  const page = current?.page;
  const resultCount = page?.results.length ?? 0;
  const canNext =
    !!query &&
    !!page?.has_more &&
    query.offset + SEARCH_PAGE_SIZE <= MAX_SEARCH_OFFSET;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const text = draft.trim();
    if (text) setQuery({ text, offset: 0 });
  }

  function clear() {
    setDraft('');
    setQuery(null);
    setResponse(null);
    if (query) onClear();
    input.current?.focus();
  }

  return (
    <>
      <section className="search-card" aria-labelledby="search-title">
        <h2 id="search-title" className="sr-only">
          Search documents
        </h2>
        <form role="search" onSubmit={submit}>
          <label className="sr-only" htmlFor="document-search">
            Search documents
          </label>
          <div className="search-controls">
            <Icon name="search" />
            <input
              ref={input}
              id="document-search"
              type="search"
              value={draft}
              maxLength={512}
              autoComplete="off"
              placeholder="Search your documents"
              onChange={(event) => setDraft(event.target.value)}
            />
            <button className="primary" type="submit" disabled={!draft.trim()}>
              Search
            </button>
            {(query || draft) && (
              <button className="secondary" type="button" onClick={clear}>
                Clear search
              </button>
            )}
          </div>
        </form>
      </section>
      {!query ? (
        children
      ) : (
        <section
          className="document-card"
          aria-labelledby="search-results-title"
          aria-busy={loading}
        >
          <div className="section-heading search-heading">
            <div>
              <h2 id="search-results-title">Search results</h2>
              <p>For “{query.text}”</p>
            </div>
            <button
              className="secondary"
              disabled={loading}
              onClick={() => setQuery({ ...query })}
            >
              Refresh results
            </button>
          </div>
          <p className="search-summary" role="status">
            {loading
              ? 'Searching documents…'
              : current?.error
                ? 'Search unavailable'
                : `${resultCount} result${resultCount === 1 ? '' : 's'} on this page`}
          </p>
          {current?.error ? (
            <div className="empty error" role="alert">
              <p>{current.error}</p>
              <button
                className="secondary"
                onClick={() => setQuery({ ...query })}
              >
                Try search again
              </button>
            </div>
          ) : !loading && page?.results.length === 0 ? (
            <div className="empty">
              <h3>
                {query.offset === 0
                  ? 'No matching documents'
                  : 'No results on this page'}
              </h3>
              <p>
                Try different words or refresh after indexing. File details
                remain searchable when text is unavailable.
              </p>
            </div>
          ) : !loading && page ? (
            <ul className="search-results" aria-label="Matching documents">
              {page.results.map((hit) => (
                <li className="search-result" key={hit.document.id}>
                  <div className="search-result-heading">
                    <h3>
                      <a
                        href={`/api/documents/${encodeURIComponent(hit.document.id)}/download`}
                        aria-label={`Download ${hit.document.name}`}
                      >
                        {hit.document.name}
                      </a>
                    </h3>
                    <IndexingBadge status={hit.document.indexing_status} />
                  </div>
                  <p className="search-reference">
                    {hit.match === 'filename'
                      ? 'Filename match'
                      : hit.match === 'metadata'
                        ? 'File details match'
                        : 'Text match'}
                    {hit.page ? ` · Page ${hit.page}` : ''}
                    {hit.section ? ` · Section ${hit.section}` : ''}
                    {' · '}
                    {formatBytes(hit.document.size)}
                  </p>
                  <p className="search-snippet">{hit.snippet}</p>
                  <div className="document-actions">
                    <button
                      className="secondary"
                      aria-label={`Preview ${hit.document.name}`}
                      onClick={() => onPreview(hit.document, hit.page)}
                    >
                      Preview{hit.page ? ` page ${hit.page}` : ''}
                    </button>
                    {renderDelete?.(hit.document)}
                  </div>
                </li>
              ))}
            </ul>
          ) : null}
          <div className="pagination">
            <span>
              {!loading && page?.results.length
                ? `Showing ${query.offset + 1}–${query.offset + page.results.length}`
                : 'Search your documents'}
            </span>
            <div>
              <button
                className="secondary"
                disabled={query.offset === 0 || loading}
                onClick={() =>
                  setQuery({
                    ...query,
                    offset: Math.max(0, query.offset - SEARCH_PAGE_SIZE),
                  })
                }
              >
                Previous results
              </button>
              <button
                className="secondary"
                disabled={!canNext || loading || !!current?.error}
                onClick={() =>
                  setQuery({
                    ...query,
                    offset: query.offset + SEARCH_PAGE_SIZE,
                  })
                }
              >
                Next results
              </button>
            </div>
          </div>
          {page?.has_more && !canNext && (
            <p className="search-summary">
              Narrow your search to see more matches.
            </p>
          )}
        </section>
      )}
    </>
  );
}
