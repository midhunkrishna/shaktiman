package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// InsertEdges inserts resolved edges into the edges table and queues
// unresolved edges into pending_edges. symbolIDs maps symbol names to their
// database IDs from the current file. Uses tx to see uncommitted symbols.
// language is the source file's language, used for language-filtered lookup
// and stored on pending_edges for cross-language misresolution prevention.
func (s *Store) InsertEdges(ctx context.Context, txh types.TxHandle, fileID int64, edges []types.EdgeRecord, symbolIDs map[string]int64, language string) error {
	if len(edges) == 0 {
		return nil
	}
	tx := txh.(TxHandle).Tx

	edgeStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO edges (src_symbol_id, dst_symbol_id, kind, file_id)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare edge insert: %w", err)
	}
	defer func() { _ = edgeStmt.Close() }()

	pendingStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO pending_edges (src_symbol_id, file_id, dst_symbol_name, dst_qualified_name, kind, src_language)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare pending edge insert: %w", err)
	}
	defer func() { _ = pendingStmt.Close() }()

	for _, e := range edges {
		srcID := symbolIDs[e.SrcSymbolName]
		if srcID == 0 {
			continue
		}

		dstID := symbolIDs[e.DstSymbolName]
		if dstID == 0 {
			// Lookup via tx, preferring same-file, then same-language
			dstID, _ = lookupSymbolIDTx(ctx, tx, e.DstSymbolName, fileID, language)
		}

		if dstID != 0 {
			if _, err := edgeStmt.ExecContext(ctx, srcID, dstID, e.Kind, fileID); err != nil {
				return fmt.Errorf("insert edge %s→%s: %w", e.SrcSymbolName, e.DstSymbolName, err)
			}
		} else {
			if _, err := pendingStmt.ExecContext(ctx, srcID, fileID, e.DstSymbolName, e.DstQualifiedName, e.Kind, language); err != nil {
				return fmt.Errorf("insert pending edge %s→%s: %w", e.SrcSymbolName, e.DstSymbolName, err)
			}
		}
	}

	return nil
}

// ResolvePendingEdges attempts to resolve pending edges whose dst_symbol_name
// matches any of the given newly-inserted symbol names. Uses src_language
// to ensure a Java import never resolves to a Python symbol.
func (s *Store) ResolvePendingEdges(ctx context.Context, txh types.TxHandle, newSymbolNames []string) error {
	if len(newSymbolNames) == 0 {
		return nil
	}
	tx := txh.(TxHandle).Tx

	placeholders := make([]string, len(newSymbolNames))
	args := make([]any, len(newSymbolNames))
	for i, name := range newSymbolNames {
		placeholders[i] = "?"
		args[i] = name
	}

	query := fmt.Sprintf(`
		SELECT id, src_symbol_id, file_id, dst_symbol_name, kind, src_language
		FROM pending_edges
		WHERE dst_symbol_name IN (%s)`, strings.Join(placeholders, ","))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query pending edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

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
			return fmt.Errorf("scan pending edge: %w", err)
		}
		toResolve = append(toResolve, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate pending edges: %w", err)
	}

	if len(toResolve) == 0 {
		return nil
	}

	// Preserve file_id on resolved edges so DeleteEdgesByFile can cascade.
	edgeStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO edges (src_symbol_id, dst_symbol_id, kind, file_id)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare edge resolve insert: %w", err)
	}
	defer func() { _ = edgeStmt.Close() }()

	delStmt, err := tx.PrepareContext(ctx, `DELETE FROM pending_edges WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare pending delete: %w", err)
	}
	defer func() { _ = delStmt.Close() }()

	for _, p := range toResolve {
		dstID, err := lookupSymbolIDTx(ctx, tx, p.dstName, 0, p.language)
		if err != nil || dstID == 0 {
			continue
		}

		if _, err := edgeStmt.ExecContext(ctx, p.srcID, dstID, p.kind, p.fileID); err != nil {
			return fmt.Errorf("resolve edge %d: %w", p.id, err)
		}
		if _, err := delStmt.ExecContext(ctx, p.id); err != nil {
			return fmt.Errorf("delete resolved pending %d: %w", p.id, err)
		}
	}

	return nil
}

// Neighbors performs BFS via a SQLite recursive CTE starting from symbolID.
// direction: "outgoing" (follow edges from src), "incoming" (follow edges to dst), "both".
// Returns distinct symbol IDs reachable within maxDepth hops.
func (s *Store) Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error) {
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 10 {
		maxDepth = 10
	}

	switch direction {
	case "outgoing":
		return s.neighborsCTE(ctx, symbolID, maxDepth, `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT dst_symbol_id, 1 FROM edges WHERE src_symbol_id = ?
				UNION
				SELECT e.dst_symbol_id, r.depth + 1
				FROM edges e JOIN reachable r ON e.src_symbol_id = r.id
				WHERE r.depth < ?
			)
			SELECT DISTINCT id FROM reachable`)

	case "incoming":
		return s.neighborsCTE(ctx, symbolID, maxDepth, `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT src_symbol_id, 1 FROM edges WHERE dst_symbol_id = ?
				UNION
				SELECT e.src_symbol_id, r.depth + 1
				FROM edges e JOIN reachable r ON e.dst_symbol_id = r.id
				WHERE r.depth < ?
			)
			SELECT DISTINCT id FROM reachable`)

	case "both":
		out, err := s.Neighbors(ctx, symbolID, maxDepth, "outgoing")
		if err != nil {
			return nil, err
		}
		in, err := s.Neighbors(ctx, symbolID, maxDepth, "incoming")
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

func (s *Store) neighborsCTE(ctx context.Context, symbolID int64, maxDepth int, query string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, query, symbolID, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("neighbors query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan neighbor: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PendingEdgeCallers returns src_symbol_ids from pending_edges where the
// unresolved destination matches the given name. This enables finding callers
// of external/library symbols (e.g. "ExecutorService") that are never defined
// in the project and therefore remain in pending_edges permanently.
func (s *Store) PendingEdgeCallers(ctx context.Context, dstName string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT src_symbol_id
		FROM pending_edges
		WHERE dst_symbol_name = ?`, dstName)
	if err != nil {
		return nil, fmt.Errorf("pending edge callers for %s: %w", dstName, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pending caller: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PendingEdgeCaller is a backward-compatible alias for types.PendingEdgeCaller.
type PendingEdgeCaller = types.PendingEdgeCaller

// PendingEdgeCallersWithKind returns src_symbol_id, kind, and dst_qualified_name
// from pending_edges for a given unresolved destination name. Used by the symbols
// handler to show which symbols reference an external type.
func (s *Store) PendingEdgeCallersWithKind(ctx context.Context, dstName string) ([]PendingEdgeCaller, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT src_symbol_id, kind, dst_qualified_name
		FROM pending_edges
		WHERE dst_symbol_name = ?`, dstName)
	if err != nil {
		return nil, fmt.Errorf("pending edge callers with kind for %s: %w", dstName, err)
	}
	defer func() { _ = rows.Close() }()

	var results []PendingEdgeCaller
	for rows.Next() {
		var c PendingEdgeCaller
		if err := rows.Scan(&c.SrcSymbolID, &c.Kind, &c.DstQualifiedName); err != nil {
			return nil, fmt.Errorf("scan pending caller with kind: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// DeleteEdgesByFile removes all edges originating from a given file.
func (s *Store) DeleteEdgesByFile(ctx context.Context, txh types.TxHandle, fileID int64) error {
	tx := txh.(TxHandle).Tx
	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE file_id = ?", fileID); err != nil {
		return fmt.Errorf("delete edges for file %d: %w", fileID, err)
	}
	return nil
}

// lookupSymbolIDTx looks up a symbol by name within a write transaction,
// ensuring uncommitted symbols from the current transaction are visible.
// Prefers same-file match, then same-language match, then global fallback.
// Pass language="" for non-import edges (calls, inherits) where cross-language
// is not a concern.
func lookupSymbolIDTx(ctx context.Context, tx *sql.Tx, name string, fileID int64, language string) (int64, error) {
	var id int64
	// Try same-file first
	if fileID > 0 {
		err := tx.QueryRowContext(ctx,
			"SELECT id FROM symbols WHERE name = ? AND file_id = ? LIMIT 1",
			name, fileID).Scan(&id)
		if err == nil {
			return id, nil
		}
	}
	// Try same-language match
	if language != "" {
		err := tx.QueryRowContext(ctx,
			`SELECT s.id FROM symbols s JOIN files f ON s.file_id = f.id
			 WHERE s.name = ? AND f.language = ? LIMIT 1`,
			name, language).Scan(&id)
		if err == nil {
			return id, nil
		}
		// When a language is specified, do NOT fall back to global lookup.
		// This prevents cross-language misresolution (e.g. Java import
		// resolving to a Python symbol).
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("lookup symbol %s (lang=%s): %w", name, language, err)
	}
	// Global fallback: no language constraint (for non-import edges
	// and pre-migration pending edges with empty src_language)
	err := tx.QueryRowContext(ctx, "SELECT id FROM symbols WHERE name = ? LIMIT 1", name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("lookup symbol %s: %w", name, err)
	}
	return id, nil
}
