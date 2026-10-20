// Package store extends the durable repositories with M15 cost aggregation
// over the model_usage table recorded by the model gateway.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UsageRow is the measured usage for one model, separated into cached and
// uncached input tokens as the provider reports them.
type UsageRow struct {
	Model               string  `json:"model"`
	InputTokensCached   int64   `json:"input_tokens_cached"`
	InputTokensUncached int64   `json:"input_tokens_uncached"`
	OutputTokens        int64   `json:"output_tokens"`
	UsdCost             float64 `json:"usd_cost"`
}

// CostAggregate is the measured cost and token usage for one organization,
// computed from the real model_usage rows the gateway records.
type CostAggregate struct {
	OrganizationID string     `json:"organization_id"`
	TotalUSD       float64    `json:"total_usd"`
	TotalTokens    int64      `json:"total_tokens"`
	ByModel        []UsageRow `json:"by_model"`
	GeneratedAt    time.Time  `json:"generated_at"`
}

// Usage aggregates measured model usage and USD cost from the model_usage
// table. Every number is derived from rows the gateway recorded; nothing is
// estimated or invented.
type Usage struct {
	pool *pgxpool.Pool
}

// NewUsage binds the repository to the connection pool.
func NewUsage(pool *pgxpool.Pool) *Usage { return &Usage{pool: pool} }

// usageColumns is the projection shared by every cost read.
const usageColumns = `model, prompt_tokens, cached_tokens, output_tokens, usd_cost, created_at`

// scanUsageRow reads one model_usage row into a UsageRow.
func scanUsageRow(row pgx.CollectableRow) (UsageRow, error) {
	var raw modelUsageRow
	if err := row.Scan(&raw.Model, &raw.PromptTokens, &raw.CachedTokens, &raw.OutputTokens, &raw.UsdCost, &raw.CreatedAt); err != nil {
		return UsageRow{}, err
	}
	return UsageRow{
		Model:               raw.Model,
		InputTokensCached:   raw.CachedTokens,
		InputTokensUncached: raw.PromptTokens - raw.CachedTokens,
		OutputTokens:        raw.OutputTokens,
		UsdCost:             raw.UsdCost,
	}, nil
}

type modelUsageRow struct {
	Model        string
	PromptTokens int64
	CachedTokens int64
	OutputTokens int64
	UsdCost      float64
	CreatedAt    time.Time
}

// Aggregate computes the measured cost and token usage for one organization,
// grouped by model. It is tenant-scoped: callers only see their own usage.
func (u *Usage) Aggregate(ctx context.Context, organizationID string) (CostAggregate, error) {
	if !nonEmpty(organizationID) {
		return CostAggregate{}, validationErrorf("organization_id", "id is required")
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := u.pool.Query(ctx, `
		SELECT model, prompt_tokens, cached_tokens, output_tokens, usd_cost, created_at
		  FROM model_usage
		 WHERE organization_id = $1
		 ORDER BY created_at DESC`,
		organizationID)
	if err != nil {
		return CostAggregate{}, fmt.Errorf("read model usage for org %s: %w", organizationID, err)
	}

	items, err := pgx.CollectRows(rows, scanUsageRow)
	if err != nil {
		return CostAggregate{}, fmt.Errorf("scan model usage for org %s: %w", organizationID, err)
	}

	aggregate := CostAggregate{
		OrganizationID: organizationID,
		ByModel:        items,
		GeneratedAt:    time.Now().UTC(),
	}
	for _, item := range items {
		aggregate.TotalUSD += item.UsdCost
		aggregate.TotalTokens += item.InputTokensCached + item.InputTokensUncached + item.OutputTokens
	}
	return aggregate, nil
}
