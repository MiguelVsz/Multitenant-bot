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
	StateDeliveryPlaced          = "ORDER_PLACED"
)

type DeliverySession struct {
	State       string             `json:"state"`
	Address     string             `json:"address,omitempty"`
	Cart        []models.OrderItem `json:"cart"`
	PhoneNumber string             `json:"phone_number"`
	CustomerID  string             `json:"customer_id"`
	Total       float64            `json:"total"`
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
		session.State = StateDeliveryAwaitingAddress
		return &DeliveryResponse{
			Message:    "¡Claro! ¿A qué dirección enviamos tu pedido?",
			NextState:  StateDeliveryAwaitingAddress,
			NewSession: session,
		}

	case StateDeliveryAwaitingAddress:
		session.Address = userInput
		session.State = StateDeliveryAwaitingProduct
		return &DeliveryResponse{
			Message:    fmt.Sprintf("Dirección registrada: %s. ¿Qué te gustaría pedir? (Puedes elegir algo de nuestra carta)", session.Address),
			NextState:  StateDeliveryAwaitingProduct,
			NewSession: session,
		}

	case StateDeliveryAwaitingProduct:
		// IA para identificar producto
		productName, quantity, found := pickProductWithAI(userInput, products, history, apiKey)
		if !found {
			return &DeliveryResponse{
				Message:    "No logré identificar qué producto deseas. ¿Me lo podrías repetir, por favor?",
				NextState:  StateDeliveryAwaitingProduct,
				NewSession: session,
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
		upsellSuggestion := getUpsellSuggestion(selected, products, history, apiKey)
		
		return &DeliveryResponse{
			Message:    fmt.Sprintf("¡Excelente! He añadido %d x %s a tu pedido. %s", quantity, selected.Name, upsellSuggestion),
			NextState:  StateDeliveryUpsell,
			NewSession: session,
		}

	case StateDeliveryUpsell:
		if isPositive(userInput) {
			// Encontrar qué sugirió la IA (esto es un poco complejo sin estado extra, pero por ahora simulamos que acepta el mejor complemento)
			// Para algo real, la IA de upsell debería devolver el productoID sugerido.
			// Por ahora, asumimos que el usuario acepta un 'complemento' genérico si dice sí.
			// TODO: Mejorar lógica de upsell para identificar el producto aceptado.
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
					{ID: "pay_cash", Title: "💵 Efectivo"},
					{ID: "pay_transfer", Title: "📲 Transferencia"},
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
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			},
		}

	case StateDeliveryPayment:
		if textNorm == "pay_cash" || strings.Contains(textNorm, "efectivo") {
			session.State = StateDeliveryPlaced
			return &DeliveryResponse{
				Message:    "✅ Pago en Efectivo registrado. Estamos procesando tu orden... ¡Llegará pronto!",
				NextState:  StateDeliveryPlaced,
				NewSession: session,
			}
		}
		if textNorm == "pay_transfer" || strings.Contains(textNorm, "transferencia") {
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
				{ID: "pay_cash", Title: "💵 Efectivo"},
				{ID: "pay_transfer", Title: "📲 Transferencia"},
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

func getUpsellSuggestion(product models.Product, _ []models.Product, _ []models.AIMessage, apiKey string) string {
	if apiKey == "" { return "¿Deseas agregar alguna bebida?" }

	prompt := fmt.Sprintf(`El usuario ha pedido: %s. 
Basado en esto, sugiere un "agrandado" o complemento ideal (ej. papas mas grandes, doble carne, bebida, postre).
Sé persuasivo pero breve. No uses más de 20 palabras.`, product.Name)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.3-70b-versatile",
		"messages": []map[string]string{{"role": "system", "content": prompt}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 { return "¿Deseas algo más para acompañar?" }
	defer resp.Body.Close()

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&res)

	return res.Choices[0].Message.Content
}

func renderCart(cart []models.OrderItem) string {
	var items []string
	for _, item := range cart {
		items = append(items, fmt.Sprintf("%d x %s", item.Quantity, item.Name))
	}
	return strings.Join(items, ", ")
}

func resolveAPIKey() string {
	if v := os.Getenv("AGENT_DELIVERY_KEY"); v != "" { return v }
	return os.Getenv("GROQ_API_KEY")
}

func isPositive(msg string) bool {
	s := strings.ToLower(msg)
	return strings.Contains(s, "si") || strings.Contains(s, "ok") || strings.Contains(s, "vale") || strings.Contains(s, "acepto")
}

func isNegative(msg string) bool {
	s := strings.ToLower(msg)
	return strings.Contains(s, "no") || strings.Contains(s, "cancelar")
}
