package models

import (
	"encoding/json"
	"time"
)

type InteractiveButton struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

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

type Customer struct {
	ID            string
	TenantID      string
	WhatsAppPhone string
	Name          string
	Email         string
	Metadata      map[string]interface{}
}

type Order struct {
	ID              string
	TenantID        string
	CustomerID      string
	OrderType       string
	Status          string
	DeliveryAddress string
	Subtotal        float64
	DeliveryFee     float64
	Total           float64
	PaymentMethod   string
	Metadata        map[string]interface{}
	Items           []OrderItem
}

type OrderItem struct {
	ProductID *string
	Name      string
	UnitPrice float64
	Quantity  int
	Subtotal  float64
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

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
