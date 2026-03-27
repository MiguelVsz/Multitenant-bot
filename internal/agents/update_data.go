package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/models"
	"strings"
)

const (
	StateUpdateSelectField = "UPDATE_SELECT_FIELD"
	StateUpdateAwaitingVal = "UPDATE_AWAITING_VALUE"
	StateUpdateConfirm     = "UPDATE_CONFIRMATION"
	StateUpdateApply       = "UPDATE_APPLY" // webhook hace el update en BD
)

func HandleUpdateData(userInput string, currentState string, currentContext string) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse
	backBtn := models.InteractiveButton{ID: "menu_principal", Title: "🏠 Menú Principal"}

	switch currentState {
	case "UPDATE_START", "IDLE", "":
		res.Message = "👤 *Actualizar Datos*\n━━━━━━━━━━━━━━━━\n\n¿Qué dato deseas actualizar?"
		res.NextState = StateUpdateSelectField
		res.Buttons = []models.InteractiveButton{
			{ID: "upd_field_1", Title: "1️⃣ Nombre"},
			{ID: "upd_field_2", Title: "2️⃣ Correo"},
			{ID: "upd_field_3", Title: "3️⃣ Teléfono"},
			{ID: "upd_field_4", Title: "4️⃣ Dirección"},
		}

	case StateUpdateSelectField:
		fieldName, fieldLabel := resolveUpdateField(userInput)
		if fieldName == "" {
			res.Message = "No reconocí esa opción. ¿Qué dato deseas actualizar?"
			res.NextState = StateUpdateSelectField
			res.Buttons = []models.InteractiveButton{
				{ID: "upd_field_1", Title: "1️⃣ Nombre"},
				{ID: "upd_field_2", Title: "2️⃣ Correo"},
				{ID: "upd_field_3", Title: "3️⃣ Teléfono"},
				{ID: "upd_field_4", Title: "4️⃣ Dirección"},
				backBtn,
			}
			res.NewContext = context
			return res
		}
		context["field_to_update"] = fieldName
		context["field_label"] = fieldLabel
		res.Message = fmt.Sprintf("✏️ Por favor ingresa tu nuevo *%s*:", fieldLabel)
		res.NextState = StateUpdateAwaitingVal
		res.Buttons = []models.InteractiveButton{backBtn}

	case StateUpdateAwaitingVal:
		context["new_value"] = userInput
		res.Message = fmt.Sprintf("¿Confirmas cambiar tu *%s* a:\n\n📝 *%s*?",
			context["field_label"], userInput)
		res.NextState = StateUpdateConfirm
		res.Buttons = []models.InteractiveButton{
			{ID: "upd_confirm_yes", Title: "✅ Sí, confirmar"},
			{ID: "upd_confirm_no", Title: "❌ Cancelar"},
			backBtn,
		}

	case StateUpdateConfirm:
		confirmed := userInput == "upd_confirm_yes" ||
			strings.ToLower(strings.TrimSpace(userInput)) == "si" ||
			strings.ToLower(strings.TrimSpace(userInput)) == "sí"

		if confirmed {
			if context["customer_id"] == "" {
				res.Message = "No encontré tu perfil. Por favor contacta soporte."
				res.NextState = "IDLE"
				res.Buttons = []models.InteractiveButton{backBtn}
				res.NewContext = context
				return res
			}
			// Señalar al webhook que haga el update en BD
			res.NextState = StateUpdateApply
			res.Message = "" // el webhook construye el mensaje de éxito/error
		} else {
			res.Message = "Actualización cancelada. ¿Deseas modificar otro dato?"
			res.NextState = StateUpdateSelectField
			res.Buttons = []models.InteractiveButton{
				{ID: "upd_field_1", Title: "1️⃣ Nombre"},
				{ID: "upd_field_2", Title: "2️⃣ Correo"},
				{ID: "upd_field_3", Title: "3️⃣ Teléfono"},
				{ID: "upd_field_4", Title: "4️⃣ Dirección"},
				backBtn,
			}
		}
	}

	res.NewContext = context
	return res
}

func resolveUpdateField(input string) (fieldName, fieldLabel string) {
	switch input {
	case "upd_field_1", "1":
		return "name", "nombre"
	case "upd_field_2", "2":
		return "email", "correo electrónico"
	case "upd_field_3", "3":
		return "phone", "teléfono"
	case "upd_field_4", "4":
		return "address", "dirección"
	}
	lower := strings.ToLower(strings.TrimSpace(input))
	switch {
	case strings.Contains(lower, "nombre"):
		return "name", "nombre"
	case strings.Contains(lower, "correo") || strings.Contains(lower, "email"):
		return "email", "correo electrónico"
	case strings.Contains(lower, "telefono") || strings.Contains(lower, "teléfono"):
		return "phone", "teléfono"
	case strings.Contains(lower, "direccion") || strings.Contains(lower, "dirección"):
		return "address", "dirección"
	}
	return "", ""
}
