package agents

import (
	"encoding/json"
	"fmt"
	"multi-tenant-bot/db"
	"strings"
)

type UpdateResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
}

const SystemPromptUpdate = `Eres un asistente especializado en actualizar la información personal de clientes registrados.
Guía al usuario de forma segura para modificar únicamente sus propios datos.
Tono claro y preciso; confirma cada cambio antes de aplicarlo.`

const (
	StateUpdateSelectField = "UPDATE_SELECT_FIELD"
	StateUpdateAwaitingVal = "UPDATE_AWAITING_VALUE"
	StateUpdateConfirm     = "UPDATE_CONFIRMATION"
)

// fieldLabel traduce el campo técnico a texto amigable
func fieldLabel(field string) string {
	switch field {
	case "name":
		return "nombre"
	case "email":
		return "correo electrónico"
	case "whatsapp_phone":
		return "teléfono"
	case "default_address":
		return "dirección"
	default:
		return field
	}
}

func HandleUpdateData(userInput string, currentState string, currentContext string) UpdateResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res UpdateResponse

	switch currentState {
	case "UPDATE_START", "IDLE", "":
		// En producción este número viene del mensaje de WhatsApp
		// Buscamos con y sin '+' para cubrir ambos formatos
		userPhone := "573003478228"

		var customerID, name string
		err := db.DB.QueryRow(`
			SELECT id, name 
			FROM gobot.customers 
			WHERE whatsapp_phone = $1 
			   OR whatsapp_phone = $2
			LIMIT 1`,
			userPhone, "+"+userPhone,
		).Scan(&customerID, &name)

		if err != nil {
			fmt.Printf("[DEBUG SQL ERROR]: %v\n", err)
			res.Message = "No encontramos tu registro. Verifica que estés registrado en nuestro sistema."
			res.NextState = "IDLE"
			return res
		}

		context["customer_id"] = customerID
		context["customer_name"] = name

		res.Message = fmt.Sprintf(
			"Hola %s 👋 ¿Qué dato deseas actualizar?\n\n"+
				"1. Nombre\n"+
				"2. Correo electrónico\n"+
				"3. Teléfono\n"+
				"4. Dirección",
			name,
		)
		res.NextState = StateUpdateSelectField

	case StateUpdateSelectField:
		field := ""
		switch strings.TrimSpace(userInput) {
		case "1":
			field = "name"
		case "2":
			field = "email"
		case "3":
			field = "whatsapp_phone"
		case "4":
			field = "default_address"
		default:
			// Si dice no, listo, gracias, etc → terminar
			lower := strings.ToLower(strings.TrimSpace(userInput))
			if lower == "no" || strings.Contains(lower, "listo") ||
				strings.Contains(lower, "gracias") || strings.Contains(lower, "nada") {
				res.Message = fmt.Sprintf(
					"¡Perfecto %s! Tus datos están actualizados. 😊\n\nEscribe *menu principal* si necesitas algo más.",
					context["customer_name"],
				)
				res.NextState = "FINISHED"
				res.NewContext = context
				return res
			}
			res.Message = "Por favor selecciona una opción válida (1, 2, 3 o 4)\no escribe *No* si ya terminaste."
			res.NextState = StateUpdateSelectField
			res.NewContext = context
			return res
		}

		context["field_to_update"] = field
		res.Message = fmt.Sprintf("Ingresa tu nuevo %s:", fieldLabel(field))
		res.NextState = StateUpdateAwaitingVal

	case StateUpdateAwaitingVal:
		newValue := strings.TrimSpace(userInput)
		if newValue == "" {
			res.Message = "El valor no puede estar vacío. Intenta de nuevo."
			res.NextState = StateUpdateAwaitingVal
			res.NewContext = context
			return res
		}

		context["new_value"] = newValue
		res.Message = fmt.Sprintf(
			"¿Confirmas cambiar tu *%s* a:\n\n➡️ *%s*\n\n¿Aplicamos el cambio? (Sí/No)",
			fieldLabel(context["field_to_update"]),
			newValue,
		)
		res.NextState = StateUpdateConfirm

	case StateUpdateConfirm:
		input := strings.ToLower(strings.TrimSpace(userInput))

		if input != "si" && input != "sí" {
			res.Message = "Actualización cancelada. ¿Deseas cambiar otro dato?\n\n1. Nombre\n2. Correo electrónico\n3. Teléfono\n4. Dirección"
			res.NextState = StateUpdateSelectField
			res.NewContext = context
			return res
		}

		campo := context["field_to_update"]
		nuevoValor := context["new_value"]
		customerID := context["customer_id"]

		// Solo permitimos campos conocidos para evitar SQL injection
		allowedFields := map[string]bool{
			"name":            true,
			"email":           true,
			"whatsapp_phone":  true,
			"default_address": true,
		}

		if !allowedFields[campo] {
			res.Message = "Campo no permitido."
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}

		query := fmt.Sprintf(
			"UPDATE gobot.customers SET %s = $1, updated_at = NOW() WHERE id = $2",
			campo,
		)
		_, err := db.DB.Exec(query, nuevoValor, customerID)

		if err != nil {
			fmt.Printf("[DEBUG SQL ERROR]: %v\n", err)
			res.Message = "❌ Hubo un error al guardar los cambios. ¿Deseas intentarlo de nuevo? (Sí/No)"
			res.NextState = StateUpdateConfirm
			res.NewContext = context
			return res
		}

		res.Message = fmt.Sprintf(
			"✅ ¡Listo %s! Tu *%s* fue actualizado exitosamente.\n\n¿Deseas cambiar otro dato?\n\n1. Nombre\n2. Correo electrónico\n3. Teléfono\n4. Dirección\n\nO escribe *menu principal* para volver.",
			context["customer_name"],
			fieldLabel(campo),
		)
		res.NextState = StateUpdateSelectField
	}

	res.NewContext = context
	return res
}
