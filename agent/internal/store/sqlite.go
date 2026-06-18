package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/autosre/agent/internal/contracts"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS incidents (
    id              TEXT    PRIMARY KEY,
    correlation_key TEXT    NOT NULL,
    severity        TEXT    NOT NULL,
    opened_at       INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    resolved_at     INTEGER NOT NULL DEFAULT 0,
    signals_json    TEXT    NOT NULL DEFAULT '[]',
    affected_json   TEXT    NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_incidents_open ON incidents (resolved_at);

CREATE TABLE IF NOT EXISTS pending_approvals (
    request_id    TEXT    PRIMARY KEY,
    incident_id   TEXT    NOT NULL,
    proposal_json TEXT    NOT NULL,
    requested_at  INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS circuit_breaker_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    recorded_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cb_recorded ON circuit_breaker_events (recorded_at);

CREATE TABLE IF NOT EXISTS integration_settings (
    key        TEXT    PRIMARY KEY,
    value      BLOB    NOT NULL,
    updated_at INTEGER NOT NULL
);
`

// SQLiteStore is a Store backed by a local SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dsn and applies the schema.
// Recommended DSN: "file:./data/autosre.db?_journal_mode=WAL"
func Open(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	// Limit to one writer; WAL still allows concurrent readers.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) UpsertIncident(ctx context.Context, inc contracts.Incident, correlationKey string) error {
	sigJSON, err := json.Marshal(inc.Signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}
	affJSON, err := json.Marshal(inc.AffectedResources)
	if err != nil {
		return fmt.Errorf("marshal resources: %w", err)
	}
	var resolvedAt int64
	if !inc.ResolvedAt.IsZero() {
		resolvedAt = inc.ResolvedAt.UnixMilli()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO incidents
		    (id, correlation_key, severity, opened_at, updated_at, resolved_at, signals_json, affected_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    severity      = excluded.severity,
		    updated_at    = excluded.updated_at,
		    resolved_at   = excluded.resolved_at,
		    signals_json  = excluded.signals_json,
		    affected_json = excluded.affected_json`,
		inc.ID, correlationKey, inc.Severity,
		inc.OpenedAt.UnixMilli(), inc.UpdatedAt.UnixMilli(), resolvedAt,
		string(sigJSON), string(affJSON),
	)
	return err
}

func (s *SQLiteStore) LoadOpenIncidents(ctx context.Context) ([]IncidentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, correlation_key, severity, opened_at, updated_at, signals_json, affected_json
		FROM incidents WHERE resolved_at = 0`)
	if err != nil {
		return nil, fmt.Errorf("query open incidents: %w", err)
	}
	defer rows.Close()

	var out []IncidentRecord
	for rows.Next() {
		var rec IncidentRecord
		var openedMs, updatedMs int64
		var sigJSON, affJSON string
		if err := rows.Scan(
			&rec.Incident.ID, &rec.CorrelationKey, &rec.Incident.Severity,
			&openedMs, &updatedMs, &sigJSON, &affJSON,
		); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		rec.Incident.OpenedAt = time.UnixMilli(openedMs)
		rec.Incident.UpdatedAt = time.UnixMilli(updatedMs)
		if err := json.Unmarshal([]byte(sigJSON), &rec.Incident.Signals); err != nil {
			return nil, fmt.Errorf("unmarshal signals for %s: %w", rec.Incident.ID, err)
		}
		if err := json.Unmarshal([]byte(affJSON), &rec.Incident.AffectedResources); err != nil {
			return nil, fmt.Errorf("unmarshal resources for %s: %w", rec.Incident.ID, err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertApproval(ctx context.Context, rec ApprovalRecord) error {
	propJSON, err := json.Marshal(rec.Proposal)
	if err != nil {
		return fmt.Errorf("marshal proposal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pending_approvals
		    (request_id, incident_id, proposal_json, requested_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
		    proposal_json = excluded.proposal_json,
		    expires_at    = excluded.expires_at`,
		rec.RequestID, rec.IncidentID, string(propJSON),
		rec.RequestedAt.UnixMilli(), rec.ExpiresAt.UnixMilli(),
	)
	return err
}

func (s *SQLiteStore) DeleteApproval(ctx context.Context, requestID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_approvals WHERE request_id = ?`, requestID)
	return err
}

func (s *SQLiteStore) LoadPendingApprovals(ctx context.Context) ([]ApprovalRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT request_id, incident_id, proposal_json, requested_at, expires_at
		FROM pending_approvals WHERE expires_at > ?`, time.Now().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("query pending approvals: %w", err)
	}
	defer rows.Close()

	var out []ApprovalRecord
	for rows.Next() {
		var rec ApprovalRecord
		var propJSON string
		var reqMs, expMs int64
		if err := rows.Scan(&rec.RequestID, &rec.IncidentID, &propJSON, &reqMs, &expMs); err != nil {
			return nil, fmt.Errorf("scan approval: %w", err)
		}
		rec.RequestedAt = time.UnixMilli(reqMs)
		rec.ExpiresAt = time.UnixMilli(expMs)
		if err := json.Unmarshal([]byte(propJSON), &rec.Proposal); err != nil {
			return nil, fmt.Errorf("unmarshal proposal for %s: %w", rec.RequestID, err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetSetting(ctx context.Context, key string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM integration_settings WHERE key = ?`, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, true, nil
}

func (s *SQLiteStore) PutSetting(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO integration_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
		    value      = excluded.value,
		    updated_at = excluded.updated_at`,
		key, value, time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("put setting %q: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM integration_settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) RecordCBEvent(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO circuit_breaker_events (recorded_at) VALUES (?)`,
		time.Now().UnixMilli(),
	)
	return err
}

func (s *SQLiteStore) LoadCBEvents(ctx context.Context, since time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT recorded_at FROM circuit_breaker_events WHERE recorded_at > ? ORDER BY recorded_at`,
		since.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("query cb events: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return nil, fmt.Errorf("scan cb event: %w", err)
		}
		out = append(out, time.UnixMilli(ms))
	}
	return out, rows.Err()
}
