package documents

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/authorization"
)

const (
	MaxSearchBytes  = 512
	MaxSearchTerms  = 8
	MaxSearchLimit  = 50
	MaxSearchOffset = 10000
	MaxSnippetBytes = 768
)

var (
	ErrSearchQuery  = errors.New("search requires 1–512 UTF-8 bytes and at most 8 words or quoted phrases; operators are unsupported")
	ErrSearchBounds = errors.New("search limit must be 1–50 and offset must be 0–10000")
)

type SearchHit struct {
	Document Document `json:"document"`
	Snippet  string   `json:"snippet"`
	Match    string   `json:"match"`
	Page     int      `json:"page,omitempty"`
	Section  int      `json:"section,omitempty"`
}

type SearchPage struct {
	Results []SearchHit `json:"results"`
	HasMore bool        `json:"has_more"`
}

// Search filters both metadata and passages using current database authority
// before document deduplication, ranking and pagination. Ranking uses only each
// row's field/match frequency, never global statistics influenced by hidden rows.
func (s *Service) Search(ctx context.Context, actor auth.User, query string, limit, offset int) (SearchPage, error) {
	expression, err := searchExpression(query)
	if err != nil {
		return SearchPage{}, err
	}
	if limit < 1 || limit > MaxSearchLimit || offset < 0 || offset > MaxSearchOffset {
		return SearchPage{}, ErrSearchBounds
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `
        WITH hits AS MATERIALIZED (
            SELECT d.id,d.name,d.size,d.media_type,d.sha256,d.created_at,d.owner_id,COALESCE(j.status,'') AS indexing_status,
                   snippet(document_fts,-1,'','',' … ',24) AS excerpt,
                   CASE WHEN instr(highlight(document_fts,1,char(1),''),char(1)) > 0 THEN 'filename' ELSE 'metadata' END AS kind,
                   CASE WHEN instr(highlight(document_fts,1,char(1),''),char(1)) > 0 THEN 3 ELSE 2 END AS priority,
                   min(16,length(highlight(document_fts,1,char(1),''))-length(document_fts.name)
                          +length(highlight(document_fts,2,char(1),''))-length(document_fts.metadata)) AS frequency,
                   0 AS page,0 AS section
            FROM document_fts JOIN documents d ON d.id = document_fts.document_id
            JOIN users u ON u.id = ?
            LEFT JOIN document_index_jobs j ON j.document_id = d.id AND j.source_sha256 = d.sha256
            WHERE document_fts MATCH ? AND `+authorization.Visible+`
            UNION ALL
            SELECT d.id,d.name,d.size,d.media_type,d.sha256,d.created_at,d.owner_id,j.status,
                   snippet(passage_fts,0,'','',' … ',24), 'content',1,
                   min(16,length(highlight(passage_fts,0,char(1),''))-length(p.text)),p.page,p.section
            FROM passage_fts JOIN document_passages p ON p.id = passage_fts.rowid
            JOIN documents d ON d.id = p.document_id AND d.sha256 = p.source_sha256
            JOIN document_index_jobs j ON j.document_id = d.id AND j.source_sha256 = d.sha256 AND j.status = 'ready'
            JOIN users u ON u.id = ?
            WHERE passage_fts MATCH ? AND `+authorization.Visible+`
        ), ranked AS (
            SELECT *,row_number() OVER (
                PARTITION BY id ORDER BY priority DESC,frequency DESC,page,section
            ) AS position FROM hits
        )
        SELECT id,name,size,media_type,sha256,created_at,owner_id,indexing_status,excerpt,kind,page,section
        FROM ranked WHERE position = 1
        ORDER BY priority DESC,frequency DESC,created_at DESC,id
        LIMIT ? OFFSET ?`, actor.ID, expression, actor.ID, expression, limit+1, offset)
	if err != nil {
		return SearchPage{}, err
	}
	defer rows.Close()
	page := SearchPage{Results: make([]SearchHit, 0)}
	for rows.Next() {
		var hit SearchHit
		var createdAt string
		var ownerID *string
		err = rows.Scan(&hit.Document.ID, &hit.Document.Name, &hit.Document.Size, &hit.Document.MediaType,
			&hit.Document.SHA256, &createdAt, &ownerID, &hit.Document.IndexingStatus, &hit.Snippet, &hit.Match, &hit.Page, &hit.Section)
		if err != nil {
			return SearchPage{}, err
		}
		hit.Document.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return SearchPage{}, err
		}
		if ownerID != nil {
			hit.Document.OwnerID = *ownerID
		}
		hit.Snippet = trimSnippet(hit.Snippet)
		page.Results = append(page.Results, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchPage{}, err
	}
	page.HasMore = len(page.Results) > limit
	if page.HasMore {
		page.Results = page.Results[:limit]
	}
	return page, nil
}

// This is a small literal-query grammar, not a pass-through FTS expression.
// Each clause is quoted, so SQL/FTS operators can never become executable syntax.
func searchExpression(query string) (string, error) {
	if len(query) > MaxSearchBytes || !utf8.ValidString(query) {
		return "", ErrSearchQuery
	}
	for _, r := range query {
		if unicode.IsControl(r) {
			return "", ErrSearchQuery
		}
	}
	var terms []string
	for rest := strings.TrimSpace(query); rest != ""; {
		var term string
		if rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return "", ErrSearchQuery
			}
			term, rest = rest[1:end+1], rest[end+2:]
			if rest != "" && !unicode.IsSpace([]rune(rest)[0]) {
				return "", ErrSearchQuery
			}
		} else {
			end := strings.IndexFunc(rest, unicode.IsSpace)
			if end < 0 {
				end = len(rest)
			}
			term, rest = rest[:end], rest[end:]
			if strings.ContainsAny(term, `"*^:(){}`) {
				return "", ErrSearchQuery
			}
		}
		if !strings.ContainsFunc(term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			return "", ErrSearchQuery
		}
		terms = append(terms, `"`+term+`"`)
		if len(terms) > MaxSearchTerms {
			return "", ErrSearchQuery
		}
		rest = strings.TrimSpace(rest)
	}
	if len(terms) == 0 {
		return "", ErrSearchQuery
	}
	return strings.Join(terms, " AND "), nil
}

func trimSnippet(text string) string {
	if len(text) <= MaxSnippetBytes {
		return text
	}
	end := MaxSnippetBytes - len("…")
	for !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}
