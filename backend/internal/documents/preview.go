package documents

import (
	"context"

	"github.com/pinkhit/vault/backend/internal/audit"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/preview"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func (s *Service) Preview(ctx context.Context, actor auth.User, id string, page int) (preview.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, preview.Timeout)
	defer cancel()
	document, err := s.Get(ctx, actor, id) // SQL checks the enabled user, owner and explicit grants.
	if err != nil {
		return preview.Result{}, err
	}
	if document.Size > preview.MaxInputBytes {
		return preview.Result{}, preview.ErrLimit
	}
	file, err := s.Store.OpenVerifiedContext(ctx, id, document.Size, document.SHA256)
	if err != nil {
		return preview.Result{}, err
	}
	defer file.Close()
	result, err := preview.Render(ctx, file, document.Name, document.MediaType, document.Size, page)
	if err != nil {
		return preview.Result{}, err
	}
	// Recheck after expensive parsing so revocation during rendering is honored.
	current, err := s.Get(ctx, actor, id)
	if err != nil {
		return preview.Result{}, err
	}
	if current.SHA256 != document.SHA256 || current.Size != document.Size {
		return preview.Result{}, storage.ErrIntegrity
	}
	if err = audit.Record(ctx, s.DB, actor.ID, "document.preview", id, "allowed"); err != nil {
		return preview.Result{}, err // Never release content if the audit write fails.
	}
	return result, nil
}
