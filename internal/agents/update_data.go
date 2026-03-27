package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/pos"
	"strings"
)

const SystemPromptUpdate = `Eres un asistente especializado en actualizar la información personal de clientes registrados.
Guía al usuario de forma segura para modificar únicamente sus propios datos.
Tono claro y preciso; confirma cada cambio antes de aplicarlo.

Flujo Estricto:
1. Verifica identidad (sesión activa o código).
2. Pregunta qué dato actualizar (nombre, teléfono, dirección, correo).
3. Muestra el nuevo valor y pide confirmación.
4. Llama a PATCH /users/{id}.
5. Confirma el cambio realizado.
6. Si la API falla, ofrece reintentar o contactar soporte.
7. Registra el cambio en el log con timestamp.

REGLAS: No modifiques datos sin confirmación explícita, no toques datos de otros usuarios, no omitas el log de auditoría.`

const (
	StateUpdateSelectField = "UPDATE_SELECT_FIELD"
	StateUpdateAwaitingVal = "UPDATE_AWAITING_VALUE"
	StateUpdateConfirm     = "UPDATE_CONFIRMATION"
)

func HandleUpdateData(userInput string, currentState string, currentContext string) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	// SIMULACIÓN DE LOGIN: Si no hay ID, pongamos uno de prueba para que no de 404
	if context["user_id"] == "" {
		context["user_id"] = "3003478228" // ID de prueba que existe en InOut
	}

	var res PickupResponse
	api := pos.NewInOutClient()

	switch currentState {
	case "UPDATE_START", "IDLE", "": // Añadimos IDLE por si el router falla
		res.Message = "¿Qué dato te gustaría actualizar?\n1. Nombre\n2. Correo\n3. Teléfono"
		res.NextState = StateUpdateSelectField

	case StateUpdateSelectField:
		// Traducimos el número al nombre técnico del campo que pide la API
		field := ""
		switch userInput {
		case "1":
			field = "name"
		case "2":
			field = "email"
		case "3":
			field = "phone"
		default:
			field = userInput // Por si escribe el nombre directamente
		}

		context["field_to_update"] = field
		res.Message = fmt.Sprintf("Entendido. Por favor, ingresa el nuevo valor para %s:", field)
		res.NextState = StateUpdateAwaitingVal

	case StateUpdateAwaitingVal:
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("¿Confirmas que quieres cambiar tu %s a: '%s'? (Sí/No)",
			context["field_to_update"], userInput)
		res.NextState = StateUpdateConfirm

	case StateUpdateConfirm:
		if strings.ToLower(userInput) == "si" || strings.ToLower(userInput) == "sí" {
			// Ahora enviará map[name:juana] en lugar de map[1:juana]
			updateData := map[string]interface{}{
				context["field_to_update"]: context["new_value"],
			}

			err := api.UpdateUser(context["user_id"], updateData)

			if err != nil {
				// Si la API dice 404, explícale al usuario que no lo encontramos
				res.Message = fmt.Sprintf("Lo siento, no encontré el perfil con el ID %s en nuestro sistema. ¿Estás registrado con nosotros?", context["user_id"])
				res.NextState = "IDLE" // Lo mandamos al inicio para que no se bloquee
			} else {
				res.Message = "✅ ¡Perfecto Juana! He actualizado tu nombre en el sistema."
				res.NextState = "FINISHED"
			}
		} else {
			res.Message = "Actualización cancelada."
			res.NextState = "IDLE"
		}
	}

	res.NewContext = context
	return res
}
