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

	QueryGetActiveOrdersByPhone = `
SELECT o.id, o.status, o.total, COALESCE(o.delivery_address, ''), COALESCE(o.notes, ''),
       COALESCE(
           json_agg(
               json_build_object('name', oi.name, 'quantity', oi.quantity)
           ) FILTER (WHERE oi.id IS NOT NULL), '[]'
       ) as items
FROM gobot.orders o
JOIN gobot.customers c ON o.customer_id = c.id
LEFT JOIN gobot.order_items oi ON o.id = oi.order_id
WHERE o.tenant_id = $1 
  AND c.whatsapp_phone = $2 
  AND o.status IN ('pending', 'confirmed', 'preparing', 'ready', 'dispatched')
GROUP BY o.id, o.status, o.total, o.delivery_address, o.notes
ORDER BY o.created_at DESC`
)
