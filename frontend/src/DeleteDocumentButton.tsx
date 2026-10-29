import { useState } from 'react';
import {
  APIError,
  deleteDocument,
  type DocumentRecord,
  type User,
} from './api';

export default function DeleteDocumentButton({
  document,
  user,
  onDeleted,
  onUnauthorized,
}: {
  document: DocumentRecord;
  user: User;
  onDeleted: () => void;
  onUnauthorized: () => void;
}) {
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState('');
  if (!user.is_admin && document.owner_id !== user.id) return null;

  async function remove() {
    if (
      deleting ||
      !window.confirm(
        `Permanently delete “${document.name}”? This cannot be undone.`,
      )
    )
      return;
    setDeleting(true);
    setError('');
    try {
      await deleteDocument(document.id);
      onDeleted();
    } catch (error: unknown) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else
        setError(
          error instanceof Error
            ? error.message
            : 'Deletion failed. Please try again.',
        );
    } finally {
      setDeleting(false);
    }
  }

  return (
    <>
      <button
        className="secondary delete-button"
        aria-label={`Delete ${document.name}`}
        disabled={deleting}
        onClick={() => void remove()}
      >
        {deleting ? 'Deleting…' : 'Delete'}
      </button>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </>
  );
}
