package store

import "context"

// Diagnostics returns aggregate queue and pool statistics, never delivery rows.
func (s *Store) Diagnostics(ctx context.Context) (map[string]float64, error) {
	stats := s.db.Stats()
	readers := s.db.ReadStats()
	values := map[string]float64{
		"db_open":         float64(stats.OpenConnections + readers.OpenConnections),
		"db_in_use":       float64(stats.InUse + readers.InUse),
		"db_idle":         float64(stats.Idle + readers.Idle),
		"db_wait_count":   float64(stats.WaitCount + readers.WaitCount),
		"db_wait_seconds": (stats.WaitDuration + readers.WaitDuration).Seconds(),
	}
	rows, err := s.db.ReadExecutor().QueryContext(ctx, `
		SELECT 'sale', status, COUNT(*), (julianday('now') - julianday(MIN(created_at))) * 86400.0
		FROM deliveries WHERE status IN ('pending', 'retry', 'sending', 'dead') GROUP BY status
		UNION ALL
		SELECT 'free', status, COUNT(*), (julianday('now') - julianday(MIN(created_at))) * 86400.0
		FROM free_deliveries WHERE status IN ('pending', 'retry', 'sending', 'dead') GROUP BY status`)
	if err != nil {
		return values, err
	}
	defer rows.Close()
	queue := map[string]float64{
		"sale_pending":        0,
		"sale_retry":          0,
		"sale_sending":        0,
		"sale_dead":           0,
		"sale_oldest_seconds": 0,
		"free_pending":        0,
		"free_retry":          0,
		"free_sending":        0,
		"free_dead":           0,
		"free_oldest_seconds": 0,
	}
	for rows.Next() {
		var kind, status string
		var count, age float64
		if err := rows.Scan(&kind, &status, &count, &age); err != nil {
			return values, err
		}
		queue[kind+"_"+status] = count
		if status != "dead" {
			key := kind + "_oldest_seconds"
			queue[key] = max(queue[key], age)
		}
	}
	if err := rows.Err(); err != nil {
		return values, err
	}
	for key, value := range queue {
		values[key] = value
	}

	return values, nil
}
