package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"multi-tenant-bot/internal/models"
)

// Constantes de Estado para Domicilios
const (
	StateDeliveryIdle            = "IDLE"
	StateDeliveryAwaitingAddress = "AWAITING_ADDRESS"
	StateDeliveryAwaitingProduct = "AWAITING_PRODUCT"
	StateDeliveryUpsell           = "UPSELLING"
	StateDeliveryConfirmingOrder = "CONFIRMING_ORDER"
	StateDeliveryPayment         = "PAYMENT"
	StateDeliveryConfirmingRegisteredAddress = "CONFIRMING_REGISTERED_ADDRESS"
	StateDeliveryPlaced                      = "ORDER_PLACED"
)

type DeliverySession struct {
	State         string             `json:"state"`
	Address       string             `json:"address,omitempty"`
	Cart          []models.OrderItem `json:"cart"`
	PhoneNumber   string             `json:"phone_number"`
	CustomerID    string             `json:"customer_id"`
	Total         float64            `json:"total"`
	SuggestedItem *models.Product    `json:"suggested_item,omitempty"`
}

type DeliveryResponse struct {
	Message    string
	NextState  string
	NewSession *DeliverySession
	Buttons    []models.InteractiveButton // Usando modelo compartido
}

func HandleDelivery(
	ctx context.Context,
	session *DeliverySession,
	userInput string,
	textNorm string,
	history []models.AIMessage,
	products []models.Product,
) *DeliveryResponse {
	if session == nil {
		session = &DeliverySession{State: StateDeliveryIdle}
	}

	apiKey := resolveAPIKey()

	switch session.State {
	case "", StateDeliveryIdle:
		if session.Address != "" {
			session.State = StateDeliveryConfirmingRegisteredAddress
			return &DeliveryResponse{
				Message:    fmt.Sprintf("Veo que tienes una dirección registrada: %s. ¿Deseas usarla o prefieres una nueva?", session.Address),
				NextState:  StateDeliveryConfirmingRegisteredAddress,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "use_reg_addr", Title: "📍 Usar Registrada"},
					{ID: "use_new_addr", Title: "🏠 Usar Nueva"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		session.State = StateDeliveryAwaitingAddress
		return &DeliveryResponse{
			Message:    "¡Claro! ¿A qué dirección enviamos tu pedido?",
			NextState:  StateDeliveryAwaitingAddress,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryConfirmingRegisteredAddress:
		if textNorm == "use_reg_addr" {
			session.State = StateDeliveryAwaitingProduct
			return &DeliveryResponse{
				Message:    fmt.Sprintf("Perfecto, enviaremos a: %s. ¿Qué te gustaría pedir?", session.Address),
				NextState:  StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		session.State = StateDeliveryAwaitingAddress
		return &DeliveryResponse{
			Message:    "Entendido. ¿A qué dirección enviamos tu pedido entonces?",
			NextState:  StateDeliveryAwaitingAddress,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryAwaitingAddress:
		session.Address = userInput
		session.State = StateDeliveryAwaitingProduct
		return &DeliveryResponse{
			Message:    fmt.Sprintf("✅ Dirección guardada: *%s*\n\n¿Qué te gustaría pedir? Escribe el nombre del producto o mira nuestra carta.", session.Address),
			NextState:  StateDeliveryAwaitingProduct,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryAwaitingProduct:
		// Guard: botones de sistema que no son productos
		if textNorm == "confirm_cancel" {
			return &DeliveryResponse{
				Message:    "",
				NextState:  StateDeliveryIdle,
				NewSession: &DeliverySession{State: StateDeliveryIdle},
			}
		}
		// Botones de adición de items al carrito (del upsell)
		if strings.HasPrefix(textNorm, "menu_") && textNorm != "menu_1" {
			return &DeliveryResponse{
				Message:   "",
				NextState: StateDeliveryIdle,
				NewSession: &DeliverySession{State: StateDeliveryIdle},
			}
		}
		// El usuario tocó un botón viejo de confirmación de dirección — ignorar y recordarles
		if textNorm == "use_reg_addr" || textNorm == "use_new_addr" {
			return &DeliveryResponse{
				Message:   fmt.Sprintf("¡Listo! Enviaremos tu pedido a: *%s*\n\n¿Qué producto deseas agregar a tu pedido?", session.Address),
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		// menu_1: mostrar carta inline SIN salir del agente delivery
		if textNorm == "menu_1" {
			var sb strings.Builder
			sb.WriteString("🍕 *Nuestra Carta*\n━━━━━━━━━━━━━━━━━━━━━━\n\n")
			for _, p := range products {
				sb.WriteString(fmt.Sprintf("*%s* — $%.0f\n", p.Name, p.Price))
				if p.Description != nil && *p.Description != "" {
					sb.WriteString(fmt.Sprintf("  _%s_\n", *p.Description))
				}
				sb.WriteString("\n")
			}
			sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\nEscribe el nombre del producto que quieres pedir:")
			return &DeliveryResponse{
				Message:    sb.String(),
				NextState:  StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				},
			}
		}
		// IA para identificar producto — limitar historial para evitar confusión con la carta
		recentHistory := history
		if len(recentHistory) > 6 {
			recentHistory = recentHistory[len(recentHistory)-6:]
		}
		productName, quantity, found := pickProductWithAI(userInput, products, recentHistory, apiKey)
		if !found {
			return &DeliveryResponse{
				Message:    "No logré identificar ese producto en nuestra carta. ¿Podrías escribir el nombre exactamente como aparece en la carta?",
				NextState:  StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}

		// Buscar detalles del producto real
		var selected models.Product
		for _, p := range products {
			if strings.EqualFold(p.Name, productName) {
				selected = p
				break
			}
		}

		item := models.OrderItem{
			ProductID: &selected.ID,
			Name:      selected.Name,
			UnitPrice: selected.Price,
			Quantity:  quantity,
			Subtotal:  selected.Price * float64(quantity),
		}
		session.Cart = append(session.Cart, item)
		session.Total += item.Subtotal

		// Estado de Upsell (Sugerencia de agrandado)
		session.State = StateDeliveryUpsell
		suggested := getUpsellSuggestion(selected, products, history, apiKey)
		session.SuggestedItem = suggested
		
		msg := fmt.Sprintf("¡Excelente! He añadido %d x %s a tu pedido. ", quantity, selected.Name)
		if suggested != nil {
			msg += fmt.Sprintf("¿Te gustaría acompañarlo con %s por solo $%.0f adicionales?", suggested.Name, suggested.Price)
		} else {
			msg += "¿Deseas algo más?"
		}

		return &DeliveryResponse{
			Message:    msg,
			NextState:  StateDeliveryUpsell,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "upsell_yes", Title: "✅ ¡Sí, genial!"},
				{ID: "upsell_no", Title: "👎 No, gracias"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryUpsell:
		upsellAccepted := textNorm == "upsell_yes" || textNorm == "upsell yes" || isPositive(userInput)
		if upsellAccepted && session.SuggestedItem != nil {
			item := models.OrderItem{
				ProductID: &session.SuggestedItem.ID,
				Name:      session.SuggestedItem.Name,
				UnitPrice: session.SuggestedItem.Price,
				Quantity:  1,
				Subtotal:  session.SuggestedItem.Price,
			}
			session.Cart = append(session.Cart, item)
			session.Total += item.Subtotal
			session.SuggestedItem = nil // Limpiar sugerencia ya usada
		}
		
		session.State = StateDeliveryConfirmingOrder
		return &DeliveryResponse{
			Message:    fmt.Sprintf("Entendido. Tu pedido actual es: %s por un total de $%.0f. ¿Confirmas tu pedido? (Si/No)", renderCart(session.Cart), session.Total),
			NextState:  StateDeliveryConfirmingOrder,
			NewSession: session,
		}

	case StateDeliveryConfirmingOrder:
		if textNorm == "confirm_ok" || isPositive(userInput) {
			session.State = StateDeliveryPayment
			return &DeliveryResponse{
				Message:    "¡Perfecto! ¿Cómo deseas pagar?",
				NextState:  StateDeliveryPayment,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "cash", Title: "💵 Efectivo"},
					{ID: "transfer", Title: "📲 Transferencia"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		if textNorm == "confirm_cancel" || isNegative(userInput) {
			return &DeliveryResponse{
				Message:    "Pedido cancelado. ¿Hay algo más en lo que pueda ayudarte?",
				NextState:  StateDeliveryIdle,
				NewSession: &DeliverySession{State: StateDeliveryIdle},
			}
		}
		return &DeliveryResponse{
			Message:    "Por favor, confirma tu pedido.",
			NextState:  StateDeliveryConfirmingOrder,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_ok", Title: "✅ Confirmar"},
				{ID: "confirm_edit", Title: "✏️ Editar"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryPayment:
		if textNorm == "cash" || strings.Contains(textNorm, "efectivo") {
			session.State = StateDeliveryPlaced
			return &DeliveryResponse{
				Message:    "✅ Pago en Efectivo registrado. Estamos procesando tu orden... ¡Llegará pronto!",
				NextState:  StateDeliveryPlaced,
				NewSession: session,
			}
		}
		if textNorm == "transfer" || strings.Contains(textNorm, "transferencia") {
			session.State = StateDeliveryPlaced
			return &DeliveryResponse{
				Message:    "✅ Pago por Transferencia registrado. Por favor envía el comprobante por aquí. Estamos procesando tu orden... ¡Llegará pronto!",
				NextState:  StateDeliveryPlaced,
				NewSession: session,
			}
		}
		return &DeliveryResponse{
			Message:    "Por favor, indica tu método de pago.",
			NextState:  StateDeliveryPayment,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "cash", Title: "💵 Efectivo"},
				{ID: "transfer", Title: "📲 Transferencia"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	default:
		return &DeliveryResponse{
			Message:    "Lo siento, hubo un error en el flujo. ¿Podrías decirme tu dirección de nuevo?",
			NextState:  StateDeliveryAwaitingAddress,
			NewSession: &DeliverySession{State: StateDeliveryAwaitingAddress},
		}
	}
}

func pickProductWithAI(input string, products []models.Product, history []models.AIMessage, apiKey string) (string, int, bool) {
	if apiKey == "" {
		return "", 0, false
	}

	productList := ""
	for _, p := range products {
		productList += fmt.Sprintf("- %s ($%.0f)\n", p.Name, p.Price)
	}

	prompt := fmt.Sprintf(`Identifica el producto y la cantidad que el usuario quiere pedir de la siguiente lista:
%s

Responde únicamente en formato JSON: {"product": "Nombre Exacto", "quantity": 1, "found": true}
Si no encuentras el producto, responde {"found": false}`, productList)

	messages := []map[string]string{
		{"role": "system", "content": prompt},
	}
	// Añadir historial reciente
	for _, h := range history {
		role := h.Role
		if role == "assistant" { role = "assistant" }
		messages = append(messages, map[string]string{"role": role, "content": h.Content})
	}
	messages = append(messages, map[string]string{"role": "user", "content": input})

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.3-70b-versatile",
		"messages": messages,
		"response_format": map[string]string{"type": "json_object"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 { return "", 0, false }
	defer resp.Body.Close()

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&res)

	var data struct {
		Product  string `json:"product"`
		Quantity int    `json:"quantity"`
		Found    bool   `json:"found"`
	}
	json.Unmarshal([]byte(res.Choices[0].Message.Content), &data)

	return data.Product, data.Quantity, data.Found
}

func getUpsellSuggestion(selected models.Product, products []models.Product, _ []models.AIMessage, apiKey string) *models.Product {
	if apiKey == "" { return nil }

	// Buscar productos que NO sean el actual y que su precio sea razonable para un upsell (ej. < $15.000)
	var candidates []models.Product
	for _, p := range products {
		if p.ID != selected.ID && p.Available && p.Price < 15000 {
			candidates = append(candidates, p)
		}
	}

	if len(candidates) == 0 { return nil }

	// Usar IA para elegir el mejor candidato
	candidateList := ""
	for i, c := range candidates {
		candidateList += fmt.Sprintf("%d. %s ($%.0f)\n", i, c.Name, c.Price)
	}

	prompt := fmt.Sprintf(`El usuario pidió: %s. 
De esta lista, ¿cuál es el MEJOR acompañamiento o "agrandado"? 
Responde SOLO con el número del índice.
%s`, selected.Name, candidateList)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.1-8b-instant",
		"messages": []map[string]string{{"role": "system", "content": prompt}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 { return &candidates[0] }
	defer resp.Body.Close()

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&res)

	if len(res.Choices) == 0 { return &candidates[0] }
	idx := 0
	fmt.Sscanf(strings.TrimSpace(res.Choices[0].Message.Content), "%d", &idx)
	if idx < 0 || idx >= len(candidates) { idx = 0 }

	return &candidates[idx]
}

func renderCart(cart []models.OrderItem) string {
	var items []string
	for _, item := range cart {
		items = append(items, fmt.Sprintf("%d x %s ($%.0f)", item.Quantity, item.Name, item.Subtotal))
	}
	return strings.Join(items, ", ")
}

func resolveAPIKey() string {
	if v := os.Getenv("AGENT_DELIVERY_KEY"); v != "" { return v }
	return os.Getenv("GROQ_API_KEY")
}

func isPositive(msg string) bool {
	s := strings.ToLower(strings.TrimSpace(msg))
	positiveExact := []string{"si", "sí", "ok", "vale", "claro", "acepto", "perfecto", "listo", "dale", "upsell_yes", "upsell yes"}
	for _, w := range positiveExact {
		if s == w {
			return true
		}
	}
	// Verificar como palabra completa al inicio o fin
	return strings.HasPrefix(s, "si ") || strings.HasPrefix(s, "sí ") ||
		strings.HasSuffix(s, " si") || strings.HasSuffix(s, " sí")
}

func isNegative(msg string) bool {
	s := strings.ToLower(strings.TrimSpace(msg))
	negativeExact := []string{"no", "nel", "nop", "nope", "upsell_no", "upsell no"}
	for _, w := range negativeExact {
		if s == w {
			return true
		}
	}
	return strings.HasPrefix(s, "no ") || strings.HasSuffix(s, " no") ||
		strings.Contains(s, "cancelar") || strings.Contains(s, "no gracias")
}
