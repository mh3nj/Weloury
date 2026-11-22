package app

import (
	"context"
	"encoding/json"
	"time"

	"weloury/internal/ipc"
	"weloury/internal/metrics"
	"weloury/pkg/fwp"
)

// registerMetricsMethods wires the metrics engine (internal/metrics,
// spec section 17) onto r.
func registerMetricsMethods(r *ipc.Router, engine *metrics.Engine) {
	r.Handle("metrics.current", metricsCurrentHandler(engine))
	r.Handle("metrics.rolling", metricsRollingHandler(engine))
	r.Handle("metrics.history", metricsHistoryHandler(engine))
}

func metricsCurrentHandler(engine *metrics.Engine) ipc.HandlerFunc {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return engine.Current(), nil
	}
}

type metricsSeriesParams struct {
	Metric string `json:"metric"`
	Scope  string `json:"scope,omitempty"`
}

func metricsRollingHandler(engine *metrics.Engine) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p metricsSeriesParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.Metric == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "metric is required")
		}
		return engine.Rolling(metrics.Name(p.Metric), p.Scope), nil
	}
}

type metricsHistoryParams struct {
	Metric string `json:"metric"`
	Scope  string `json:"scope,omitempty"`
	// SinceSeconds, if > 0, requests aggregates from that many seconds
	// ago onward. Defaults to 24h - long enough to be useful for a
	// history chart, short enough that a client that forgets to pass it
	// still gets a bounded response rather than a whole retention
	// window's worth of rows.
	SinceSeconds int `json:"since_seconds,omitempty"`
	// Limit caps the number of points returned; defaults to 500 and is
	// clamped to the same value as a ceiling regardless of what's
	// requested - the same "bounded regardless of caller input"
	// discipline internal/logs.Query applies (see its doc comment).
	Limit int `json:"limit,omitempty"`
}

const (
	defaultHistorySinceSeconds = 24 * 60 * 60
	defaultHistoryLimit        = 500
	maxHistoryLimit            = 500
)

func metricsHistoryHandler(engine *metrics.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p metricsHistoryParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.Metric == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "metric is required")
		}
		sinceSeconds := p.SinceSeconds
		if sinceSeconds <= 0 {
			sinceSeconds = defaultHistorySinceSeconds
		}
		limit := p.Limit
		if limit <= 0 {
			limit = defaultHistoryLimit
		}
		if limit > maxHistoryLimit {
			limit = maxHistoryLimit
		}

		since := time.Now().Add(-time.Duration(sinceSeconds) * time.Second)
		points, err := engine.Query(ctx, metrics.Name(p.Metric), p.Scope, since, limit)
		if err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		return points, nil
	}
}
