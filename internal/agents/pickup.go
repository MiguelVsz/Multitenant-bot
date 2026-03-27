package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/models"
	"strings"
	"unicode"
)

const (
	StatePickupAwaitingCity    = "PICKUP_AWAITING_CITY"
	StatePickupAwaitingStore   = "PICKUP_AWAITING_STORE"
	StatePickupConfirmingStore = "PICKUP_CONFIRMING_STORE"
	StatePickupAwaitingProduct = "PICKUP_AWAITING_PRODUCT"
	StatePickupUpsell          = "PICKUP_UPSELL"
	StatePickupConfirming      = "PICKUP_CONFIRMING"
)

// PickupResponse define la estructura de retorno del agente pickup
type PickupResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
	Buttons    []models.InteractiveButton
}

// HandlePickup maneja el flujo de recogida en tienda usando zonas de cobertura de la BD y la IA para productos
func HandlePickup(
	userInput string,
	currentState string,
	currentContext string,
	zones []models.CoverageZone,
	products []models.Product,
) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse
	apiKey := resolveAPIKey()
	menuBtn := models.InteractiveButton{ID: "menu_principal", Title: "🏠 Menú Principal"}

	switch currentState {
	case "IDLE", "":
		res.Message = "🥡 *Recoger en Tienda*\n━━━━━━━━━━━━━━━━\n\n¡Perfecto! ¿En qué *ciudad* te encuentras para mostrarte los puntos de recogida disponibles?"
		res.NextState = StatePickupAwaitingCity
		res.Buttons = []models.InteractiveButton{menuBtn}

	case StatePickupAwaitingCity:
		city := strings.TrimSpace(userInput)
		context["city"] = city
		matched := filterZonesByCity(zones, city)
		if len(matched) == 0 {
			matched = zones
		}
		if len(matched) == 0 {
			res.Message = "Lo siento, no encontré puntos de recogida disponibles. Por favor contacta soporte."
			res.NextState = "IDLE"
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_4", Title: "🎧 Ir a Soporte"},
				menuBtn,
			}
			res.NewContext = context
			return res
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🏪 Puntos de recogida disponibles cerca de *%s*:\n", city))
		sb.WriteString("─────────────────────\n\n")

		var storeIDs, storeNames []string
		for i, z := range matched {
			sb.WriteString(fmt.Sprintf("%d️⃣ *%s*\n", i+1, z.Name))
			storeIDs = append(storeIDs, z.ID)
			storeNames = append(storeNames, z.Name)
		}
		sb.WriteString("\n¿En cuál de ellos quieres recoger? (Responde con el número)")

		context["store_ids"] = strings.Join(storeIDs, "|")
		context["store_names"] = strings.Join(storeNames, "|")
		res.Message = sb.String()
		res.NextState = StatePickupAwaitingStore
		res.Buttons = []models.InteractiveButton{menuBtn}

	case StatePickupAwaitingStore:
		storeNamesList := strings.Split(context["store_names"], "|")
		storeName := strings.TrimSpace(userInput)

		idx := 0
		fmt.Sscanf(strings.TrimSpace(userInput), "%d", &idx)
		if idx > 0 && idx <= len(storeNamesList) {
			storeName = storeNamesList[idx-1]
		} else {
			for _, name := range storeNamesList {
				if strings.Contains(normalizeText(name), normalizeText(userInput)) ||
					strings.Contains(normalizeText(userInput), normalizeText(name)) {
					storeName = name
					break
				}
			}
		}

		context["store"] = storeName
		res.Message = fmt.Sprintf("📍 Has seleccionado: *%s*\n\n¿Confirmas que recogerás aquí tu pedido?", storeName)
		res.NextState = StatePickupConfirmingStore
		res.Buttons = []models.InteractiveButton{
			{ID: "pickup_confirm_store", Title: "✅ Sí, confirmar"},
			{ID: "pickup_change_store", Title: "🔄 Cambiar punto"},
			menuBtn,
		}

	case StatePickupConfirmingStore:
		tn := normalizeText(userInput)
		if tn == "pickup_confirm_store" || isPositive(userInput) {
			// Cargar catálogo en contexto para la IA de pickup
			context["catalog"] = buildProductCatalogString(products)
			res.Message = fmt.Sprintf(
				"✅ Perfecto. Tu pedido será para recoger en *%s*.\n\n🍕 ¿Qué deseas pedir? Puedes escribir el nombre del producto, pedir una recomendación o ver la carta:",
				context["store"],
			)
			res.NextState = StatePickupAwaitingProduct
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta"},
				menuBtn,
			}
		} else if userInput == "pickup_change_store" || isNegative(userInput) {
			res.Message = "Sin problema. ¿En qué ciudad buscas el punto de recogida?"
			res.NextState = StatePickupAwaitingCity
			delete(context, "store")
			res.Buttons = []models.InteractiveButton{menuBtn}
		} else {
			res.Message = fmt.Sprintf("¿Confirmas recoger en *%s*?", context["store"])
			res.NextState = StatePickupConfirmingStore
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_confirm_store", Title: "✅ Sí, confirmar"},
				{ID: "pickup_change_store", Title: "🔄 Cambiar punto"},
				menuBtn,
			}
		}

	case StatePickupAwaitingProduct:
		// Guard: comandos de sistema
		if strings.HasPrefix(normalizeText(userInput), "menu_") || userInput == "menu_principal" {
			res.Message = ""
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}

		// Caso 1: El usuario pide una recomendación
		if IsRecommendationQuery(userInput) {
			catalogStr := context["catalog"]
			if catalogStr == "" {
				catalogStr = buildProductCatalogString(products)
			}
			recommendation := AskAIRecommendation(userInput, catalogStr, "nuestra pizzería")
			if recommendation == "" {
				recommendation = buildSimpleRecommendation(products)
			}
			res.Message = recommendation
			res.NextState = StatePickupAwaitingProduct
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta completa"},
				menuBtn,
			}
			res.NewContext = context
			return res
		}

		// Caso 2: El usuario intenta pedir un producto — validar contra catálogo con IA (Agente Maestro)
		if len(products) > 0 && apiKey != "" {
			recentHistory := []models.AIMessage{}
			storeAddr := context["store"] + ", " + context["city"]
			cartStr := context["products"]
			action, aiMsg, productName, quantity := processOrderAI(userInput, storeAddr, cartStr, products, recentHistory, apiKey)
			
			if action == "reply" {
				res.Message = aiMsg
				res.NextState = StatePickupAwaitingProduct
				res.Buttons = []models.InteractiveButton{
					{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				}
				res.NewContext = context
				return res
			}

			if action == "add_product" {
				// Encontrar el producto real y confirmar
				var matched *models.Product
				for i, p := range products {
					if strings.EqualFold(p.Name, productName) {
						matched = &products[i]
						break
					}
				}
				if matched != nil {
					existing := context["products"]
					if existing == "" {
						context["products"] = fmt.Sprintf("%dx %s ($%.0f)", quantity, matched.Name, matched.Price*float64(quantity))
					} else {
						context["products"] += fmt.Sprintf(", %dx %s", quantity, matched.Name)
					}
					
					upsellMsg := aiMsg
					if upsellMsg == "" {
						upsellMsg = fmt.Sprintf("🍕 ¡Excelente elección! Agregué *%dx %s* a tu pedido. ", quantity, matched.Name)
					} else {
						upsellMsg += " "
					}
					upsellMsg += "¿Deseas agregar algo más o confirmamos tu pedido?"
					
					res.Message = upsellMsg
					res.NextState = StatePickupUpsell
					res.Buttons = []models.InteractiveButton{
						{ID: "pickup_upsell_yes", Title: "✅ Sí, agregar más"},
						{ID: "pickup_upsell_no", Title: "👎 No, continuar"},
						menuBtn,
					}
					res.NewContext = context
					return res
				}
			}
		} else if len(products) == 0 {
			// Sin catálogo en BD: guardar lo que el usuario escribe
			context["products"] = userInput
			res.Message = fmt.Sprintf("🍕 Entendido: *%s*\n\n¿Te gustaría añadir algo más?", userInput)
			res.NextState = StatePickupUpsell
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_upsell_yes", Title: "✅ Sí, agregar"},
				{ID: "pickup_upsell_no", Title: "👎 No, continuar"},
				menuBtn,
			}
			res.NewContext = context
			return res
		}

		// Caso 3: No se encontró el producto → mostrar carta disponible
		var sb strings.Builder
		sb.WriteString("❌ No encontré ese producto en nuestra carta.\n\n")
		sb.WriteString("📋 *Productos disponibles:*\n")
		for _, p := range products {
			sb.WriteString(fmt.Sprintf("• *%s* — $%.0f\n", p.Name, p.Price))
		}
		sb.WriteString("\n¿Cuál de estos deseas pedir?")
		res.Message = sb.String()
		res.NextState = StatePickupAwaitingProduct
		res.Buttons = []models.InteractiveButton{
			{ID: "menu_1", Title: "🍕 Ver Carta completa"},
			menuBtn,
		}

	case StatePickupUpsell:
		if userInput == "pickup_upsell_yes" || isPositive(userInput) {
			context["upsell"] = "si"
			res.Message = "¿Qué más deseas agregar? Escríbelo y lo busco en nuestra carta:"
			res.NextState = StatePickupAwaitingProduct
		} else {
			totalStr := ""
			if t := context["products"]; t != "" {
				totalStr = "\n🛒 Pedido: " + t
			}
			res.Message = fmt.Sprintf(
				"📝 *Resumen – Recogida en Tienda*\n━━━━━━━━━━━━━━━━\n📍 Punto: *%s*\n🏙️ Ciudad: *%s*%s\n\n💰 El precio final se confirma en tienda.\n\n¿Confirmas tu pedido?",
				context["store"], context["city"], totalStr,
			)
			res.NextState = StatePickupConfirming
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_final_ok", Title: "✅ Confirmar pedido"},
				{ID: "pickup_final_cancel", Title: "❌ Cancelar"},
			}
		}

	case StatePickupConfirming:
		if userInput == "pickup_final_ok" || isPositive(userInput) {
			res.Message = fmt.Sprintf(
				"✅ *¡Pedido confirmado!*\n\nTu pedido para recoger en *%s* ha sido registrado.\n\n⏱️ Tiempo estimado: 20-30 min.\n\n¡Gracias por elegirnos! 🍕",
				context["store"],
			)
			res.NextState = "FINISHED"
			res.Buttons = []models.InteractiveButton{menuBtn}
		} else {
			res.Message = "Pedido cancelado. Si deseas iniciar de nuevo, selecciona *Recoger en Tienda* desde el menú."
			res.NextState = "IDLE"
			res.Buttons = []models.InteractiveButton{menuBtn}
		}
	}

	res.NewContext = context
	return res
}

// buildProductCatalogString construye un string legible con el catálogo de productos
func buildProductCatalogString(products []models.Product) string {
	if len(products) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range products {
		sb.WriteString(fmt.Sprintf("- %s: $%.0f", p.Name, p.Price))
		if p.Description != nil && *p.Description != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", *p.Description))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildSimpleRecommendation devuelve una recomendación sin IA cuando la API no está disponible
func buildSimpleRecommendation(products []models.Product) string {
	if len(products) == 0 {
		return "¡Explora nuestra carta y elige lo que más te apetezca!"
	}
	best := products[0]
	for _, p := range products {
		// Preferir pizzas si hay
		if strings.Contains(strings.ToLower(p.Name), "pizza") {
			best = p
			break
		}
	}
	return fmt.Sprintf("¡Te recomiendo nuestra *%s* ($%.0f)! Es una de las favoritas de nuestros clientes. 🍕 ¿Te la incluyo en tu pedido?", best.Name, best.Price)
}

// filterZonesByCity filtra zonas por ciudad con búsqueda flexible
func filterZonesByCity(zones []models.CoverageZone, city string) []models.CoverageZone {
	cityNorm := normalizeText(city)
	var matched []models.CoverageZone
	for _, z := range zones {
		if strings.Contains(normalizeText(z.Name), cityNorm) || strings.Contains(cityNorm, normalizeText(z.Name)) {
			matched = append(matched, z)
		}
	}
	return matched
}

// normalizeText normaliza texto para comparación (minúsculas, sin tildes, sin caracteres especiales)
func normalizeText(s string) string {
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u",
		"ñ", "n", "Ñ", "n",
	)
	cleaned := replacer.Replace(s)
	var b strings.Builder
	for _, r := range cleaned {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '_' {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimSpace(b.String())
}
