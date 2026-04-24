package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── GraphMutator interface ──

func (s *PgStore) InsertEdges(ctx context.Context, txh types.TxHandle, fileID int64, edges []types.EdgeRecord, symbolIDs map[string]int64, language string) error {
	if len(edges) == 0 {
		return nil
	}
	tx := txh.(PgTxHandle).Tx

	for _, e := range edges {
		srcID := symbolIDs[e.SrcSymbolName]
		if srcID == 0 {
			continue
		}

		dstID := symbolIDs[e.DstSymbolName]
		if dstID == 0 {
			dstID = lookupSymbolIDPg(ctx, tx, e.DstSymbolName, fileID, language, s.projectID)
		}

		if dstID != 0 {
			_, err := tx.Exec(ctx,
				`INSERT INTO edges (src_symbol_id, dst_symbol_id, kind, file_id)
				 VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				srcID, dstID, e.Kind, fileID)
			if err != nil {
				return fmt.Errorf("insert edge %s→%s: %w", e.SrcSymbolName, e.DstSymbolName, err)
			}
		} else {
			_, err := tx.Exec(ctx,
				`INSERT INTO pending_edges (src_symbol_id, file_id, dst_symbol_name, dst_qualified_name, kind, src_language)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				srcID, fileID, e.DstSymbolName, e.DstQualifiedName, e.Kind, language)
			if err != nil {
				return fmt.Errorf("insert pending edge %s→%s: %w", e.SrcSymbolName, e.DstSymbolName, err)
			}
		}
	}
	return nil
}

func (s *PgStore) ResolvePendingEdges(ctx context.Context, txh types.TxHandle, newSymbolNames []string) error {
	if len(newSymbolNames) == 0 {
		return nil
	}
	tx := txh.(PgTxHandle).Tx

	rows, err := tx.Query(ctx, `
		SELECT id, src_symbol_id, file_id, dst_symbol_name, kind, src_language
		FROM pending_edges
		WHERE dst_symbol_name = ANY($1)`, newSymbolNames)
	if err != nil {
		return fmt.Errorf("query pending edges: %w", err)
	}
	defer rows.Close()

	type pending struct {
		id       int64
		srcID    int64
		fileID   int64
		dstName  string
		kind     string
		language string
	}
	var toResolve []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.srcID, &p.fileID, &p.dstName, &p.kind, &p.language); err != nil {
			return err
		}
		toResolve = append(toResolve, p)
	}

	for _, p := range toResolve {
		dstID := lookupSymbolIDPg(ctx, tx, p.dstName, 0, p.language, s.projectID)
		if dstID == 0 {
			continue
		}
		// Preserve file_id on resolved edges so DeleteEdgesByFile can cascade.
		if _, err := tx.Exec(ctx,
			`INSERT INTO edges (src_symbol_id, dst_symbol_id, kind, file_id)
			 VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			p.srcID, dstID, p.kind, p.fileID); err != nil {
			return fmt.Errorf("insert resolved edge %d→%d: %w", p.srcID, dstID, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM pending_edges WHERE id = $1", p.id); err != nil {
			return fmt.Errorf("delete resolved pending edge %d: %w", p.id, err)
		}
	}
	return nil
}

func (s *PgStore) DeleteEdgesByFile(ctx context.Context, txh types.TxHandle, fileID int64) error {
	tx := txh.(PgTxHandle).Tx
	_, err := tx.Exec(ctx, "DELETE FROM edges WHERE file_id = $1", fileID)
	return err
}

// PendingEdgeCallers returns src_symbol_ids for pending edges matching dstName.
// No project_id filter needed: pending edges are created by InsertEdges which only
// inserts src_symbol_ids from the current project (FK chain: pending_edges → symbols → files).
func (s *PgStore) PendingEdgeCallers(ctx context.Context, dstName string) ([]int64, error) {
	rows, err := s.query(ctx,
		"SELECT DISTINCT src_symbol_id FROM pending_edges WHERE dst_symbol_name = $1", dstName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pending edge caller id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PgStore) PendingEdgeCallersWithKind(ctx context.Context, dstName string) ([]types.PendingEdgeCaller, error) {
	rows, err := s.query(ctx, `
		SELECT src_symbol_id, kind, COALESCE(dst_qualified_name, '')
		FROM pending_edges WHERE dst_symbol_name = $1`, dstName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []types.PendingEdgeCaller
	for rows.Next() {
		var c types.PendingEdgeCaller
		if err := rows.Scan(&c.SrcSymbolID, &c.Kind, &c.DstQualifiedName); err != nil {
			return nil, fmt.Errorf("scan pending edge caller: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// ── Neighbors (recursive CTE) ──

// neighborsPg performs BFS traversal on the edges table.
// No project_id filter needed: cross-project edges cannot exist because InsertEdges
// and ResolvePendingEdges scope symbol lookup by project_id.
func neighborsPg(ctx context.Context, s *PgStore, symbolID int64, maxDepth int, direction string) ([]int64, error) {
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	switch direction {
	case "outgoing":
		return neighborsCTEPg(ctx, s, symbolID, maxDepth, `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT dst_symbol_id, 1 FROM edges WHERE src_symbol_id = $1
				UNION
				SELECT e.dst_symbol_id, r.depth + 1
				FROM edges e JOIN reachable r ON e.src_symbol_id = r.id
				WHERE r.depth < $2
			)
			SELECT DISTINCT id FROM reachable`)
	case "incoming":
		return neighborsCTEPg(ctx, s, symbolID, maxDepth, `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT src_symbol_id, 1 FROM edges WHERE dst_symbol_id = $1
				UNION
				SELECT e.src_symbol_id, r.depth + 1
				FROM edges e JOIN reachable r ON e.dst_symbol_id = r.id
				WHERE r.depth < $2
			)
			SELECT DISTINCT id FROM reachable`)
	case "both":
		out, err := neighborsPg(ctx, s, symbolID, maxDepth, "outgoing")
		if err != nil {
			return nil, err
		}
		in, err := neighborsPg(ctx, s, symbolID, maxDepth, "incoming")
		if err != nil {
			return nil, err
		}
		seen := make(map[int64]bool, len(out)+len(in))
		var result []int64
		for _, id := range append(out, in...) {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid direction: %s", direction)
	}
}

func neighborsCTEPg(ctx context.Context, s *PgStore, symbolID int64, maxDepth int, query string) ([]int64, error) {
	rows, err := s.query(ctx, query, symbolID, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("neighbors query: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan neighbor id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// lookupSymbolIDPg looks up a symbol by name within a transaction.
// projectID scopes the lookup to the current project, preventing cross-project resolution.
func lookupSymbolIDPg(ctx context.Context, tx pgx.Tx, name string, fileID int64, language string, projectID int64) int64 {
	var id int64

	// Prefer same-file match (fileID is already project-scoped).
	if fileID > 0 {
		err := tx.QueryRow(ctx,
			"SELECT id FROM symbols WHERE name = $1 AND file_id = $2 LIMIT 1",
			name, fileID).Scan(&id)
		if err == nil {
			return id
		}
	}

	// Fall back to same-language match within the project.
	if language != "" {
		err := tx.QueryRow(ctx,
			`SELECT s.id FROM symbols s JOIN files f ON s.file_id = f.id
			 WHERE s.name = $1 AND f.language = $2 AND f.project_id = $3 LIMIT 1`,
			name, language, projectID).Scan(&id)
		if err == nil {
			return id
		}
		return 0
	}

	// Bare name fallback, scoped to project.
	err := tx.QueryRow(ctx,
		`SELECT s.id FROM symbols s JOIN files f ON s.file_id = f.id
		 WHERE s.name = $1 AND f.project_id = $2 LIMIT 1`,
		name, projectID).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}
