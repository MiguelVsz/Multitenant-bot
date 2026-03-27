package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"multi-tenant-bot/db"
	"multi-tenant-bot/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrTenantNotFound = errors.New("tenant not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.db.Ping(ctx)
}

func (r *Repository) ResolveTenantByPhoneNumberID(ctx context.Context, phoneNumberID string) (*models.Tenant, error) {
	row := r.db.QueryRow(ctx, db.QueryResolveTenantByPhoneNumberID, phoneNumberID)

	var tenant models.Tenant
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

func (r *Repository) GetProducts(ctx context.Context, tenantID string) ([]models.Product, error) {
	rows, err := r.db.Query(ctx, db.QueryGetProducts, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
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

func (r *Repository) GetCoverageZones(ctx context.Context, tenantID string) ([]models.CoverageZone, error) {
	rows, err := r.db.Query(ctx, db.QueryGetCoverageZones, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []models.CoverageZone
	for rows.Next() {
		var z models.CoverageZone
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

func (r *Repository) GetActiveOrdersByPhone(ctx context.Context, tenantID string, phone string) ([]models.OrderRecord, error) {
	rows, err := r.db.Query(ctx, db.QueryGetActiveOrdersByPhone, tenantID, phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.OrderRecord
	for rows.Next() {
		var o models.OrderRecord
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

func (r *Repository) GetCustomerByPhone(ctx context.Context, tenantID string, phone string) (*models.Customer, error) {
	row := r.db.QueryRow(ctx, db.QueryGetCustomerByPhone, tenantID, phone)

	var c models.Customer
	var metadataJSON []byte
	if err := row.Scan(&c.ID, &c.TenantID, &c.WhatsAppPhone, &c.Name, &c.Email, &metadataJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &c.Metadata)
	}

	return &c, nil
}

func (r *Repository) CreateCustomer(ctx context.Context, c *models.Customer) error {
	metadataJSON, _ := json.Marshal(c.Metadata)
	row := r.db.QueryRow(ctx, db.QueryCreateCustomer, c.TenantID, c.WhatsAppPhone, c.Name, c.Email, metadataJSON)

	var createdAt time.Time
	if err := row.Scan(&c.ID, &createdAt); err != nil {
		return err
	}

	return nil
}

func (r *Repository) UpdateCustomerMetadata(ctx context.Context, tenantID string, phone string, metadata map[string]interface{}) error {
	metadataJSON, _ := json.Marshal(metadata)
	_, err := r.db.Exec(ctx, db.QueryUpdateCustomerMetadata, tenantID, phone, metadataJSON)
	return err
}

// UpdateCustomerField actualiza un campo específico del cliente directamente en la BD.
// field puede ser: "name", "email", "address", "phone"
func (r *Repository) UpdateCustomerField(ctx context.Context, tenantID, customerID, field, value string) error {
	var query string
	switch field {
	case "name":
		query = db.QueryUpdateCustomerName
	case "email":
		query = db.QueryUpdateCustomerEmail
	case "address":
		query = db.QueryUpdateCustomerAddress
	case "phone":
		query = db.QueryUpdateCustomerPhone
	default:
		return fmt.Errorf("campo desconocido: %s", field)
	}
	_, err := r.db.Exec(ctx, query, tenantID, customerID, value)
	return err
}


func (r *Repository) CreateOrder(ctx context.Context, o *models.Order) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	metadataJSON, _ := json.Marshal(o.Metadata)
	row := tx.QueryRow(ctx, db.QueryCreateOrder,
		o.TenantID, o.CustomerID, o.OrderType, o.Status, o.DeliveryAddress,
		o.Subtotal, o.DeliveryFee, o.Total, o.PaymentMethod, metadataJSON,
	)

	var createdAt time.Time
	if err := row.Scan(&o.ID, &createdAt); err != nil {
		return err
	}

	for _, item := range o.Items {
		_, err := tx.Exec(ctx, db.QueryCreateOrderItem,
			o.ID, item.ProductID, item.Name, item.UnitPrice, item.Quantity, item.Subtotal,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
