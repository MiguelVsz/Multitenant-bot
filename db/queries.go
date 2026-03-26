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
)
