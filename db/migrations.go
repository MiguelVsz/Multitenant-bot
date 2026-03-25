package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaMigrations = []string{
	`
CREATE TABLE IF NOT EXISTS tenants (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	phone_number_id TEXT NOT NULL UNIQUE,
	pos_provider TEXT NOT NULL DEFAULT 'generic',
	pos_config JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`,
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range schemaMigrations {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
