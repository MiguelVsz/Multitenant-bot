package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/pos"
	"strings"
)

const SystemPromptUpdate = `Eres un asistente especializado en actualizar la información personal de clientes registrados.
Guía al usuario de forma segura para modificar únicamente sus propios datos.
Tono claro y preciso; confirma cada cambio antes de aplicarlo.`

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
		res.Message = "👤 *Actualizar Datos*\n━━━━━━━━━━━━━━━━\n\n¿Qué dato deseas actualizar?\n\n1️⃣ Nombre\n2️⃣ Correo electrónico\n3️⃣ Teléfono\n4️⃣ Dirección"
		res.NextState = StateUpdateSelectField

	case StateUpdateSelectField:
		fieldName := ""
		fieldLabel := ""
		switch userInput {
		case "1":
			fieldName = "name"
			fieldLabel = "nombre"
		case "2":
			fieldName = "email"
			fieldLabel = "correo electrónico"
		case "3":
			fieldName = "phone"
			fieldLabel = "teléfono"
		case "4":
			fieldName = "address"
			fieldLabel = "dirección"
		default:
			// Intentar mapear texto libre
			lower := strings.ToLower(strings.TrimSpace(userInput))
			switch {
			case strings.Contains(lower, "nombre"):
				fieldName, fieldLabel = "name", "nombre"
			case strings.Contains(lower, "correo") || strings.Contains(lower, "email"):
				fieldName, fieldLabel = "email", "correo electrónico"
			case strings.Contains(lower, "telefono") || strings.Contains(lower, "teléfono"):
				fieldName, fieldLabel = "phone", "teléfono"
			case strings.Contains(lower, "direccion") || strings.Contains(lower, "dirección"):
				fieldName, fieldLabel = "address", "dirección"
			default:
				res.Message = "No reconocí esa opción. Por favor responde con 1, 2, 3 o 4:\n\n1️⃣ Nombre\n2️⃣ Correo\n3️⃣ Teléfono\n4️⃣ Dirección"
				res.NextState = StateUpdateSelectField
				res.NewContext = context
				return res
			}
		}

		context["field_to_update"] = fieldName
		context["field_label"] = fieldLabel
		res.Message = fmt.Sprintf("Entendido. Por favor ingresa el nuevo valor para tu *%s*:", fieldLabel)
		res.NextState = StateUpdateAwaitingVal

	case StateUpdateAwaitingVal:
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("¿Confirmas cambiar tu *%s* a:\n\n📝 *%s*\n\n¿Es correcto? (Sí/No)",
			context["field_label"], userInput)
		res.NextState = StateUpdateConfirm

	case StateUpdateConfirm:
		if strings.ToLower(strings.TrimSpace(userInput)) == "si" || strings.ToLower(strings.TrimSpace(userInput)) == "sí" {
			userID := context["customer_id"]
			if userID == "" {
				res.Message = "No encontré tu perfil en sesión. Por favor contacta soporte."
				res.NextState = "IDLE"
				res.NewContext = context
				return res
			}

			updateData := map[string]interface{}{
				context["field_to_update"]: context["new_value"],
			}

			err := api.UpdateUser(userID, updateData)
			if err != nil {
				res.Message = fmt.Sprintf("Lo siento, no pudimos actualizar tu %s en este momento. Por favor intenta más tarde o contacta soporte.", context["field_label"])
				res.NextState = "IDLE"
			} else {
				res.Message = fmt.Sprintf("✅ ¡Tu *%s* ha sido actualizado a *%s* correctamente!\n\n¿Necesitas actualizar otro dato?",
					context["field_label"], context["new_value"])
				res.NextState = "FINISHED"
			}
		} else {
			res.Message = "Actualización cancelada. ¿Deseas actualizar otro dato?\n\n1️⃣ Nombre\n2️⃣ Correo\n3️⃣ Teléfono\n4️⃣ Dirección"
			res.NextState = StateUpdateSelectField
		}
	}

	res.NewContext = context
	return res
}
