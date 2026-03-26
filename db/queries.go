package db

const (
	QueryInsertTenant = `
INSERT INTO gobot.tenants (name, whatsapp_phone_id, integration_type, integration_config)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at, updated_at`

	QueryResolveTenantByPhoneNumberID = `
SELECT id, name, whatsapp_phone_id, integration_type, integration_config, bot_config, created_at, updated_at, whatsapp_token
FROM gobot.tenants
WHERE whatsapp_phone_id = $1`

	QueryGetProducts = `
SELECT id, category_id, name, description, price, available
FROM gobot.products
WHERE tenant_id = $1 AND available = true
ORDER BY sort_order ASC, name ASC`

	QueryGetCoverageZones = `
SELECT id, name, delivery_fee, min_order
FROM gobot.tenant_coverage_zones
WHERE tenant_id = $1 AND active = true
ORDER BY name ASC`
)
