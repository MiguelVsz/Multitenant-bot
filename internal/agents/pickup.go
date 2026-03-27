package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/db"
	"multi-tenant-bot/internal/pos"
	"strings"
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
			res.Message = "Lo siento, hubo un problema al consultar nuestras tiendas. Intenta de nuevo en unos minutos."
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
		stores, err := api.GetPointSales()
		if err != nil {
			res.Message = "Error al consultar las tiendas."
			return res
		}

		var selectedStoreName string
		var index int
		_, errScan := fmt.Sscanf(userInput, "%d", &index)

		if errScan == nil && index > 0 && index <= len(stores) {
			selectedStoreName = stores[index-1]
		} else {
			selectedStoreName = userInput
		}

		context["store"] = selectedStoreName

		products, err := api.GetProducts()
		if err != nil || len(products) == 0 {
			res.Message = fmt.Sprintf("Punto seleccionado: %s.\n\nVe nuestro menú: https://menu.inoutdelivery.com/dlk\n\n¿Qué deseas ordenar?", selectedStoreName)
		} else {
			msg := fmt.Sprintf("Punto seleccionado: %s.\n\n📋 *Menú:*\n\n", selectedStoreName)
			for i, p := range products {
				if i >= 20 {
					msg += fmt.Sprintf("...y %d productos más en: https://menu.inoutdelivery.com/dlk\n", len(products)-20)
					break
				}
				msg += fmt.Sprintf("%d. %s\n", i+1, p.Name)
			}
			msg += "\n¿Qué productos deseas ordenar?"
			res.Message = msg
		}
		res.NextState = StatePickupAwaitingProduct

	case StatePickupAwaitingProduct:
		products, err := api.GetProducts()
		if err != nil {
			res.Message = "Error al obtener productos"
			return res
		}

		var selectedProductName string
		var index int
		_, errScan := fmt.Sscanf(userInput, "%d", &index)

		if errScan == nil && index > 0 && index <= len(products) {
			selectedProductName = products[index-1].Name
		} else {
			selectedProductName = userInput
		}
		context["products"] = selectedProductName

		res.Message = fmt.Sprintf("¡Excelente elección con '%s'! 🍔\n\n¿Te gustaría agrandar tu combo...?", selectedProductName)
		res.NextState = StatePickupConfirming

	case StatePickupConfirming:
		upsell := "Sin adicionales"
		if userInput == "si" || userInput == "Si" || userInput == "SI" {
			upsell = "Combo Agrandado"
		}
		res.Message = fmt.Sprintf("📝 *Resumen de tu pedido:*\n- Tienda: %s\n- Ciudad: %s\n- Productos: %s\n- Adicional: %s\n\nTotal estimado: $24.900\n\n¿Confirmas tu pedido para generar el link de pago? (Responde CONFIRMAR)",
			context["store"], context["city"], context["products"], upsell)
		res.NextState = "AWAITING_PAYMENT_LINK"

	case "AWAITING_PAYMENT_LINK":
		if strings.TrimSpace(strings.ToLower(userInput)) == "confirmar" {
			var orderID string
			err := db.DB.QueryRow(db.QueryInsertOrder,
				"ed2a4366-a42e-4043-a1ee-0a72cf897683",
				"",
				context["store"],
				context["city"],
				context["products"],
			).Scan(&orderID)

			if err != nil {
				fmt.Printf("[DEBUG] Error guardando pedido: %v\n", err)
			} else {
				fmt.Printf("[DEBUG] Pedido guardado con ID: %s\n", orderID)
			}

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
