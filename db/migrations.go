package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaMigrations = []string{}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range schemaMigrations {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
