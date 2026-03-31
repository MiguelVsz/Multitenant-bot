package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/db"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type PickupResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
}

const (
	StatePickupAwaitingCity    = "PICKUP_AWAITING_CITY"
	StatePickupAwaitingStore   = "PICKUP_AWAITING_STORE"
	StatePickupAwaitingProduct = "PICKUP_AWAITING_PRODUCT"
	StatePickupAwaitingUpsell  = "PICKUP_AWAITING_UPSELL"
	StatePickupConfirming      = "PICKUP_CONFIRMING"
)

const SystemPromptPickup = `Eres el asistente de WhatsApp para recogida en tienda.
Sigue este flujo de forma estricta, sin improvisar pasos:
1. Ciudades (Bogotá). 
2. Elegir ciudad. 
3. Mostrar puntos. 
4. Elegir punto. 
5. Confirmar punto. 
6. Menú. 
7. Selección + Upselling. 
8. Resumen. 
9. Pago. 
10. Guardar.`

// productInfo guarda nombre y precio juntos
type productInfo struct {
	Name  string
	Price float64
}

func HandlePickup(userInput string, currentState string, currentContext string) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse

	switch currentState {
	case "IDLE", "":
		res.Message = "¡Claro! Por favor, dime en qué ciudad te encuentras para ver los puntos de recogida."
		res.NextState = StatePickupAwaitingCity

	case StatePickupAwaitingCity:
		cityName := strings.TrimSpace(userInput)
		// No guardamos la ciudad aquí, la guardamos después con el nombre real de la BD
		context["city_input"] = cityName
		query := `
			SELECT name 
			FROM gobot.tenant_coverage_zones 
			WHERE unaccent(lower(name)) ILIKE unaccent(lower($1))
			AND active = true`
		rows, err := db.DB.Query(query, "%"+cityName+"%")
		if err != nil {
			res.Message = "Error al conectar con la base de datos de sedes."
			return res
		}
		defer rows.Close()

		var stores []string
		var firstCity string
		for rows.Next() {
			var name string
			rows.Scan(&name)
			stores = append(stores, name)
			// Extraemos la ciudad del primer resultado (ej: "Bogotá Norte..." → "Bogotá")
			if firstCity == "" {
				parts := strings.SplitN(name, " ", 2)
				firstCity = parts[0] // "Bogotá"
			}
		}
		context["city"] = firstCity // ✅ Guardamos "Bogotá" con tilde

		if len(stores) == 0 {
			res.Message = fmt.Sprintf("No encontramos puntos en '%s'. ¿Puedes intentar con otra ciudad?", cityName)
			return res
		}

		// Guardamos las tiendas en contexto como JSON
		storesJSON, _ := json.Marshal(stores)
		context["stores_list"] = string(storesJSON)

		msg := fmt.Sprintf("Perfecto, en %s tenemos estos puntos disponibles:\n\n", cityName)
		for i, name := range stores {
			msg += fmt.Sprintf("%d. %s\n", i+1, name)
		}
		msg += "\n¿En cuál de ellos quieres recoger tu pedido?"
		res.Message = msg
		res.NextState = StatePickupAwaitingStore

	case StatePickupAwaitingStore:
		// Recuperar lista de tiendas del contexto
		var stores []string
		json.Unmarshal([]byte(context["stores_list"]), &stores)

		var index int
		_, errScan := fmt.Sscanf(userInput, "%d", &index)
		if errScan == nil && index > 0 && index <= len(stores) {
			context["store"] = stores[index-1] // ✅ Guardamos el NOMBRE, no el número
		} else {
			context["store"] = userInput
		}

		// Cargar menú con precios
		query := "SELECT name, price FROM gobot.products WHERE available = true LIMIT 20"
		rows, err := db.DB.Query(query)
		if err != nil {
			res.Message = "Error al cargar el menú desde la base de datos."
			return res
		}
		defer rows.Close()

		var products []productInfo
		msg := "📋 *Menú:*\n\n"
		i := 1
		for rows.Next() {
			var p productInfo
			rows.Scan(&p.Name, &p.Price)
			products = append(products, p)
			msg += fmt.Sprintf("%d. %s ($%.0f)\n", i, p.Name, p.Price)
			i++
		}

		// Guardamos productos en contexto
		productsJSON, _ := json.Marshal(products)
		context["products_list"] = string(productsJSON)

		msg += "\n¿Qué producto deseas ordenar? (Escribe el número)"
		res.Message = msg
		res.NextState = StatePickupAwaitingProduct

	case StatePickupAwaitingProduct:
		var products []productInfo
		json.Unmarshal([]byte(context["products_list"]), &products)

		var index int
		_, errScan := fmt.Sscanf(userInput, "%d", &index)
		if errScan == nil && index > 0 && index <= len(products) {
			selected := products[index-1]
			context["product_name"] = selected.Name
			context["product_price"] = strconv.FormatFloat(selected.Price, 'f', 0, 64)
		} else {
			context["product_name"] = userInput
			context["product_price"] = "0"
		}

		res.Message = "¡Excelente elección! 🍔\n\n¿Deseas añadir papas por $5.900 adicionales? (Sí/No)"
		res.NextState = StatePickupAwaitingUpsell

	case StatePickupAwaitingUpsell:
		upsellPrice := 0.0
		upsellText := ""

		if strings.ToLower(strings.TrimSpace(userInput)) == "si" ||
			strings.ToLower(strings.TrimSpace(userInput)) == "sí" {
			upsellPrice = 5900
			upsellText = "Papas ($5.900)"
			context["upsell"] = "Papas"
			context["upsell_price"] = "5900"
		} else {
			context["upsell"] = ""
			context["upsell_price"] = "0"
		}

		// Calcular total
		productPrice, _ := strconv.ParseFloat(context["product_price"], 64)
		total := productPrice + upsellPrice

		// Construir resumen
		msg := fmt.Sprintf("📝 *Resumen de tu pedido:*\n\n"+
			"📍 Tienda: %s\n"+
			"🏙️ Ciudad: %s\n"+
			"🛒 Producto: %s ($%.0f)\n",
			context["store"],
			context["city"],
			context["product_name"],
			productPrice,
		)

		if upsellText != "" {
			msg += fmt.Sprintf("➕ Adicional: %s\n", upsellText)
		}

		msg += fmt.Sprintf("\n💰 *Total a pagar: $%.0f*\n\n¿Confirmas tu pedido? (Escribe CONFIRMAR)", total)
		context["total"] = strconv.FormatFloat(total, 'f', 0, 64)

		res.Message = msg
		res.NextState = StatePickupConfirming

	case StatePickupConfirming:
		if strings.ToLower(strings.TrimSpace(userInput)) != "confirmar" {
			res.Message = "Pedido cancelado. ¿En qué más te puedo ayudar?"
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}

		// ✅ Guardar en base de datos
		orderID := uuid.New().String()
		total, _ := strconv.ParseFloat(context["total"], 64)
		productPrice, _ := strconv.ParseFloat(context["product_price"], 64)
		upsellPrice, _ := strconv.ParseFloat(context["upsell_price"], 64)

		// Metadata con info de recogida
		metadata := map[string]string{
			"pickup_zone": context["store"],
			"city":        context["city"],
		}
		metadataJSON, _ := json.Marshal(metadata)

		// Insertar orden
		tenantID := "ed2a4366-a42e-4043-a1ee-0a72cf897683"

		_, err := db.DB.Exec(`
    INSERT INTO gobot.orders 
        (id, tenant_id, order_type, status, subtotal, delivery_fee, total, notes, metadata)
    VALUES 
        ($1, $2, 'pickup', 'pending', $3, 0, $4, $5, $6)`,
			orderID,
			tenantID,
			productPrice,
			total,
			fmt.Sprintf("Recogida en: %s", context["store"]),
			string(metadataJSON),
		)
		if err != nil {
			res.Message = fmt.Sprintf("Error al guardar el pedido: %v", err)
			return res
		}

		// Insertar producto principal en order_items
		_, err = db.DB.Exec(`
			INSERT INTO gobot.order_items 
				(id, order_id, name, unit_price, quantity, subtotal)
			VALUES 
				($1, $2, $3, $4, 1, $4)`,
			uuid.New().String(),
			orderID,
			context["product_name"],
			productPrice,
		)
		if err != nil {
			res.Message = fmt.Sprintf("Error al guardar el item: %v", err)
			return res
		}

		// Insertar upsell si aplica
		if context["upsell"] != "" && upsellPrice > 0 {
			_, err = db.DB.Exec(`
				INSERT INTO gobot.order_items 
					(id, order_id, name, unit_price, quantity, subtotal)
				VALUES 
					($1, $2, $3, $4, 1, $4)`,
				uuid.New().String(),
				orderID,
				context["upsell"],
				upsellPrice,
			)
			if err != nil {
				res.Message = fmt.Sprintf("Error al guardar el adicional: %v", err)
				return res
			}
		}

		res.Message = fmt.Sprintf(
			"✅ *¡Pedido confirmado!*\n\n"+
				"🆔 Pedido #%s\n"+
				"💰 Total: $%s\n\n"+
				"Pronto recibirás tu link de pago. ¡Gracias!",
			orderID[:8],
			context["total"],
		)
		res.NextState = "FINISHED"
	}

	res.NewContext = context
	return res
}
