package db

const (
	QueryInsertTenant = `
INSERT INTO gobot.tenants (name, whatsapp_phone_id, integration_type, integration_config)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at, updated_at`

	QueryResolveTenantByPhoneNumberID = `
SELECT id, name, whatsapp_phone_id, integration_type, integration_config, created_at, updated_at, whatsapp_token
FROM gobot.tenants
WHERE whatsapp_phone_id = $1`

	QueryResolveUserByPhone = `
SELECT external_id, id, name, email 
FROM gobot.customers 
WHERE whatsapp_phone = $1 AND tenant_id = $2`

	QueryUpdateUserRID = `
UPDATE gobot.customers 
SET external_id = $1, updated_at = NOW() 
WHERE id = $2`

	QueryInsertOrder = `
INSERT INTO gobot.orders (id, tenant_id, customer_id, store, city, products, status, created_at)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, 'pendiente_pago', NOW())
RETURNING id`
)
