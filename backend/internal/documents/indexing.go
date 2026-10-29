package documents

import (
	"context"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
	"github.com/pinkhit/vault/backend/internal/indexing"
)

type IndexingProgress struct {
	Status        indexing.Status `json:"status"`
	Attempts      int             `json:"attempts"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
	NextAttemptAt int64           `json:"next_attempt_at,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
}

func (s *Service) Indexing(ctx context.Context, actor auth.User, id string) (IndexingProgress, error) {
	var progress IndexingProgress
	err := s.DB.QueryRowContext(ctx, `
        SELECT j.status,j.attempts,j.created_at,j.updated_at,
               CASE WHEN j.status = 'pending' THEN j.available_at ELSE 0 END,j.last_error
        FROM documents d JOIN users u ON u.id = ?
        JOIN document_index_jobs j ON j.document_id = d.id AND j.source_sha256 = d.sha256
        WHERE d.id = ? AND `+authorization.Visible, actor.ID, id).
		Scan(&progress.Status, &progress.Attempts, &progress.CreatedAt, &progress.UpdatedAt, &progress.NextAttemptAt, &progress.LastError)
	return progress, err
}
