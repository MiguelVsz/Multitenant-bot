package pos

import (
	"context"
	"encoding/json"
	"time"
)

type Customer struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type LineItem struct {
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Price    int64  `json:"price"`
}

type CreateOrderInput struct {
	TenantID     int64           `json:"tenant_id"`
	Customer     Customer        `json:"customer"`
	Items        []LineItem      `json:"items"`
	Channel      string          `json:"channel"`
	Reference    string          `json:"reference,omitempty"`
	DeliveryNote string          `json:"delivery_note,omitempty"`
	RawConfig    json.RawMessage `json:"raw_config,omitempty"`
}

type Order struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	PaymentURL  string    `json:"payment_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ProviderRef string    `json:"provider_ref,omitempty"`
}

type Provider interface {
	Name() string
	HealthCheck(ctx context.Context) error
	CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error)
}

type Factory func(config json.RawMessage) (Provider, error)
