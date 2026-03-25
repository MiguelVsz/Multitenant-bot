package agents

import (
	"encoding/json"
	"fmt"
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

	switch currentState {
	case "IDLE", "":
		// Según tu flujo: "Agente IA TIENDA muestra ciudades disponibles"
		res.Message = "¡Claro! Por favor, dime en qué ciudad te encuentras para ver los puntos de recogida."
		res.NextState = StatePickupAwaitingCity

	case StatePickupAwaitingCity:
		// Guardamos Bogotá por defecto o lo que el usuario confirme
		context["city"] = "Bogotá"

		// Según tu flujo: "Mostrar puntos de recogida disponibles"
		res.Message = "Perfecto, en Bogotá tenemos estos puntos disponibles:\n" +
			"1. Centro Comercial Andino\n" +
			"2. Portal Norte\n" +
			"3. Salitre Plaza\n\n" +
			"¿En cuál de ellos quieres recoger tu pedido?"

		res.NextState = StatePickupAwaitingStore

	case StatePickupAwaitingStore:
		// Cliente selecciona punto -> Validamos (aquí puedes meter lógica de DB)
		context["store"] = userInput
		res.Message = fmt.Sprintf("Perfecto, punto seleccionado: %s. Ahora, ¿qué productos deseas ordenar?", userInput)
		res.NextState = StatePickupAwaitingProduct

	case StatePickupAwaitingProduct:
		// Gestión de productos y Upselling (según tu diagrama)
		context["products"] = userInput
		res.Message = "¡Excelente elección! ¿Te gustaría agrandar tu combo por un valor adicional?"
		res.NextState = StatePickupConfirming

	case StatePickupConfirming:
		// Resumen y confirmación final
		res.Message = "Generando tu resumen de compra y link de pago... Dame un momento."
		res.NextState = "FINISHED"
	}

	res.NewContext = context
	return res
}
