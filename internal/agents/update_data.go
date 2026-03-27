package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/models"
	"multi-tenant-bot/internal/pos"
	"strings"
)

const SystemPromptUpdate = `Eres un asistente especializado en actualizar la información personal de clientes registrados.`

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

	backBtn := models.InteractiveButton{ID: "menu_principal", Title: "🏠 Menú Principal"}

	switch currentState {
	case "UPDATE_START", "IDLE", "":
		res.Message = "👤 *Actualizar Datos*\n━━━━━━━━━━━━━━━━\n\n¿Qué dato deseas actualizar?\n\n1️⃣ Nombre\n2️⃣ Correo electrónico\n3️⃣ Teléfono\n4️⃣ Dirección"
		res.NextState = StateUpdateSelectField
		res.Buttons = []models.InteractiveButton{
			{ID: "upd_field_1", Title: "1️⃣ Nombre"},
			{ID: "upd_field_4", Title: "4️⃣ Dirección"},
			backBtn,
		}

	case StateUpdateSelectField:
		fieldName := ""
		fieldLabel := ""

		// Mapear botones de selección rápida
		switch userInput {
		case "upd_field_1", "1":
			fieldName, fieldLabel = "name", "nombre"
		case "upd_field_2", "2":
			fieldName, fieldLabel = "email", "correo electrónico"
		case "upd_field_3", "3":
			fieldName, fieldLabel = "phone", "teléfono"
		case "upd_field_4", "4":
			fieldName, fieldLabel = "address", "dirección"
		default:
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
				res.Message = "No reconocí esa opción. Por favor elige una:\n\n1️⃣ Nombre\n2️⃣ Correo\n3️⃣ Teléfono\n4️⃣ Dirección"
				res.NextState = StateUpdateSelectField
				res.Buttons = []models.InteractiveButton{
					{ID: "upd_field_1", Title: "1️⃣ Nombre"},
					{ID: "upd_field_4", Title: "4️⃣ Dirección"},
					backBtn,
				}
				res.NewContext = context
				return res
			}
		}

		context["field_to_update"] = fieldName
		context["field_label"] = fieldLabel
		res.Message = fmt.Sprintf("✏️ Por favor ingresa tu nuevo *%s*:", fieldLabel)
		res.NextState = StateUpdateAwaitingVal
		res.Buttons = []models.InteractiveButton{backBtn}

	case StateUpdateAwaitingVal:
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("¿Confirmas actualizar tu *%s* a:\n\n📝 *%s*?",
			context["field_label"], userInput)
		res.NextState = StateUpdateConfirm
		res.Buttons = []models.InteractiveButton{
			{ID: "upd_confirm_yes", Title: "✅ Sí, actualizar"},
			{ID: "upd_confirm_no", Title: "❌ Cancelar"},
			backBtn,
		}

	case StateUpdateConfirm:
		confirmed := userInput == "upd_confirm_yes" ||
			strings.ToLower(strings.TrimSpace(userInput)) == "si" ||
			strings.ToLower(strings.TrimSpace(userInput)) == "sí"

		if confirmed {
			userID := context["customer_id"]
			if userID == "" {
				res.Message = "No encontré tu perfil en sesión. Por favor contacta soporte."
				res.NextState = "IDLE"
				res.Buttons = []models.InteractiveButton{
					{ID: "menu_4", Title: "🎧 Ir a Soporte"},
					backBtn,
				}
				res.NewContext = context
				return res
			}

			updateData := map[string]interface{}{
				context["field_to_update"]: context["new_value"],
			}

			err := api.UpdateUser(userID, updateData)
			if err != nil {
				res.Message = fmt.Sprintf("Lo siento, no pudimos actualizar tu *%s*. Por favor intenta más tarde.", context["field_label"])
				res.NextState = "IDLE"
			} else {
				res.Message = fmt.Sprintf("✅ ¡Tu *%s* ha sido actualizado exitosamente a:\n\n📝 *%s*\n\n¿Deseas actualizar otro dato?",
					context["field_label"], context["new_value"])
				res.NextState = StateUpdateSelectField
				context["field_to_update"] = ""
				context["field_label"] = ""
				context["new_value"] = ""
			}
			res.Buttons = []models.InteractiveButton{
				{ID: "upd_field_1", Title: "1️⃣ Nombre"},
				{ID: "upd_field_4", Title: "4️⃣ Dirección"},
				backBtn,
			}
		} else {
			res.Message = "Actualización cancelada. ¿Deseas modificar otro dato?"
			res.NextState = StateUpdateSelectField
			res.Buttons = []models.InteractiveButton{
				{ID: "upd_field_1", Title: "1️⃣ Nombre"},
				{ID: "upd_field_4", Title: "4️⃣ Dirección"},
				backBtn,
			}
		}
	}

	res.NewContext = context
	return res
}
