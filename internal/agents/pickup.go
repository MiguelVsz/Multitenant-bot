package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/pos"
)

const SystemPromptPickup = `Eres el asistente de WhatsApp para recogida en tienda.
Sigue este flujo de forma estricta, sin improvisar pasos:

1. Muestra ciudades disponibles (Por ahora solo Bogotá).
2. Usuario elige ciudad.
3. Muestra puntos de recogida.
4. Usuario elige punto (si no es válido, vuelve al paso 3).
5. Confirma nombre y dirección del punto.
6. Envía URL del menú.
7. Recibe selección + recomienda productos adicionales (upselling).
8. Muestra resumen y pide confirmación (si no confirma, cancela).
9. Envía link de pago.
10. Guarda pedido con estado 'pendiente de pago'.`

const (
	StatePickupAwaitingCity    = "PICKUP_AWAITING_CITY"
	StatePickupAwaitingStore   = "PICKUP_AWAITING_STORE"
	StatePickupAwaitingProduct = "PICKUP_AWAITING_PRODUCT"
	StatePickupConfirming      = "PICKUP_CONFIRMING"
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
		res.Message = "¡Claro! Por favor, dime en qué ciudad te encuentras para ver los puntos de recogida."
		res.NextState = StatePickupAwaitingCity

	case StatePickupAwaitingCity:
		context["city"] = userInput
		stores, err := api.GetPointSales()
		if err != nil || len(stores) == 0 {
			res.Message = "Lo siento, hubo un problema al consultar nuestras tiendas. Por favor, intenta de nuevo en unos minutos."
			res.NextState = "IDLE"
			return res
		}

		msg := fmt.Sprintf("Perfecto, en %s tenemos estos puntos disponibles:\n\n", userInput)
		for i, name := range stores {
			msg += fmt.Sprintf("%d. %s\n", i+1, name)
		}
		msg += "\n¿En cuál de ellos quieres recoger tu pedido?"
		res.Message = msg
		res.NextState = StatePickupAwaitingStore

	case StatePickupAwaitingStore:
		context["store"] = userInput // Guardamos el punto elegido
		// PASO 6: Enviar URL del menú (Simulada por ahora)
		res.Message = fmt.Sprintf("Perfecto, punto seleccionado: %s.\n\n Puedes ver nuestro menú aquí: https://menu.inoutdelivery.com/dlk\n\n¿Qué productos deseas ordenar? (Escríbelos aquí)", userInput)
		res.NextState = StatePickupAwaitingProduct

	case StatePickupAwaitingProduct:
		context["products"] = userInput
		// PASO 7: Upselling (Recomendar algo más)
		res.Message = fmt.Sprintf("¡Excelente elección con '%s'! 🍔\n\n¿Te gustaría agrandar tu combo o añadir unas papas medianas por solo $5.900 adicionales? (Responde Sí/No)", userInput)
		res.NextState = StatePickupConfirming

	case StatePickupConfirming:
		// PASO 8 & 9: Resumen y Link de Pago
		upsell := "Sin adicionales"
		if userInput == "si" || userInput == "Si" || userInput == "SI" {
			upsell = "Combo Agrandado"
		}

		res.Message = fmt.Sprintf("📝 *Resumen de tu pedido:*\n- Tienda: %s\n- Ciudad: %s\n- Productos: %s\n- Adicional: %s\n\nTotal estimado: $24.900\n\n¿Confirmas tu pedido para generar el link de pago? (Responde CONFIRMAR)",
			context["store"], context["city"], context["products"], upsell)
		res.NextState = "AWAITING_PAYMENT_LINK"

	case "AWAITING_PAYMENT_LINK":
		if userInput == "confirmar" || userInput == "CONFIRMAR" {
			// PASO 10: Simulación de link de pago y guardado
			res.Message = "✅ ¡Pedido confirmado! Aquí tienes tu link de pago seguro: https://pagos.inout.com/ref123\n\nTu pedido quedará con estado 'Pendiente de Pago' hasta que completes la transacción. ¡Gracias por elegirnos!"
			res.NextState = "FINISHED"
		} else {
			res.Message = "Entendido, he cancelado el proceso. Si deseas empezar de nuevo, escribe 'menu principal'."
			res.NextState = "IDLE"
		}
	}

	res.NewContext = context
	return res
}
