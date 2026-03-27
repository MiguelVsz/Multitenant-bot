package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/pos"
	"strings"
)

const (
	StatePickupAwaitingCity    = "PICKUP_AWAITING_CITY"
	StatePickupAwaitingStore   = "PICKUP_AWAITING_STORE"
	StatePickupConfirmingStore = "PICKUP_CONFIRMING_STORE"
	StatePickupAwaitingProduct = "PICKUP_AWAITING_PRODUCT"
	StatePickupUpsell          = "PICKUP_UPSELL"
	StatePickupConfirming      = "PICKUP_CONFIRMING"
	StatePickupAwaitingPayment = "PICKUP_AWAITING_PAYMENT"
)

// PickupResponse define la estructura de lo que devuelve la función
type PickupResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
}

func HandlePickup(userInput string, currentState string, currentContext string) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse
	api := pos.NewInOutClient()

	switch currentState {
	case "IDLE", "":
		res.Message = "🥡 *Recoger en Tienda*\n━━━━━━━━━━━━━━━━\n\n¡Perfecto! Dime en qué *ciudad* te encuentras para mostrarte los puntos de recogida disponibles."
		res.NextState = StatePickupAwaitingCity

	case StatePickupAwaitingCity:
		context["city"] = userInput
		stores, err := api.GetPointSales()
		if err != nil || len(stores) == 0 {
			res.Message = "Lo siento, hubo un problema al consultar nuestras tiendas. Por favor intenta de nuevo en unos minutos."
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🏪 Puntos de recogida en *%s*:\n", userInput))
		sb.WriteString("─────────────────────\n\n")
		for i, name := range stores {
			sb.WriteString(fmt.Sprintf("%d️⃣ %s\n", i+1, name))
		}
		sb.WriteString("\n¿En cuál punto deseas recoger tu pedido?")
		context["stores_list"] = strings.Join(stores, "|")
		res.Message = sb.String()
		res.NextState = StatePickupAwaitingStore

	case StatePickupAwaitingStore:
		storeName := userInput
		// Intentar resolver por número
		storesList := strings.Split(context["stores_list"], "|")
		idx := 0
		fmt.Sscanf(strings.TrimSpace(userInput), "%d", &idx)
		if idx > 0 && idx <= len(storesList) {
			storeName = storesList[idx-1]
		}
		context["store"] = storeName
		res.Message = fmt.Sprintf("📍 Seleccionaste: *%s*\n\n¿Confirmas que recogerás aquí tu pedido?", storeName)
		res.NextState = StatePickupConfirmingStore

	case StatePickupConfirmingStore:
		if isPositive(userInput) || strings.ToLower(strings.TrimSpace(userInput)) == "si" {
			res.Message = fmt.Sprintf("✅ Perfecto. Tu pedido será para recoger en *%s*.\n\nPuedes ver nuestro menú aquí 👉 https://menu.donpepe.com\n\n¿Qué productos deseas ordenar? Escríbelos aquí:", context["store"])
			res.NextState = StatePickupAwaitingProduct
		} else if isNegative(userInput) {
			res.Message = "Sin problema. ¿En qué ciudad buscas el punto de recogida?"
			res.NextState = StatePickupAwaitingCity
			delete(context, "store")
		} else {
			res.Message = fmt.Sprintf("¿Confirmas recoger en *%s*? (Sí/No)", context["store"])
			res.NextState = StatePickupConfirmingStore
		}

	case StatePickupAwaitingProduct:
		context["products"] = userInput
		res.Message = fmt.Sprintf("🍕 ¡Excelente elección con *%s*!\n\n¿Te gustaría agregar algo más a tu pedido? Por ejemplo, una bebida o acompañamiento. (Responde Sí/No)", userInput)
		res.NextState = StatePickupUpsell

	case StatePickupUpsell:
		if isPositive(userInput) {
			context["upsell"] = "Bebida o acompañamiento adicional"
			res.Message = "¡Genial! Agrega ese complemento a tu pedido. Descríbelo:"
			res.NextState = StatePickupAwaitingProduct
			// Guardamos el estado del upsell pero volvemos a pedir más productos
			context["upsell_applied"] = "si"
		} else {
			// Mostrar resumen
			upsellText := ""
			if context["upsell_applied"] == "si" {
				upsellText = "\n• Complementos: ✅ Agregados"
			}
			res.Message = fmt.Sprintf(
				"📝 *Resumen de tu pedido (Recogida en Tienda)*\n━━━━━━━━━━━━━━━━\n• Punto: %s\n• Ciudad: %s\n• Productos: %s%s\n\n💰 El precio final se calculará en tienda.\n\n¿Confirmas tu pedido? (Sí/No)",
				context["store"], context["city"], context["products"], upsellText,
			)
			res.NextState = StatePickupConfirming
		}

	case StatePickupConfirming:
		if isPositive(userInput) {
			res.Message = fmt.Sprintf(
				"✅ *¡Pedido confirmado!*\n\nTu pedido para recoger en *%s* ha sido registrado.\n\n🏪 Dirígete al local con esta confirmación.\n⏱️ Tiempo estimado de preparación: 20-30 minutos.\n\n¡Gracias por elegirnos! 🍕",
				context["store"],
			)
			res.NextState = "FINISHED"
		} else {
			res.Message = "Entendido, he cancelado el proceso. Si deseas iniciar de nuevo, selecciona 🥡 *Recoger en Tienda* desde el menú."
			res.NextState = "IDLE"
		}
	}

	res.NewContext = context
	return res
}
