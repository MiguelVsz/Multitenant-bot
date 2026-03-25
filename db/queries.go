package db

const (
	QueryInsertTenant = `
INSERT INTO tenants (name, phone_number_id, pos_provider, pos_config)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at, updated_at`

	QueryResolveTenantByPhoneNumberID = `
SELECT id, name, phone_number_id, pos_provider, pos_config, created_at, updated_at
FROM tenants
WHERE phone_number_id = $1`
)
