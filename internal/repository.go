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

type BotConfig struct {
	WelcomeMessage string   `json:"welcome_message"`
	MeetingPoints  []string `json:"meeting_points"`
	MenuLink       string   `json:"menu_link"`
}

type Product struct {
	ID          string  `json:"id"`
	CategoryID  *string `json:"category_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Price       float64 `json:"price"`
	Available   bool    `json:"available"`
}

type CoverageZone struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	DeliveryFee float64 `json:"delivery_fee"`
	MinOrder    float64 `json:"min_order"`
}

type OrderRecord struct {
	ID      string
	Status  string
	Total   float64
	Address string
	Notes   string
	Items   []OrderItemRecord
}

type OrderItemRecord struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

type Tenant struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	PhoneNumberID string          `json:"phone_number_id"`
	POSProvider   string          `json:"pos_provider"`
	POSConfig     json.RawMessage `json:"pos_config"`
	BotConfigRaw  json.RawMessage `json:"-"`
	BotConfig     BotConfig       `json:"bot_config"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	WhatsAppToken string          `json:"whatsapp_token"`
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
		&tenant.BotConfigRaw,
		&tenant.CreatedAt,
		&tenant.UpdatedAt,
		&tenant.WhatsAppToken,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, err
	}

	if len(tenant.BotConfigRaw) > 0 {
		_ = json.Unmarshal(tenant.BotConfigRaw, &tenant.BotConfig)
	}

	return &tenant, nil
}

func (r *Repository) GetProducts(ctx context.Context, tenantID string) ([]Product, error) {
	rows, err := r.db.Query(ctx, db.QueryGetProducts, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(
			&p.ID,
			&p.CategoryID,
			&p.Name,
			&p.Description,
			&p.Price,
			&p.Available,
		); err != nil {
			return nil, err
		}
		products = append(products, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return products, nil
}

func (r *Repository) GetCoverageZones(ctx context.Context, tenantID string) ([]CoverageZone, error) {
	rows, err := r.db.Query(ctx, db.QueryGetCoverageZones, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []CoverageZone
	for rows.Next() {
		var z CoverageZone
		if err := rows.Scan(&z.ID, &z.Name, &z.DeliveryFee, &z.MinOrder); err != nil {
			return nil, err
		}
		zones = append(zones, z)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return zones, nil
}

func (r *Repository) GetActiveOrdersByPhone(ctx context.Context, tenantID string, phone string) ([]OrderRecord, error) {
	rows, err := r.db.Query(ctx, db.QueryGetActiveOrdersByPhone, tenantID, phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []OrderRecord
	for rows.Next() {
		var o OrderRecord
		var itemsJSON string
		if err := rows.Scan(&o.ID, &o.Status, &o.Total, &o.Address, &o.Notes, &itemsJSON); err != nil {
			return nil, err
		}
		
		if itemsJSON != "" {
			_ = json.Unmarshal([]byte(itemsJSON), &o.Items)
		}
		
		orders = append(orders, o)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return orders, nil
}
