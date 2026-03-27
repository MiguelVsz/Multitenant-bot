package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/db"
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

	var res PickupResponse
	api := pos.NewInOutClient()

	switch currentState {
	case "UPDATE_START", "IDLE", "":
		var inoutRID string
		var userID string
		var name string
		var email string

		userPhone := "573195508310"
		tenantID := context["tenant_id"]
		if tenantID == "" {
			tenantID = "ed2a4366-a42e-4043-a1ee-0a72cf897683" // ← fix principal
		}

		err := db.DB.QueryRow(db.QueryResolveUserByPhone, userPhone, tenantID).Scan(
			&inoutRID,
			&userID,
			&name,
			&email,
		)

		if err != nil {
			fmt.Printf("[DEBUG] Usuario no en DB, buscando en API: %v\n", err)
			inoutRID, _ = api.GetUserIDByPhone(userPhone)
			name = "Usuario"
		}

		context["user_id"] = inoutRID // 16617
		res.Message = fmt.Sprintf("Hola %s, ¿qué dato quieres actualizar?\n1. Nombre\n2. Correo\n3. Teléfono secundario", name)
		res.NextState = StateUpdateSelectField

	case StateUpdateSelectField:
		field := userInput
		if userInput == "1" {
			field = "name"
		}
		if userInput == "2" {
			field = "email"
		}
		if userInput == "3" {
			field = "phone"
		}

		context["field_to_update"] = field
		res.Message = fmt.Sprintf("Entendido. Por favor, ingresa el nuevo valor para: %s", field)
		res.NextState = StateUpdateAwaitingVal

	case StateUpdateAwaitingVal:
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("Confirmas que quieres cambiar tu %s a: '%s'? (Sí/No)",
			context["field_to_update"], userInput)
		res.NextState = StateUpdateConfirm

	case StateUpdateConfirm:
		if strings.ToLower(userInput) == "si" || strings.ToLower(userInput) == "sí" {
			updateData := map[string]interface{}{
				context["field_to_update"]: context["new_value"],
			}

			err := api.UpdateUser(context["user_id"], updateData)

			if err != nil {
				res.Message = fmt.Sprintf("Error técnico: %v. ¿Quieres intentar de nuevo?", err)
				res.NextState = "IDLE"
			} else {
				res.Message = "✅ ¡Listo! Tus datos han sido actualizados con éxito en el sistema."
				res.NextState = "FINISHED"
			}
		} else {
			res.Message = "Actualización cancelada. Volviendo al menú."
			res.NextState = "IDLE"
		}
	}

	res.NewContext = context
	return res
}
