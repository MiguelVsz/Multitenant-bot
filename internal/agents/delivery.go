package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Constantes de Estado
const (
	StateIdle            = "IDLE"
	StateGreeting        = "GREETING"
	StateAwaitingAddress = "AWAITING_ADDRESS"
	StateAwaitingProduct = "AWAITING_PRODUCT"
	StateConfirmingOrder = "CONFIRMING_ORDER"
	StateOrderPlaced     = "ORDER_PLACED"
	StateCancelled       = "CANCELLED"
	StatePayment         = "PAYMENT"
	StateOrderCreate     = "ORDER_CREATE"
	StateOrderConfirm    = "ORDER_CONFIRM"
)

type DeliverySession struct {
	State       string            `json:"state"`
	Address     string            `json:"address,omitempty"`
	Product     string            `json:"product,omitempty"`
	PhoneNumber string            `json:"phone_number"`
	UserData    map[string]string `json:"user_data"`
	Cart        []string          `json:"cart"`
}

type AgentResponse struct {
	Messages    []string         `json:"messages"`
	NextState   string           `json:"next_state"`
	SessionData *DeliverySession `json:"session_data"`
}

// HandleDelivery maneja la lógica de la conversación de domicilios
func HandleDelivery(session *DeliverySession, userMessage string) *AgentResponse {
	if session == nil {
		session = &DeliverySession{State: StateIdle}
	}

	switch session.State {

	case "", StateIdle:
		session.State = StateAwaitingAddress
		return &AgentResponse{
			Messages:    []string{"¡Claro! Con gusto te ayudo con tu domicilio. ¿A qué dirección lo enviamos?"},
			NextState:   StateAwaitingAddress,
			SessionData: session,
		}

	case StateAwaitingAddress:
		session.Address = userMessage
		session.State = StateAwaitingProduct
		return &AgentResponse{
			Messages:    []string{fmt.Sprintf("Dirección recibida: %s. ¿Qué producto deseas pedir? (Escribe 'carta' para ver opciones)", session.Address)},
			NextState:   StateAwaitingProduct,
			SessionData: session,
		}

	case StateAwaitingProduct:
		menu := getMenuFromAPI()
		apiKey, _ := resolveRouterKey()

		// Si el usuario solo quiere ver el menú, se lo mostramos y nos quedamos aquí
		if strings.ToLower(strings.TrimSpace(userMessage)) == "carta" {
			return &AgentResponse{
				Messages:    []string{"Mira nuestro menú:\n" + menu + "\n\n¿Qué te gustaría pedir?"},
				NextState:   StateAwaitingProduct,
				SessionData: session,
			}
		}

		// Intentamos IA
		systemSales := fmt.Sprintf(`Eres el asistente virtual del restaurante Burgers & Co.
		Ayudas al cliente a elegir del menú usando solo estos productos: %s
		Prioriza recomendar combos y promociones. Sé amable, claro y breve.
		Nunca inventes productos fuera de la lista.
		Si el cliente no sabe qué pedir, hazle una pregunta corta sobre su preferencia.
		Formato de respuesta: Nombre del producto — descripción corta — precio.
		Ejemplo: Combo Clásico — Hamburguesa + papas + bebida — $18.000`, menu)
		aiReply := callGroqDirect(systemSales, userMessage, apiKey)

		// GUARDAMOS EL PRODUCTO (ya sea lo que dijo la IA o el texto bruto del usuario)
		if aiReply != "" {
			session.Product = aiReply
		} else {
			session.Product = userMessage // Plan B: guardamos el texto tal cual
		}

		// AVANZAMOS DE ESTADO SIEMPRE (Aquí se rompe el bucle)
		session.State = StateConfirmingOrder

		replyText := aiReply
		if replyText == "" {
			replyText = fmt.Sprintf("¡Excelente elección! Anotado: %s.", userMessage)
		}

		return &AgentResponse{
			Messages:    []string{replyText, "¿Confirmas tu pedido? (Si/No)"},
			NextState:   StateConfirmingOrder,
			SessionData: session,
		}

	case StateConfirmingOrder:
		if isPositive(userMessage) {
			session.State = StatePayment
			return &AgentResponse{
				Messages:    []string{"¡Perfecto! ¿Cómo deseas pagar? Tenemos: **Efectivo** o **Transferencia**."},
				NextState:   StatePayment,
				SessionData: session,
			}
		}

		if isNegative(userMessage) {
			session.State = StateCancelled
			return &AgentResponse{
				Messages:    []string{"Pedido cancelado. ¿Te puedo ayudar con algo más?"},
				NextState:   StateCancelled,
				SessionData: &DeliverySession{State: StateIdle},
			}
		}
		return &AgentResponse{
			Messages:    []string{"Por favor, confirma con 'si' o 'no'."},
			NextState:   StateConfirmingOrder,
			SessionData: session,
		}

	case StatePayment:
		// 1. Validamos lo que el usuario escribió después de la pregunta de pago
		metodo := strings.ToLower(userMessage)
		if strings.Contains(metodo, "efectivo") || strings.Contains(metodo, "transferencia") {

			// 2. Aquí es donde realmente "Crearías la orden" (StateOrderCreate)
			// Por ahora lo simulamos avanzando al éxito
			session.State = StateOrderPlaced

			return &AgentResponse{
				Messages: []string{
					fmt.Sprintf("✅ Pago por %s registrado.", userMessage),
					"Estamos creando tu orden en el sistema... ⏳",
					"¡Listo! Tu pedido #1234 ha sido confirmado. Llegará en 30 min.",
				},
				NextState:   StateOrderPlaced,
				SessionData: session,
			}
		}

		// Si el usuario escribe otra cosa que no sea el método de pago
		return &AgentResponse{
			Messages:    []string{"Por favor, indica si prefieres **Efectivo** o **Transferencia** para continuar."},
			NextState:   StatePayment,
			SessionData: session,
		}

	default:
		session.State = StateAwaitingAddress
		return &AgentResponse{
			Messages:    []string{"Si quieres hacer un pedido, dime la dirección de entrega."},
			NextState:   StateAwaitingAddress,
			SessionData: session,
		}
	}
}

// --- FUNCIONES DE APOYO ---

func callGroqDirect(systemPrompt string, userPrompt string, apiKey string) string {
	if apiKey == "" {
		return ""
	}

	reqBody, _ := json.Marshal(routerGroqRequest{
		Model: "llama-3.3-70b-versatile",
		Messages: []routerMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("❌ ERROR DE RED:", err) // ESTO ES CLAVE
		return ""
	}
	defer resp.Body.Close()

	// Si Groq nos da un error (ej: 401 Unauthorized o 429 Rate Limit)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ ERROR DE API GROQ (Status %d): %s\n", resp.StatusCode, string(body))
		return ""
	}

	var parsed routerGroqResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		fmt.Println("❌ ERROR DECODE:", err)
		return ""
	}

	if len(parsed.Choices) > 0 {
		return strings.TrimSpace(parsed.Choices[0].Message.Content)
	}

	// Si llegamos aquí, la IA respondió algo vacío o hubo un error de API
	fmt.Printf("Groq devolvió 0 opciones. Status: %s\n", resp.Status)
	return ""
}

func MainHandler(phoneNumber string, userInput string) string {
	session := getSessionFromRedis(phoneNumber)

	// Si el bot está libre, el Router decide qué hacer
	if session.State == StateIdle || session.State == "" {
		route := HandleRouter(userInput)
		if route.Intent == "delivery" || route.Intent == "carta" {
			session.State = StateAwaitingAddress
			saveSessionToRedis(phoneNumber, session)
			return "¡Claro! ¿A qué dirección enviamos tu pedido?"
		}
		return route.Message
	}

	// Si ya hay proceso iniciado, seguimos en Delivery
	agentRes := HandleDelivery(session, userInput)
	saveSessionToRedis(phoneNumber, agentRes.SessionData)
	return strings.Join(agentRes.Messages, "\n")
}

func isPositive(msg string) bool {
	s := strings.ToLower(msg)
	return strings.Contains(s, "si") || strings.Contains(s, "ok") || strings.Contains(s, "vale")
}

func isNegative(msg string) bool {
	s := strings.ToLower(msg)
	return strings.Contains(s, "no") || strings.Contains(s, "cancelar")
}

func getMenuFromAPI() string {
	return "1. Combo Clásico ($18.000), 2. Combo Parrillero ($25.000), 3. Papas Fritas ($8.000)"
}

// PERSISTENCIA TEMPORAL (Simulando Redis)
var tempStorage = make(map[string]*DeliverySession)

func getSessionFromRedis(phoneNumber string) *DeliverySession {
	if s, ok := tempStorage[phoneNumber]; ok {
		return s
	}
	return &DeliverySession{PhoneNumber: phoneNumber, State: StateIdle}
}

func saveSessionToRedis(phoneNumber string, session *DeliverySession) {
	tempStorage[phoneNumber] = session
}
