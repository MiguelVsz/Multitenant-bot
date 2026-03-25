package pos

import (
	"context"
	"fmt"
	"time"
)

type GenericProvider struct {
	name string
}

func NewGenericProvider(name string) *GenericProvider {
	if name == "" {
		name = "generic"
	}
	return &GenericProvider{name: name}
}

func (p *GenericProvider) Name() string {
	return p.name
}

func (p *GenericProvider) HealthCheck(ctx context.Context) error {
	return ctx.Err()
}

func (p *GenericProvider) CreateOrder(ctx context.Context, input CreateOrderInput) (*Order, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &Order{
		ID:          fmt.Sprintf("%s-%d", p.name, time.Now().UnixNano()),
		Status:      "pending_payment",
		CreatedAt:   time.Now().UTC(),
		ProviderRef: input.Reference,
	}, nil
}
