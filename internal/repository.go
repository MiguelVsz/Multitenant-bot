package internal

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"multi-tenant-bot/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrTenantNotFound = errors.New("tenant not found")

type Tenant struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	PhoneNumberID string          `json:"phone_number_id"`
	POSProvider   string          `json:"pos_provider"`
	POSConfig     json.RawMessage `json:"pos_config"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.Ping(ctx)
}

func (r *Repository) ResolveTenantByPhoneNumberID(ctx context.Context, phoneNumberID string) (*Tenant, error) {
	row := r.db.QueryRow(ctx, db.QueryResolveTenantByPhoneNumberID, phoneNumberID)

	var tenant Tenant
	if err := row.Scan(
		&tenant.ID,
		&tenant.Name,
		&tenant.PhoneNumberID,
		&tenant.POSProvider,
		&tenant.POSConfig,
		&tenant.CreatedAt,
		&tenant.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}

	return &tenant, nil
}
