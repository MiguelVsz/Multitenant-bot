package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

var schemaMigrations = []string{
	// Aquí pueden ir las tablas que necesiten
	`CREATE TABLE IF NOT EXISTS gobot.users (
        id SERIAL PRIMARY KEY,
        phone TEXT UNIQUE,
        rid TEXT,
        name TEXT,
        tenant_id TEXT
    );`,
}

func EnsureSchema(ctx context.Context, p *pgxpool.Pool) error {
	Pool = p

	for _, stmt := range schemaMigrations {
		if _, err := Pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
