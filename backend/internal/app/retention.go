package app

import (
	"context"
	"time"
)

// retentionSweepInterval controls how often expired events and metric
// aggregates are pruned. Hourly rather than continuous keeps the
// background cost negligible while still bounding database growth (see
// spec section 16/17 and Config.EventsRetentionDays/
// MetricsRetentionDays, storage.EventRepository.PruneOlderThan, and
// storage.MetricsRepository.PruneOlderThan).
const retentionSweepInterval = time.Hour

func (a *App) runRetentionPruner(ctx context.Context) {
	a.pruneOnce(ctx) // sweep once at startup so a long-idle install isn't stuck waiting a full interval

	ticker := time.NewTicker(retentionSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pruneOnce(ctx)
		}
	}
}

func (a *App) pruneOnce(ctx context.Context) {
	pruneCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if a.cfg.EventsRetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(a.cfg.EventsRetentionDays) * 24 * time.Hour)
		n, err := a.events.PruneOlderThan(pruneCtx, cutoff)
		if err != nil {
			a.logger.Warn("event retention sweep failed", "error", err)
		} else if n > 0 {
			a.logger.Info("pruned expired events", "count", n)
		}
	}

	if a.cfg.MetricsRetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(a.cfg.MetricsRetentionDays) * 24 * time.Hour)
		n, err := a.metricsRepo.PruneOlderThan(pruneCtx, cutoff)
		if err != nil {
			a.logger.Warn("metrics retention sweep failed", "error", err)
		} else if n > 0 {
			a.logger.Info("pruned expired metric aggregates", "count", n)
		}
	}
}
