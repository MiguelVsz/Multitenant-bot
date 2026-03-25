package agents

import (
	"encoding/json"
	"fmt"
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

	var res PickupResponse

	switch currentState {
	case "UPDATE_START":
		res.Message = "¿Qué dato te gustaría actualizar?\n1. Nombre\n2. Correo\n3. Teléfono secundario"
		res.NextState = StateUpdateSelectField

	case StateUpdateSelectField:
		// Guardamos qué campo quiere editar
		context["field_to_update"] = userInput
		res.Message = fmt.Sprintf("Entendido. Por favor, ingresa el nuevo valor para: %s", userInput)
		res.NextState = StateUpdateAwaitingVal

	case StateUpdateAwaitingVal:
		// Guardamos el nuevo valor temporalmente
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("Confirmas que quieres cambiar tu %s a: '%s'? (Sí/No)", 
			context["field_to_update"], userInput)
		res.NextState = StateUpdateConfirm

	case StateUpdateConfirm:
		if strings.ToLower(userInput) == "si" || strings.ToLower(userInput) == "sí" {
			// AQUÍ es donde luego llamaremos a la función de la DB
			res.Message = "¡Listo! Tus datos han sido actualizados con éxito."
			res.NextState = "IDLE"
		} else {
			res.Message = "Actualización cancelada. Volviendo al menú."
			res.NextState = "IDLE"
		}
	}

	res.NewContext = context
	return res
}