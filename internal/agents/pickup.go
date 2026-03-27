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

// PickupResponse define la estructura de lo que devuelve la función
type PickupResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
	Buttons    []models.InteractiveButton
}

// HandlePickup maneja el flujo de recogida en tienda usando zonas de cobertura de la BD
func HandlePickup(userInput string, currentState string, currentContext string, zones []models.CoverageZone) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse

	switch currentState {
	case "IDLE", "":
		res.Message = "🥡 *Recoger en Tienda*\n━━━━━━━━━━━━━━━━\n\n¡Perfecto! ¿En qué *ciudad* te encuentras para mostrarte los puntos de recogida disponibles?"
		res.NextState = StatePickupAwaitingCity
		res.Buttons = []models.InteractiveButton{
			{ID: "menu_principal", Title: "🏠 Menú Principal"},
		}

	case StatePickupAwaitingCity:
		city := strings.TrimSpace(userInput)
		context["city"] = city

		// Filtrar zonas por nombre de ciudad (búsqueda flexible)
		matched := filterZonesByCity(zones, city)

		if len(matched) == 0 {
			// Si no hay coincidencia, mostrar todas las zonas
			matched = zones
		}

		if len(matched) == 0 {
			res.Message = "Lo siento, no encontré puntos de recogida disponibles en este momento. Por favor contacta a soporte."
			res.NextState = "IDLE"
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_4", Title: "🎧 Ir a Soporte"},
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
			res.NewContext = context
			return res
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🏪 Puntos de recogida disponibles cerca de *%s*:\n", city))
		sb.WriteString("─────────────────────\n\n")

		// Guardar IDs de tiendas para selección posterior
		var storeIDs []string
		var storeNames []string
		for i, z := range matched {
			sb.WriteString(fmt.Sprintf("%d️⃣ *%s*\n", i+1, z.Name))
			storeIDs = append(storeIDs, z.ID)
			storeNames = append(storeNames, z.Name)
		}
		sb.WriteString("\n¿En cuál de ellos quieres recoger tu pedido? (Responde con el número)")

		context["store_ids"] = strings.Join(storeIDs, "|")
		context["store_names"] = strings.Join(storeNames, "|")
		res.Message = sb.String()
		res.NextState = StatePickupAwaitingStore
		res.Buttons = []models.InteractiveButton{
			{ID: "menu_principal", Title: "🏠 Menú Principal"},
		}

	case StatePickupAwaitingStore:
		storeNames := strings.Split(context["store_names"], "|")
		storeName := strings.TrimSpace(userInput)

		// Intentar resolver por número
		idx := 0
		fmt.Sscanf(strings.TrimSpace(userInput), "%d", &idx)
		if idx > 0 && idx <= len(storeNames) {
			storeName = storeNames[idx-1]
		} else {
			// Buscar por nombre aproximado
			for _, name := range storeNames {
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
			{ID: "menu_principal", Title: "🏠 Menú Principal"},
		}

	case StatePickupConfirmingStore:
		if textNorm := normalizeText(userInput); textNorm == "pickup_confirm_store" || isPositive(userInput) {
			res.Message = fmt.Sprintf("✅ Perfecto. Tu pedido será para recoger en *%s*.\n\n¿Qué productos deseas ordenar? Escríbelos aquí o consulta nuestra carta:", context["store"])
			res.NextState = StatePickupAwaitingProduct
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta"},
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
		} else if userInput == "pickup_change_store" || isNegative(userInput) {
			res.Message = "Sin problema. ¿En qué ciudad buscas el punto de recogida?"
			res.NextState = StatePickupAwaitingCity
			delete(context, "store")
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
		} else {
			res.Message = fmt.Sprintf("¿Confirmas recoger en *%s*?", context["store"])
			res.NextState = StatePickupConfirmingStore
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_confirm_store", Title: "✅ Sí, confirmar"},
				{ID: "pickup_change_store", Title: "🔄 Cambiar punto"},
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
		}

	case StatePickupAwaitingProduct:
		if strings.HasPrefix(normalizeText(userInput), "menu_") || userInput == "menu_principal" {
			// Deja que el webhook lo maneje
			res.Message = ""
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}
		context["products"] = userInput
		res.Message = fmt.Sprintf("🍕 ¡Excelente elección con *%s*!\n\n¿Te gustaría añadir algún acompañante o bebida a tu pedido?", userInput)
		res.NextState = StatePickupUpsell
		res.Buttons = []models.InteractiveButton{
			{ID: "pickup_upsell_yes", Title: "✅ Sí, agregar"},
			{ID: "pickup_upsell_no", Title: "👎 No, continuar"},
			{ID: "menu_principal", Title: "🏠 Menú Principal"},
		}

	case StatePickupUpsell:
		if userInput == "pickup_upsell_yes" || isPositive(userInput) {
			context["upsell"] = "si"
			res.Message = "¿Qué más deseas agregar? Escríbelo:"
			res.NextState = StatePickupAwaitingProduct
			context["products"] = context["products"] + " (+ extras)"
		} else {
			res.Message = fmt.Sprintf(
				"📝 *Resumen de tu pedido – Recogida en Tienda*\n━━━━━━━━━━━━━━━━\n📍 Punto: *%s*\n🏙️ Ciudad: *%s*\n🛒 Productos: *%s*\n\n💰 El precio final se confirma en tienda.\n\n¿Confirmas tu pedido?",
				context["store"], context["city"], context["products"],
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
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
		} else {
			res.Message = "Pedido cancelado. Si deseas iniciar de nuevo, selecciona *Recoger en Tienda* desde el menú."
			res.NextState = "IDLE"
			res.Buttons = []models.InteractiveButton{
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			}
		}
	}

	res.NewContext = context
	return res
}

// filterZonesByCity filtra las zonas de cobertura por nombre de ciudad (búsqueda flexible)
func filterZonesByCity(zones []models.CoverageZone, city string) []models.CoverageZone {
	cityNorm := normalizeText(city)
	var matched []models.CoverageZone
	for _, z := range zones {
		zoneNorm := normalizeText(z.Name)
		if strings.Contains(zoneNorm, cityNorm) || strings.Contains(cityNorm, zoneNorm) {
			matched = append(matched, z)
		}
	}
	return matched
}

// normalizeText normaliza texto para comparación (minúsculas, sin tildes)
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
