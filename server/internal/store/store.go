package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

// Store wraps the SQLite WAL database.
type Store struct {
	db *sql.DB
}

// MonitorRow represents a row from the monitors table.
type MonitorRow struct {
	ID               string
	Name             string
	GroupKey         string
	Kind             string
	Cluster          string
	Visibility       string
	Severity         string
	CurrentState     string
	ConsecutiveFails int
	ConsecutiveOKs   int
	LastLatencyMs    *int64
	LastMessage      string
	MetaJSON         string
	LastCheckedAt    int64
	SortOrder        int
}

// CheckSample represents one probe result stored in check_samples.
type CheckSample struct {
	MonitorID   string
	State       string
	StatusCode  *int
	LatencyMs   *int64
	TLSDaysLeft *int
	Message     string
	CheckedAt   int64
}

// HeartbeatRow represents a cluster heartbeat row.
type HeartbeatRow struct {
	ID                int64
	Cluster           string
	CPULoad1          float64
	MemUsedPct        float64
	SwapUsedPct       float64
	RootDiskPct       float64
	ExtraDiskPct      float64
	UnhealthyPodCount int
	PayloadJSON       string
	ReceivedAt        int64
}

// Open opens (or creates) the SQLite database in WAL mode and runs migrations.
func Open(dbPath string) (*Store, error) {
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create db dir %q: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	// Serialize SQLite connections to avoid SQLITE_BUSY under concurrent goroutines.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CheckWritable verifies that SQLite is responsive and writable.
func (s *Store) CheckWritable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _healthcheck (id INTEGER PRIMARY KEY, ts INTEGER); INSERT OR REPLACE INTO _healthcheck (id, ts) VALUES (1, ?);`, time.Now().Unix())
	return err
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS monitors (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  group_key TEXT NOT NULL,
  kind TEXT NOT NULL,
  cluster TEXT,
  visibility TEXT NOT NULL,
  severity TEXT NOT NULL,
  current_state TEXT NOT NULL DEFAULT 'unknown',
  consecutive_fails INTEGER NOT NULL DEFAULT 0,
  consecutive_oks INTEGER NOT NULL DEFAULT 0,
  last_latency_ms INTEGER,
  last_message TEXT,
  meta_json TEXT,
  last_checked_at INTEGER,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS check_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  monitor_id TEXT NOT NULL,
  state TEXT NOT NULL,
  status_code INTEGER,
  latency_ms INTEGER,
  tls_days_left INTEGER,
  message TEXT,
  checked_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_samples_mon_time ON check_samples(monitor_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS uptime_buckets (
  monitor_id TEXT NOT NULL,
  bucket_type TEXT NOT NULL,
  bucket_ts INTEGER NOT NULL,
  ok_count INTEGER NOT NULL DEFAULT 0,
  degraded_count INTEGER NOT NULL DEFAULT 0,
  down_count INTEGER NOT NULL DEFAULT 0,
  latency_sum_ms INTEGER NOT NULL DEFAULT 0,
  latency_max_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (monitor_id, bucket_type, bucket_ts)
);

CREATE TABLE IF NOT EXISTS node_heartbeats (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster TEXT NOT NULL,
  cpu_load1 REAL,
  mem_used_pct REAL,
  swap_used_pct REAL,
  root_disk_pct REAL,
  extra_disk_pct REAL,
  unhealthy_pod_count INTEGER NOT NULL DEFAULT 0,
  payload_json TEXT NOT NULL,
  received_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hb_cluster_time ON node_heartbeats(cluster, received_at DESC);

CREATE TABLE IF NOT EXISTS state_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  monitor_id TEXT NOT NULL,
  cluster TEXT,
  severity TEXT NOT NULL,
  prev_state TEXT NOT NULL,
  next_state TEXT NOT NULL,
  suppressed_by TEXT,
  summary TEXT NOT NULL,
  detail_json TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_state_events_created ON state_events(created_at DESC);

CREATE TABLE IF NOT EXISTS incidents (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  impact TEXT NOT NULL,
  affected_monitors TEXT NOT NULL,
  starts_at INTEGER,
  ends_at INTEGER,
  created_by TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS incident_updates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  incident_id TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL,
  created_by TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_incident_updates_inc ON incident_updates(incident_id, created_at ASC);

CREATE TABLE IF NOT EXISTS alert_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id INTEGER NOT NULL,
  channel TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  next_retry_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
`
	_, err := s.db.Exec(schema)
	return err
}

// SyncMonitors upserts declarative monitors from config while preserving runtime state.
func (s *Store) SyncMonitors(ctx context.Context, monitors []config.MonitorConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	keep := make(map[string]struct{}, len(monitors))
	for _, m := range monitors {
		keep[m.ID] = struct{}{}
		_, err := tx.ExecContext(ctx, `
INSERT INTO monitors (id, name, group_key, kind, cluster, visibility, severity, sort_order)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  group_key = excluded.group_key,
  kind = excluded.kind,
  cluster = excluded.cluster,
  visibility = excluded.visibility,
  severity = excluded.severity,
  sort_order = excluded.sort_order
`, m.ID, m.Name, m.GroupKey, m.Kind, nullStr(m.Cluster), m.Visibility, m.Severity, m.SortOrder)
		if err != nil {
			return fmt.Errorf("upsert monitor %s: %w", m.ID, err)
		}
	}

	// Remove monitors that are no longer in config.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM monitors`)
	if err != nil {
		return err
	}
	var toDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[id]; !ok {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()

	for _, id := range toDelete {
		if _, err := tx.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListMonitors returns monitors ordered by sort_order.
func (s *Store) ListMonitors(ctx context.Context, publicOnly bool) ([]MonitorRow, error) {
	q := `SELECT id, name, group_key, kind, COALESCE(cluster,''), visibility, severity,
		current_state, consecutive_fails, consecutive_oks, last_latency_ms,
		COALESCE(last_message,''), COALESCE(meta_json,''), COALESCE(last_checked_at,0), sort_order
		FROM monitors`
	if publicOnly {
		q += ` WHERE visibility = 'public'`
	}
	q += ` ORDER BY sort_order ASC, id ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MonitorRow
	for rows.Next() {
		var r MonitorRow
		var lat sql.NullInt64
		if err := rows.Scan(
			&r.ID, &r.Name, &r.GroupKey, &r.Kind, &r.Cluster, &r.Visibility, &r.Severity,
			&r.CurrentState, &r.ConsecutiveFails, &r.ConsecutiveOKs, &lat,
			&r.LastMessage, &r.MetaJSON, &r.LastCheckedAt, &r.SortOrder,
		); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Int64
			r.LastLatencyMs = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetMonitor fetches a single monitor by ID.
func (s *Store) GetMonitor(ctx context.Context, id string) (*MonitorRow, error) {
	var r MonitorRow
	var lat sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, group_key, kind, COALESCE(cluster,''), visibility, severity,
  current_state, consecutive_fails, consecutive_oks, last_latency_ms,
  COALESCE(last_message,''), COALESCE(meta_json,''), COALESCE(last_checked_at,0), sort_order
FROM monitors WHERE id = ?`, id).Scan(
		&r.ID, &r.Name, &r.GroupKey, &r.Kind, &r.Cluster, &r.Visibility, &r.Severity,
		&r.CurrentState, &r.ConsecutiveFails, &r.ConsecutiveOKs, &lat,
		&r.LastMessage, &r.MetaJSON, &r.LastCheckedAt, &r.SortOrder,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if lat.Valid {
		v := lat.Int64
		r.LastLatencyMs = &v
	}
	return &r, nil
}

// UpdateMonitorState updates a monitor's current runtime status.
func (s *Store) UpdateMonitorState(ctx context.Context, id, state string, fails, oks int, latencyMs *int64, message, metaJSON string, checkedAt int64) error {
	var lat any
	if latencyMs != nil {
		lat = *latencyMs
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE monitors
SET current_state = ?,
    consecutive_fails = ?,
    consecutive_oks = ?,
    last_latency_ms = ?,
    last_message = ?,
    meta_json = CASE WHEN ? != '' THEN ? ELSE meta_json END,
    last_checked_at = ?
WHERE id = ?`,
		state, fails, oks, lat, message, metaJSON, metaJSON, checkedAt, id,
	)
	return err
}

// RecordCheckSample inserts a minute-level sample and rolls it up into hourly and daily uptime_buckets.
func (s *Store) RecordCheckSample(ctx context.Context, sample CheckSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sc, lat, tlsDays any
	if sample.StatusCode != nil {
		sc = *sample.StatusCode
	}
	var latVal int64
	if sample.LatencyMs != nil {
		lat = *sample.LatencyMs
		latVal = *sample.LatencyMs
	}
	if sample.TLSDaysLeft != nil {
		tlsDays = *sample.TLSDaysLeft
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO check_samples (monitor_id, state, status_code, latency_ms, tls_days_left, message, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sample.MonitorID, sample.State, sc, lat, tlsDays, sample.Message, sample.CheckedAt,
	); err != nil {
		return err
	}

	okInc, degInc, downInc := 0, 0, 0
	switch sample.State {
	case protocol.StateUp:
		okInc = 1
	case protocol.StateDegraded:
		degInc = 1
	case protocol.StateDown:
		downInc = 1
	}

	t := time.Unix(sample.CheckedAt, 0).UTC()
	hourTS := t.Truncate(time.Hour).Unix()
	dayTS := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix()

	for _, b := range []struct {
		typ string
		ts  int64
	}{
		{"hour", hourTS},
		{"day", dayTS},
	} {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO uptime_buckets (monitor_id, bucket_type, bucket_ts, ok_count, degraded_count, down_count, latency_sum_ms, latency_max_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(monitor_id, bucket_type, bucket_ts) DO UPDATE SET
  ok_count = ok_count + excluded.ok_count,
  degraded_count = degraded_count + excluded.degraded_count,
  down_count = down_count + excluded.down_count,
  latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms,
  latency_max_ms = MAX(latency_max_ms, excluded.latency_max_ms)
`, sample.MonitorID, b.typ, b.ts, okInc, degInc, downInc, latVal, latVal); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetDailyBarsBatch fetches N days of daily uptime buckets for the given monitor IDs.
func (s *Store) GetDailyBarsBatch(ctx context.Context, monitorIDs []string, days int, now time.Time) (map[string][]protocol.DayUptime, map[string]float64, error) {
	barsMap := make(map[string][]protocol.DayUptime, len(monitorIDs))
	uptimeMap := make(map[string]float64, len(monitorIDs))
	if len(monitorIDs) == 0 || days <= 0 {
		return barsMap, uptimeMap, nil
	}

	todayUTC := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	startDay := todayUTC.AddDate(0, 0, -(days - 1))

	type bucket struct {
		ok, deg, down int
		latSum        int64
	}
	byMon := make(map[string]map[int64]bucket, len(monitorIDs))

	placeholders := make([]string, len(monitorIDs))
	args := make([]any, 0, len(monitorIDs)+1)
	args = append(args, startDay.Unix())
	for i, id := range monitorIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	q := fmt.Sprintf(`
SELECT monitor_id, bucket_ts, ok_count, degraded_count, down_count, latency_sum_ms
FROM uptime_buckets
WHERE bucket_type = 'day' AND bucket_ts >= ? AND monitor_id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var monID string
		var ts int64
		var b bucket
		if err := rows.Scan(&monID, &ts, &b.ok, &b.deg, &b.down, &b.latSum); err != nil {
			return nil, nil, err
		}
		if byMon[monID] == nil {
			byMon[monID] = make(map[int64]bucket)
		}
		byMon[monID][ts] = b
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	for _, id := range monitorIDs {
		monBuckets := byMon[id]
		bars := make([]protocol.DayUptime, 0, days)
		var totalOK, totalDeg, totalAll int
		for d := 0; d < days; d++ {
			dayTime := startDay.AddDate(0, 0, d)
			ts := dayTime.Unix()
			du := protocol.DayUptime{
				Date:      dayTime.Format("2006-01-02"),
				Timestamp: ts,
				State:     "no_data",
				UptimePct: 100,
			}
			if b, ok := monBuckets[ts]; ok {
				total := b.ok + b.deg + b.down
				if total > 0 {
					du.DownCount = b.down
					du.DegradedCount = b.deg
					du.AvgLatencyMs = b.latSum / int64(total)
					du.UptimePct = round2(float64(b.ok+b.deg) / float64(total) * 100.0)
					if b.down > 0 {
						if float64(b.down)/float64(total) >= 0.05 || b.down >= 5 {
							du.State = protocol.StateDown
						} else {
							du.State = protocol.StateDegraded
						}
					} else if b.deg > 0 {
						du.State = protocol.StateDegraded
					} else {
						du.State = protocol.StateUp
					}
					totalOK += b.ok
					totalDeg += b.deg
					totalAll += total
				}
			}
			bars = append(bars, du)
		}
		barsMap[id] = bars
		if totalAll > 0 {
			uptimeMap[id] = round2(float64(totalOK+totalDeg) / float64(totalAll) * 100.0)
		} else {
			uptimeMap[id] = 100.0
		}
	}

	return barsMap, uptimeMap, nil
}

// GetRangeBarsBatch returns uptime bars and aggregate uptime percentage for a supported range:
//   - "24h": 24 hourly bars (1h per bar)
//   - "7d":  28 6-hour bars (6h per bar)
//   - "30d": 30 daily bars (1d per bar)
//   - "90d": 90 daily bars (1d per bar, default)
func (s *Store) GetRangeBarsBatch(ctx context.Context, monitorIDs []string, rangeKey string, now time.Time) (map[string][]protocol.DayUptime, map[string]float64, error) {
	switch rangeKey {
	case "24h":
		return s.getHourlyWindowBarsBatch(ctx, monitorIDs, 24, 1, now)
	case "7d":
		return s.getHourlyWindowBarsBatch(ctx, monitorIDs, 28, 6, now)
	case "30d":
		return s.GetDailyBarsBatch(ctx, monitorIDs, 30, now)
	default:
		return s.GetDailyBarsBatch(ctx, monitorIDs, 90, now)
	}
}

func (s *Store) getHourlyWindowBarsBatch(ctx context.Context, monitorIDs []string, slotCount, hoursPerSlot int, now time.Time) (map[string][]protocol.DayUptime, map[string]float64, error) {
	barsMap := make(map[string][]protocol.DayUptime, len(monitorIDs))
	uptimeMap := make(map[string]float64, len(monitorIDs))
	if len(monitorIDs) == 0 || slotCount <= 0 || hoursPerSlot <= 0 {
		return barsMap, uptimeMap, nil
	}

	nowUTC := now.UTC()
	curHour := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), nowUTC.Hour(), 0, 0, 0, time.UTC)
	slotDur := time.Duration(hoursPerSlot) * time.Hour
	// Align the last slot so it contains curHour.
	var lastSlotStart time.Time
	if hoursPerSlot == 1 {
		lastSlotStart = curHour
	} else {
		alignedHour := (curHour.Hour() / hoursPerSlot) * hoursPerSlot
		lastSlotStart = time.Date(curHour.Year(), curHour.Month(), curHour.Day(), alignedHour, 0, 0, 0, time.UTC)
	}
	firstSlotStart := lastSlotStart.Add(-time.Duration(slotCount-1) * slotDur)

	type bucket struct {
		ok, deg, down int
		latSum        int64
	}
	byMon := make(map[string]map[int64]bucket, len(monitorIDs))

	placeholders := make([]string, len(monitorIDs))
	args := make([]any, 0, len(monitorIDs)+1)
	args = append(args, firstSlotStart.Unix())
	for i, id := range monitorIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	q := fmt.Sprintf(`
SELECT monitor_id, bucket_ts, ok_count, degraded_count, down_count, latency_sum_ms
FROM uptime_buckets
WHERE bucket_type = 'hour' AND bucket_ts >= ? AND monitor_id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	firstUnix := firstSlotStart.Unix()
	slotSecs := int64(hoursPerSlot * 3600)

	for rows.Next() {
		var monID string
		var ts int64
		var b bucket
		if err := rows.Scan(&monID, &ts, &b.ok, &b.deg, &b.down, &b.latSum); err != nil {
			return nil, nil, err
		}
		if ts < firstUnix {
			continue
		}
		slotIdx := (ts - firstUnix) / slotSecs
		if slotIdx < 0 || slotIdx >= int64(slotCount) {
			continue
		}
		slotTs := firstUnix + slotIdx*slotSecs
		if byMon[monID] == nil {
			byMon[monID] = make(map[int64]bucket)
		}
		acc := byMon[monID][slotTs]
		acc.ok += b.ok
		acc.deg += b.deg
		acc.down += b.down
		acc.latSum += b.latSum
		byMon[monID][slotTs] = acc
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	cst := time.FixedZone("CST", 8*3600)

	for _, id := range monitorIDs {
		monBuckets := byMon[id]
		bars := make([]protocol.DayUptime, 0, slotCount)
		var totalOK, totalDeg, totalAll int
		for i := 0; i < slotCount; i++ {
			slotTime := firstSlotStart.Add(time.Duration(i) * slotDur)
			ts := slotTime.Unix()
			localStart := slotTime.In(cst)
			var label string
			if hoursPerSlot == 1 {
				label = localStart.Format("01-02 15:00")
			} else {
				localEnd := slotTime.Add(slotDur).In(cst)
				label = fmt.Sprintf("%s ~ %s", localStart.Format("01-02 15:00"), localEnd.Format("15:00"))
			}
			du := protocol.DayUptime{
				Date:      label,
				Timestamp: ts,
				State:     "no_data",
				UptimePct: 100,
			}
			if b, ok := monBuckets[ts]; ok {
				total := b.ok + b.deg + b.down
				if total > 0 {
					du.DownCount = b.down
					du.DegradedCount = b.deg
					du.AvgLatencyMs = b.latSum / int64(total)
					du.UptimePct = round2(float64(b.ok+b.deg) / float64(total) * 100.0)
					if b.down > 0 {
						if float64(b.down)/float64(total) >= 0.05 || b.down >= 5 {
							du.State = protocol.StateDown
						} else {
							du.State = protocol.StateDegraded
						}
					} else if b.deg > 0 {
						du.State = protocol.StateDegraded
					} else {
						du.State = protocol.StateUp
					}
					totalOK += b.ok
					totalDeg += b.deg
					totalAll += total
				}
			}
			bars = append(bars, du)
		}
		barsMap[id] = bars
		if totalAll > 0 {
			uptimeMap[id] = round2(float64(totalOK+totalDeg) / float64(totalAll) * 100.0)
		} else {
			uptimeMap[id] = 100.0
		}
	}

	return barsMap, uptimeMap, nil
}

// GetLatency24h returns minute samples from the past 24 hours for a monitor.
func (s *Store) GetLatency24h(ctx context.Context, monitorID string, now time.Time) ([]protocol.LatencyPoint, error) {
	since := now.Add(-24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `
SELECT checked_at, COALESCE(latency_ms, 0), state
FROM check_samples
WHERE monitor_id = ? AND checked_at >= ?
ORDER BY checked_at ASC`, monitorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pts := make([]protocol.LatencyPoint, 0)
	for rows.Next() {
		var p protocol.LatencyPoint
		if err := rows.Scan(&p.Timestamp, &p.LatencyMs, &p.State); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
}

// InsertHeartbeat stores a cluster heartbeat snapshot.
func (s *Store) InsertHeartbeat(ctx context.Context, payload protocol.HeartbeatPayload, receivedAt int64) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO node_heartbeats (
  cluster, cpu_load1, mem_used_pct, swap_used_pct, root_disk_pct, extra_disk_pct,
  unhealthy_pod_count, payload_json, received_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		payload.Cluster,
		payload.HostMetrics.CPULoad1,
		payload.HostMetrics.MemUsedPct,
		payload.HostMetrics.SwapUsedPct,
		payload.HostMetrics.RootDiskPct,
		payload.HostMetrics.ExtraDiskPct,
		len(payload.UnhealthyPods),
		string(raw),
		receivedAt,
	)
	return err
}

// GetLatestHeartbeats returns the most recent heartbeat per cluster.
func (s *Store) GetLatestHeartbeats(ctx context.Context) (map[string]HeartbeatRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT h.id, h.cluster, COALESCE(h.cpu_load1,0), COALESCE(h.mem_used_pct,0),
  COALESCE(h.swap_used_pct,0), COALESCE(h.root_disk_pct,0), COALESCE(h.extra_disk_pct,0),
  h.unhealthy_pod_count, h.payload_json, h.received_at
FROM node_heartbeats h
INNER JOIN (
  SELECT cluster, MAX(id) AS max_id
  FROM node_heartbeats
  GROUP BY cluster
) latest ON h.id = latest.max_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]HeartbeatRow)
	for rows.Next() {
		var r HeartbeatRow
		if err := rows.Scan(
			&r.ID, &r.Cluster, &r.CPULoad1, &r.MemUsedPct,
			&r.SwapUsedPct, &r.RootDiskPct, &r.ExtraDiskPct,
			&r.UnhealthyPodCount, &r.PayloadJSON, &r.ReceivedAt,
		); err != nil {
			return nil, err
		}
		out[r.Cluster] = r
	}
	return out, rows.Err()
}

// InsertStateEvent records a state transition and returns its auto-increment ID.
func (s *Store) InsertStateEvent(ctx context.Context, ev protocol.AlertEvent) (int64, error) {
	var detail any
	if len(ev.Detail) > 0 {
		detail = string(ev.Detail)
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO state_events (monitor_id, cluster, severity, prev_state, next_state, suppressed_by, summary, detail_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.MonitorID, nullStr(ev.Cluster), ev.Severity, ev.PrevState, ev.NextState,
		nullStr(ev.SuppressedBy), ev.Summary, detail, ev.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListStateEventsAfter returns unsuppressed state_events with id > afterID, ordered ascending.
func (s *Store) ListStateEventsAfter(ctx context.Context, afterID int64, includeSuppressed bool, limit int) ([]protocol.AlertEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `
SELECT e.id, e.monitor_id, COALESCE(m.name, e.monitor_id), COALESCE(e.cluster,''),
  e.severity, e.prev_state, e.next_state, COALESCE(e.suppressed_by,''),
  e.summary, COALESCE(e.detail_json,''), e.created_at
FROM state_events e
LEFT JOIN monitors m ON m.id = e.monitor_id
WHERE e.id > ?`
	if !includeSuppressed {
		q += ` AND (e.suppressed_by IS NULL OR e.suppressed_by = '')`
	}
	q += ` ORDER BY e.id ASC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]protocol.AlertEvent, 0)
	for rows.Next() {
		var ev protocol.AlertEvent
		var detailStr string
		if err := rows.Scan(
			&ev.ID, &ev.MonitorID, &ev.MonitorName, &ev.Cluster,
			&ev.Severity, &ev.PrevState, &ev.NextState, &ev.SuppressedBy,
			&ev.Summary, &detailStr, &ev.CreatedAt,
		); err != nil {
			return nil, err
		}
		if detailStr != "" {
			ev.Detail = json.RawMessage(detailStr)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ListRecentStateEvents returns the most recent N state_events ordered descending.
func (s *Store) ListRecentStateEvents(ctx context.Context, limit int) ([]protocol.AlertEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id, e.monitor_id, COALESCE(m.name, e.monitor_id), COALESCE(e.cluster,''),
  e.severity, e.prev_state, e.next_state, COALESCE(e.suppressed_by,''),
  e.summary, COALESCE(e.detail_json,''), e.created_at
FROM state_events e
LEFT JOIN monitors m ON m.id = e.monitor_id
ORDER BY e.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]protocol.AlertEvent, 0)
	for rows.Next() {
		var ev protocol.AlertEvent
		var detailStr string
		if err := rows.Scan(
			&ev.ID, &ev.MonitorID, &ev.MonitorName, &ev.Cluster,
			&ev.Severity, &ev.PrevState, &ev.NextState, &ev.SuppressedBy,
			&ev.Summary, &detailStr, &ev.CreatedAt,
		); err != nil {
			return nil, err
		}
		if detailStr != "" {
			ev.Detail = json.RawMessage(detailStr)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CreateIncident inserts a new incident or maintenance window and its initial update.
func (s *Store) CreateIncident(ctx context.Context, req protocol.CreateIncidentRequest, createdBy string, now int64) (*protocol.Incident, error) {
	incType := req.Type
	if incType == "" {
		incType = protocol.IncidentTypeIncident
	}
	status := req.Status
	if status == "" {
		if incType == protocol.IncidentTypeMaintenance {
			status = protocol.IncidentStatusScheduled
		} else {
			status = protocol.IncidentStatusInvestigating
		}
	}
	impact := req.Impact
	if impact == "" {
		if incType == protocol.IncidentTypeMaintenance {
			impact = protocol.ImpactMaintenance
		} else {
			impact = protocol.ImpactMinor
		}
	}
	affected := req.AffectedMonitors
	if affected == nil {
		affected = []string{}
	}
	affJSON, err := json.Marshal(affected)
	if err != nil {
		return nil, err
	}

	id := "inc_" + strings.ReplaceAll(uuid.New().String()[:12], "-", "")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
INSERT INTO incidents (id, type, title, status, impact, affected_monitors, starts_at, ends_at, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, incType, req.Title, status, impact, string(affJSON), req.StartsAt, req.EndsAt, nullStr(createdBy), now, now,
	)
	if err != nil {
		return nil, err
	}

	var updates []protocol.IncidentUpdate
	if strings.TrimSpace(req.Message) != "" {
		res, err := tx.ExecContext(ctx, `
INSERT INTO incident_updates (incident_id, status, message, created_by, created_at)
VALUES (?, ?, ?, ?, ?)`, id, status, req.Message, nullStr(createdBy), now)
		if err != nil {
			return nil, err
		}
		updID, _ := res.LastInsertId()
		updates = append(updates, protocol.IncidentUpdate{
			ID:         updID,
			IncidentID: id,
			Status:     status,
			Message:    req.Message,
			CreatedBy:  createdBy,
			CreatedAt:  now,
		})
	} else {
		updates = []protocol.IncidentUpdate{}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &protocol.Incident{
		ID:               id,
		Type:             incType,
		Title:            req.Title,
		Status:           status,
		Impact:           impact,
		AffectedMonitors: affected,
		StartsAt:         req.StartsAt,
		EndsAt:           req.EndsAt,
		CreatedBy:        createdBy,
		CreatedAt:        now,
		UpdatedAt:        now,
		Updates:          updates,
	}, nil
}

// UpdateIncident patches an existing incident and optionally appends an update entry.
func (s *Store) UpdateIncident(ctx context.Context, id string, req protocol.UpdateIncidentRequest, updatedBy string, now int64) (*protocol.Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var cur protocol.Incident
	var affJSON string
	var startsAt, endsAt sql.NullInt64
	var createdBy sql.NullString
	err = tx.QueryRowContext(ctx, `
SELECT id, type, title, status, impact, affected_monitors, starts_at, ends_at, created_by, created_at, updated_at
FROM incidents WHERE id = ?`, id).Scan(
		&cur.ID, &cur.Type, &cur.Title, &cur.Status, &cur.Impact, &affJSON,
		&startsAt, &endsAt, &createdBy, &cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(affJSON), &cur.AffectedMonitors)
	if startsAt.Valid {
		v := startsAt.Int64
		cur.StartsAt = &v
	}
	if endsAt.Valid {
		v := endsAt.Int64
		cur.EndsAt = &v
	}
	if createdBy.Valid {
		cur.CreatedBy = createdBy.String
	}

	if req.Title != nil {
		cur.Title = *req.Title
	}
	if req.Status != nil {
		cur.Status = *req.Status
	}
	if req.Impact != nil {
		cur.Impact = *req.Impact
	}
	if req.AffectedMonitors != nil {
		cur.AffectedMonitors = req.AffectedMonitors
	}
	if req.StartsAt != nil {
		cur.StartsAt = req.StartsAt
	}
	if req.EndsAt != nil {
		cur.EndsAt = req.EndsAt
	}
	cur.UpdatedAt = now

	newAff, err := json.Marshal(cur.AffectedMonitors)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE incidents
SET title = ?, status = ?, impact = ?, affected_monitors = ?, starts_at = ?, ends_at = ?, updated_at = ?
WHERE id = ?`,
		cur.Title, cur.Status, cur.Impact, string(newAff), cur.StartsAt, cur.EndsAt, cur.UpdatedAt, id,
	); err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.Message) != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO incident_updates (incident_id, status, message, created_by, created_at)
VALUES (?, ?, ?, ?, ?)`, id, cur.Status, req.Message, nullStr(updatedBy), now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	updates, err := s.listIncidentUpdates(ctx, id, false)
	if err != nil {
		return nil, err
	}
	cur.Updates = updates
	return &cur, nil
}

// ListIncidents returns incidents with their updates.
func (s *Store) ListIncidents(ctx context.Context, activeOnly bool, redactAuthor bool) ([]protocol.Incident, error) {
	q := `
SELECT id, type, title, status, impact, affected_monitors, starts_at, ends_at, COALESCE(created_by,''), created_at, updated_at
FROM incidents`
	if activeOnly {
		q += ` WHERE status != 'resolved'`
	}
	q += ` ORDER BY updated_at DESC LIMIT 100`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]protocol.Incident, 0)
	for rows.Next() {
		var inc protocol.Incident
		var affJSON string
		var startsAt, endsAt sql.NullInt64
		if err := rows.Scan(
			&inc.ID, &inc.Type, &inc.Title, &inc.Status, &inc.Impact, &affJSON,
			&startsAt, &endsAt, &inc.CreatedBy, &inc.CreatedAt, &inc.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(affJSON), &inc.AffectedMonitors)
		if inc.AffectedMonitors == nil {
			inc.AffectedMonitors = []string{}
		}
		if startsAt.Valid {
			v := startsAt.Int64
			inc.StartsAt = &v
		}
		if endsAt.Valid {
			v := endsAt.Int64
			inc.EndsAt = &v
		}
		if redactAuthor {
			inc.CreatedBy = ""
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		updates, err := s.listIncidentUpdates(ctx, out[i].ID, redactAuthor)
		if err != nil {
			return nil, err
		}
		out[i].Updates = updates
	}
	return out, nil
}

func (s *Store) listIncidentUpdates(ctx context.Context, incidentID string, redactAuthor bool) ([]protocol.IncidentUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, incident_id, status, message, COALESCE(created_by,''), created_at
FROM incident_updates
WHERE incident_id = ?
ORDER BY created_at ASC, id ASC`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]protocol.IncidentUpdate, 0)
	for rows.Next() {
		var u protocol.IncidentUpdate
		if err := rows.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Message, &u.CreatedBy, &u.CreatedAt); err != nil {
			return nil, err
		}
		if redactAuthor {
			u.CreatedBy = ""
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ActiveMaintenanceForMonitor checks if any active maintenance window covers monitorID at unix timestamp now.
func (s *Store) ActiveMaintenanceForMonitor(ctx context.Context, monitorID string, now int64) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, affected_monitors, starts_at, ends_at, status
FROM incidents
WHERE type = 'maintenance' AND status != 'resolved'`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, affJSON, status string
		var startsAt, endsAt sql.NullInt64
		if err := rows.Scan(&id, &affJSON, &startsAt, &endsAt, &status); err != nil {
			return "", false, err
		}
		inWindow := status == protocol.IncidentStatusInProgress
		if startsAt.Valid && endsAt.Valid {
			if now >= startsAt.Int64 && now <= endsAt.Int64 {
				inWindow = true
			}
		} else if startsAt.Valid && now >= startsAt.Int64 {
			inWindow = true
		}
		if !inWindow {
			continue
		}
		var affected []string
		_ = json.Unmarshal([]byte(affJSON), &affected)
		if len(affected) == 0 {
			return id, true, nil
		}
		for _, m := range affected {
			if m == monitorID {
				return id, true, nil
			}
		}
	}
	return "", false, rows.Err()
}

// InsertAlertDelivery records an outbound email or webhook delivery attempt.
func (s *Store) InsertAlertDelivery(ctx context.Context, eventID int64, channel, status, lastError string, attempts int, nextRetryAt, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO alert_deliveries (event_id, channel, status, attempts, last_error, next_retry_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		eventID, channel, status, attempts, nullStr(lastError), nextRetryAt, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LastProbeTime returns the most recent checked_at timestamp across all check_samples.
func (s *Store) LastProbeTime(ctx context.Context) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(checked_at) FROM check_samples`).Scan(&ts)
	if err != nil || !ts.Valid {
		return 0, err
	}
	return ts.Int64, nil
}

// Prune enforces data retention policies:
// - check_samples: 30 days
// - uptime_buckets: 365 days
// - node_heartbeats: 7 days
// - state_events: 90 days
// - alert_deliveries: 30 days
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	queries := []struct {
		sql    string
		cutoff int64
	}{
		{`DELETE FROM check_samples WHERE checked_at < ?`, now.AddDate(0, 0, -30).Unix()},
		{`DELETE FROM uptime_buckets WHERE bucket_ts < ?`, now.AddDate(0, 0, -365).Unix()},
		{`DELETE FROM node_heartbeats WHERE received_at < ?`, now.AddDate(0, 0, -7).Unix()},
		{`DELETE FROM state_events WHERE created_at < ?`, now.AddDate(0, 0, -90).Unix()},
		{`DELETE FROM alert_deliveries WHERE created_at < ?`, now.AddDate(0, 0, -30).Unix()},
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q.sql, q.cutoff); err != nil {
			return err
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
