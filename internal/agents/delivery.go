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

	// ════════════════════════════════════════════════════════════
	// GUARDS GLOBALES — se aplican en CUALQUIER estado del flujo
	// ════════════════════════════════════════════════════════════

	// 1. Botón de otro flujo llegó al agente delivery (ej: menu_recoger, menu_4, menu_update…)
	//    → El agente redirige al menú principal con explicación, sin abandonar el estado actual
	if strings.HasPrefix(textNorm, "menu_") && textNorm != "menu_1" {
		return &DeliveryResponse{
			Message: "Parece que deseas explorar otra opción 🧭\n\nPara cambiar, primero cancela tu pedido actual con el botón de abajo, o termina tu pedido de domicilio y luego selecciona lo que necesites desde el *Menú Principal*.",
			NextState:  session.State,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				{ID: "menu_1", Title: "🍕 Ver Carta"},
			},
		}
	}

	// 2. Botón viejo de confirmación de dirección en estado incorrecto → recordatorio suave
	if (textNorm == "use_reg_addr" || textNorm == "use_new_addr") && session.State != StateDeliveryConfirmingRegisteredAddress {
		addr := session.Address
		if addr == "" {
			addr = "no registrada"
		}
		return &DeliveryResponse{
			Message:    fmt.Sprintf("Ya tenemos registrada tu dirección: *%s*\n\n¿Qué producto deseas agregar?", addr),
			NextState:  StateDeliveryAwaitingProduct,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}
	}

	// ════════════════════════════════════════════════════════════
	// MÁQUINA DE ESTADOS
	// ════════════════════════════════════════════════════════════
	switch session.State {
	case "", StateDeliveryIdle:
		if session.Address != "" {
			session.State = StateDeliveryConfirmingRegisteredAddress
			return &DeliveryResponse{
				Message:   fmt.Sprintf("Veo que tienes una dirección registrada: *%s*. ¿Deseas usarla o prefieres una nueva?", session.Address),
				NextState: StateDeliveryConfirmingRegisteredAddress,
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
			Message:   "¡Claro! ¿A qué dirección enviamos tu pedido?",
			NextState: StateDeliveryAwaitingAddress,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryConfirmingRegisteredAddress:
		if textNorm == "use_reg_addr" {
			session.State = StateDeliveryAwaitingProduct
			return &DeliveryResponse{
				Message:   fmt.Sprintf("Perfecto, enviaremos a: *%s*\n\n🍕 ¿Qué deseas pedir? Puedes escribir el nombre, pedir una recomendación o ver la carta:", session.Address),
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		// use_new_addr o cualquier otra respuesta
		session.State = StateDeliveryAwaitingAddress
		return &DeliveryResponse{
			Message:   "Entendido. ¿A qué nueva dirección enviamos tu pedido?",
			NextState: StateDeliveryAwaitingAddress,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryAwaitingAddress:
		if textNorm == "" || strings.HasPrefix(textNorm, "use_") {
			return &DeliveryResponse{
				Message:   "Por favor escribe la dirección de entrega:",
				NextState: StateDeliveryAwaitingAddress,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		session.Address = userInput
		session.State = StateDeliveryAwaitingProduct
		return &DeliveryResponse{
			Message:   fmt.Sprintf("✅ Dirección guardada: *%s*\n\n🍕 ¿Qué deseas pedir? Puedes escribir el nombre, pedir recomendación o ver la carta:", session.Address),
			NextState: StateDeliveryAwaitingProduct,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "menu_1", Title: "🍕 Ver Carta"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryAwaitingProduct:
		// menu_1: carta inline sin salir del agente
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
			sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\n✍️ Escribe el nombre del producto que quieres pedir:")
			return &DeliveryResponse{
				Message:   sb.String(),
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				},
			}
		}

		// Recomendación con IA que conoce el catálogo
		if IsRecommendationQuery(userInput) {
			catalogStr := buildDeliveryCatalogPrompt(products, []*models.CoverageZone{})
			recommendation := AskAIRecommendation(userInput, catalogStr, "nuestra pizzería")
			if recommendation == "" {
				recommendation = buildQuickRecommendation(products)
			}
			return &DeliveryResponse{
				Message:   recommendation,
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta completa"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}

		// Intentar identificar producto con IA
		recentHistory := history
		if len(recentHistory) > 6 {
			recentHistory = recentHistory[len(recentHistory)-6:]
		}
		productName, quantity, found := pickProductWithAI(userInput, products, recentHistory, apiKey)

		if !found {
			// Pregunta general o producto no encontrado → IA conversacional con catálogo
			catalogCtx := buildDeliveryCatalogPrompt(products, []*models.CoverageZone{})
			aiMsg := askMenuConversationAI(userInput, catalogCtx, session)
			if aiMsg == "" {
				aiMsg = "No encontré ese producto. ¿Podrías decirme el nombre exacto? Puedes ver la carta con el botón de abajo."
			}
			return &DeliveryResponse{
				Message:   aiMsg,
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}

		// Buscar el producto real
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
		session.State = StateDeliveryUpsell
		suggested := getUpsellSuggestion(selected, products, history, apiKey)
		session.SuggestedItem = suggested

		upsellMsg := fmt.Sprintf("¡Excelente! Agregué *%dx %s* ($%.0f) a tu pedido. ", quantity, selected.Name, item.Subtotal)
		if suggested != nil {
			upsellMsg += fmt.Sprintf("¿Te gustaría acompañarlo con *%s* por solo *$%.0f* adicionales?", suggested.Name, suggested.Price)
		} else {
			upsellMsg += "¿Deseas agregar algo más?"
		}

		return &DeliveryResponse{
			Message:   upsellMsg,
			NextState: StateDeliveryUpsell,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "upsell_yes", Title: "✅ ¡Sí, agregar!"},
				{ID: "upsell_no", Title: "👎 No, continuar"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryUpsell:
		upsellAccepted := textNorm == "upsell_yes" || isPositive(userInput)
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
			session.SuggestedItem = nil
		}

		session.State = StateDeliveryConfirmingOrder
		return &DeliveryResponse{
			Message: fmt.Sprintf(
				"📝 *Resumen de tu pedido*\n━━━━━━━━━━━━━━━━\n%s\n─────────────────────\n💰 *Total: $%.0f*\n📍 Dirección: *%s*\n\n¿Confirmas?",
				renderCart(session.Cart), session.Total, session.Address,
			),
			NextState:  StateDeliveryConfirmingOrder,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_ok", Title: "✅ Confirmar pedido"},
				{ID: "confirm_add", Title: "➕ Agregar más"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryConfirmingOrder:
		if textNorm == "confirm_add" {
			session.State = StateDeliveryAwaitingProduct
			return &DeliveryResponse{
				Message:   "¿Qué más deseas agregar?",
				NextState: StateDeliveryAwaitingProduct,
				NewSession: session,
				Buttons: []models.InteractiveButton{
					{ID: "menu_1", Title: "🍕 Ver Carta"},
					{ID: "confirm_cancel", Title: "❌ Cancelar"},
				},
			}
		}
		if textNorm == "confirm_ok" || isPositive(userInput) {
			session.State = StateDeliveryPayment
			return &DeliveryResponse{
				Message:   "💳 ¿Cómo deseas pagar?",
				NextState: StateDeliveryPayment,
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
				Message:   "Pedido cancelado. ¡Espero verte pronto! 🍕",
				NextState: StateDeliveryIdle,
				NewSession: &DeliverySession{State: StateDeliveryIdle},
			}
		}
		return &DeliveryResponse{
			Message:   "Por favor confirma tu pedido:",
			NextState: StateDeliveryConfirmingOrder,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "confirm_ok", Title: "✅ Confirmar"},
				{ID: "confirm_add", Title: "➕ Agregar más"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryPayment:
		if textNorm == "cash" || strings.Contains(textNorm, "efectivo") {
			session.State = StateDeliveryPlaced
			return &DeliveryResponse{
				Message:   "✅ *¡Pedido confirmado!*\n\nPago en Efectivo registrado. Tu pedido está en camino 🛵\n\n¡Gracias por elegirnos!",
				NextState: StateDeliveryPlaced,
				NewSession: session,
			}
		}
		if textNorm == "transfer" || strings.Contains(textNorm, "transferencia") {
			session.State = StateDeliveryPlaced
			return &DeliveryResponse{
				Message:   "✅ *¡Pedido confirmado!*\n\nPago por Transferencia registrado. Por favor envía el comprobante. Tu pedido está en camino 🛵\n\n¡Gracias por elegirnos!",
				NextState: StateDeliveryPlaced,
				NewSession: session,
			}
		}
		return &DeliveryResponse{
			Message:   "Por favor indica tu método de pago:",
			NextState: StateDeliveryPayment,
			NewSession: session,
			Buttons: []models.InteractiveButton{
				{ID: "cash", Title: "💵 Efectivo"},
				{ID: "transfer", Title: "📲 Transferencia"},
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	default:
		return &DeliveryResponse{
			Message:   "Hubo un error en el flujo. ¿A qué dirección enviamos tu pedido?",
			NextState: StateDeliveryAwaitingAddress,
			NewSession: &DeliverySession{State: StateDeliveryAwaitingAddress},
		}
	}
}

// buildDeliveryCatalogPrompt construye el contexto de catálogo para la IA de domicilios
func buildDeliveryCatalogPrompt(products []models.Product, zones []*models.CoverageZone) string {
	var sb strings.Builder
	if len(products) > 0 {
		sb.WriteString("PRODUCTOS DISPONIBLES:\n")
		for _, p := range products {
			sb.WriteString(fmt.Sprintf("• %s: $%.0f", p.Name, p.Price))
			if p.Description != nil && *p.Description != "" {
				sb.WriteString(fmt.Sprintf(" — %s", *p.Description))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// askMenuConversationAI responde preguntas generales sobre el menú dentro del flujo de delivery
func askMenuConversationAI(userInput, catalogCtx string, session *DeliverySession) string {
	systemPrompt := fmt.Sprintf(`Eres el asistente de pedidos a domicilio. El cliente está en proceso de hacer un pedido.
Dirección actual: %s
Carrito actual: %s

Puedes:
- Responder preguntas sobre productos, ingredientes, precios
- Recomendar productos de la carta
- Ayudar a elegir qué pedir

Si el cliente quiere cambiar a "recoger en tienda" u otro servicio distinto, dile amablemente:
"Para eso, primero cancela este pedido o complétalo, y luego selecciona la opción desde el Menú Principal."

Sé conciso. Máximo 2-3 oraciones.

%s`, session.Address, renderCart(session.Cart), catalogCtx)

	return callGroqAI(systemPrompt, userInput, 200)
}

// buildQuickRecommendation hace recomendación rápida sin IA cuando no hay clave API
func buildQuickRecommendation(products []models.Product) string {
	for _, p := range products {
		if strings.Contains(strings.ToLower(p.Name), "pizza") {
			return fmt.Sprintf("¡Te recomiendo nuestra *%s* ($%.0f)! Es una de las favoritas 🍕 ¿La incluyo en tu pedido?", p.Name, p.Price)
		}
	}
	if len(products) > 0 {
		return fmt.Sprintf("¡Te recomiendo *%s* ($%.0f)! Es excelente opción.", products[0].Name, products[0].Price)
	}
	return "¡Todo está delicioso! Mira la carta y dime qué te apetece."
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
