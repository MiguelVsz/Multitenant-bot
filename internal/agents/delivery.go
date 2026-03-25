package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ctx = context.Background()
	rdb *redis.Client
)

const (
	StateIdle            = "IDLE"
	StateGreeting        = "GREETING"
	StateAwaitingAddress = "AWAITING_ADDRESS"
	StateAwaitingProduct = "AWAITING_PRODUCT"
	StateConfirmingOrder = "CONFIRMING_ORDER"
	StateOrderPlaced     = "ORDER_PLACED"
	StateCancelled       = "CANCELLED"
)

type DeliverySession struct {
	State       string            `json:"state"`
	Address     string            `json:"address,omitempty"`
	Product     string            `json:"product,omitempty"`
	LastAIReply string            `json:"last_ai_reply,omitempty"`
	PhoneNumber string            `json:"phone_number"`
	UserData    map[string]string `json:"user_data"`
	Cart        []string          `json:"cart"`
}

type AgentResponse struct {
	Messages    []string         `json:"messages"`
	NextState   string           `json:"next_state"`
	SessionData *DeliverySession `json:"session_data"`
}

func HandleDelivery(session *DeliverySession, userMessage string) *AgentResponse {
	session = ensureDeliverySession(session)
	userMessage = strings.TrimSpace(userMessage)

	switch session.State {
	case "", StateIdle:
		session.State = StateAwaitingAddress
		return &AgentResponse{
			Messages:    []string{"Hola, a que direccion te enviamos tu pedido?"},
			NextState:   StateAwaitingAddress,
			SessionData: session,
		}

	case StateAwaitingAddress:
		session.Address = userMessage
		session.UserData["address"] = userMessage
		session.State = StateAwaitingProduct
		return &AgentResponse{
			Messages:    []string{fmt.Sprintf("Direccion recibida: %s. Que producto deseas pedir?", session.Address)},
			NextState:   StateAwaitingProduct,
			SessionData: session,
		}

	case StateAwaitingProduct:
		menu := getMenuFromAPI()
		apiKey, _ := resolveRouterKey()

		aiReply := callAiForMenu("Burgers & Co", userMessage, menu)
		if strings.TrimSpace(aiReply) == "" {
			aiReply = "Perfecto. Indica los productos que deseas y luego confirma escribiendo 'si'."
		}

		detectedItems := updateCartWithAI(userMessage, menu)
		if len(detectedItems) == 0 {
			detectedItems = extractItemsFallback(userMessage, menu)
		}

		if len(detectedItems) > 0 {
			session.Cart = append(session.Cart, detectedItems...)
			session.Product = strings.Join(session.Cart, ", ")
		} else {
			session.Product = userMessage
		}

		// Si hay API key, intentamos una respuesta mas comercial.
		if apiKey != "" {
			systemSales := fmt.Sprintf(
				"Eres el asistente de Burgers & Co. Menu: %s. Responde amablemente, confirma lo que el cliente quiere y pide confirmar escribiendo 'si'.",
				menu,
			)
			if enriched := callGroqDirect(systemSales, userMessage, apiKey); strings.TrimSpace(enriched) != "" {
				aiReply = enriched
			}
		}

		session.LastAIReply = aiReply
		session.State = StateConfirmingOrder

		return &AgentResponse{
			Messages:    []string{aiReply, "Confirmas tu pedido? (si/no)"},
			NextState:   StateConfirmingOrder,
			SessionData: session,
		}

	case StateConfirmingOrder:
		if isPositive(userMessage) {
			session.State = StateOrderPlaced
			return &AgentResponse{
				Messages:    []string{fmt.Sprintf("Excelente. Tu pedido de %s ha sido enviado. Llegara en 30 min.", session.Product)},
				NextState:   StateOrderPlaced,
				SessionData: session,
			}
		}

		if isNegative(userMessage) {
			cancelled := ensureDeliverySession(&DeliverySession{
				State:       StateCancelled,
				PhoneNumber: session.PhoneNumber,
			})
			return &AgentResponse{
				Messages:    []string{"Pedido cancelado. Si quieres pedir otra cosa, dime la direccion de entrega."},
				NextState:   StateCancelled,
				SessionData: cancelled,
			}
		}

		return &AgentResponse{
			Messages:    []string{"Confirmas el pedido? Responde si o no."},
			NextState:   StateConfirmingOrder,
			SessionData: session,
		}

	case StateOrderPlaced, StateCancelled:
		reset := ensureDeliverySession(&DeliverySession{
			State:       StateAwaitingAddress,
			PhoneNumber: session.PhoneNumber,
		})
		return &AgentResponse{
			Messages:    []string{"Si quieres hacer otro pedido, dime la direccion de entrega."},
			NextState:   StateAwaitingAddress,
			SessionData: reset,
		}

	default:
		session.State = StateGreeting
		return &AgentResponse{
			Messages:    []string{"No entendi tu mensaje. Podrias repetirlo?"},
			NextState:   StateGreeting,
			SessionData: session,
		}
	}
}

func isPositive(msg string) bool {
	s := strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(s, "si") || strings.Contains(s, "ok") || strings.Contains(s, "confirmo")
}

func isNegative(msg string) bool {
	s := strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(s, "no") || strings.Contains(s, "cancelar")
}

func callGroqDirect(systemPrompt string, userPrompt string, apiKey string) string {
	if strings.TrimSpace(apiKey) == "" {
		return ""
	}

	reqBody, err := json.Marshal(routerGroqRequest{
		Model: "llama-3.3-70b-versatile",
		Messages: []routerMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return ""
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		reqCtx,
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return ""
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var parsed routerGroqResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ""
	}

	if len(parsed.Choices) > 0 {
		return strings.TrimSpace(parsed.Choices[0].Message.Content)
	}

	return ""
}

func updateCartWithAI(userInput string, menuData string) []string {
	apiKey, _ := resolveRouterKey()
	prompt := fmt.Sprintf(
		"Del siguiente mensaje: '%s', extrae los nombres de productos que el usuario quiere pedir basados en este menu: %s. Responde SOLO con los nombres separados por comas, nada mas.",
		userInput,
		menuData,
	)

	aiResponse := callGroqDirect("Eres un extractor de productos.", prompt, apiKey)
	if strings.TrimSpace(aiResponse) == "" {
		return []string{}
	}

	items := strings.Split(aiResponse, ",")
	return normalizeItems(items)
}

func MainHandler(phoneNumber string, userInput string) string {
	session := getSessionFromRedis(phoneNumber)
	agentRes := HandleDelivery(session, userInput)
	saveSessionToRedis(phoneNumber, agentRes.SessionData)
	return strings.Join(agentRes.Messages, "\n")
}

func callAiForMenu(businessName string, userInput string, menuData string) string {
	apiKey, _ := resolveRouterKey()
	if strings.TrimSpace(apiKey) == "" {
		items := extractItemsFallback(userInput, menuData)
		if len(items) == 0 {
			return fmt.Sprintf("Menu de %s: %s", businessName, menuData)
		}
		return fmt.Sprintf("Perfecto, detecte estos productos: %s. Si todo esta bien, responde 'si' para confirmar.", strings.Join(items, ", "))
	}

	systemPrompt := fmt.Sprintf(`Eres el asistente de %s.
Menu oficial: %s

Reglas:
- Formato: Nombre -> Descripcion -> Precio.
- Si el cliente elige, confirma los productos y dile: "Escribe 'si' para confirmar tu pedido".
- Nunca inventes productos.`, businessName, menuData)

	return callGroqDirect(systemPrompt, userInput, apiKey)
}

func getMenuFromAPI() string {
	return "1. Combo Clasico: Hamburguesa + Papas + Gaseosa - $18.000, " +
		"2. Combo Parrillero: Carne Angus + Queso + Chorizo - $25.000, " +
		"3. Papas Fritas - $8.000"
}

func getSessionFromRedis(phoneNumber string) *DeliverySession {
	if rdb == nil {
		return ensureDeliverySession(&DeliverySession{
			PhoneNumber: phoneNumber,
			State:       StateIdle,
		})
	}

	val, err := rdb.Get(ctx, "session:"+phoneNumber).Result()
	if err != nil {
		return ensureDeliverySession(&DeliverySession{
			PhoneNumber: phoneNumber,
			State:       StateIdle,
		})
	}

	var session DeliverySession
	if err := json.Unmarshal([]byte(val), &session); err != nil {
		fmt.Println("error deserializando sesion:", err)
		return ensureDeliverySession(&DeliverySession{
			PhoneNumber: phoneNumber,
			State:       StateIdle,
		})
	}

	if session.PhoneNumber == "" {
		session.PhoneNumber = phoneNumber
	}

	return ensureDeliverySession(&session)
}

func saveSessionToRedis(phoneNumber string, session *DeliverySession) {
	if session == nil || rdb == nil {
		return
	}

	session = ensureDeliverySession(session)
	if session.PhoneNumber == "" {
		session.PhoneNumber = phoneNumber
	}

	data, err := json.Marshal(session)
	if err != nil {
		fmt.Println("error serializando sesion:", err)
		return
	}

	if err := rdb.Set(ctx, "session:"+phoneNumber, data, 24*time.Hour).Err(); err != nil {
		fmt.Println("error guardando en redis:", err)
	}
}

func ensureDeliverySession(session *DeliverySession) *DeliverySession {
	if session == nil {
		session = &DeliverySession{}
	}
	if session.State == "" {
		session.State = StateIdle
	}
	if session.UserData == nil {
		session.UserData = map[string]string{}
	}
	if session.Cart == nil {
		session.Cart = []string{}
	}
	return session
}

func extractItemsFallback(userInput string, _ string) []string {
	lower := strings.ToLower(userInput)
	var items []string

	if strings.Contains(lower, "combo clasico") {
		items = append(items, "Combo Clasico")
	}
	if strings.Contains(lower, "combo parrillero") {
		items = append(items, "Combo Parrillero")
	}
	if strings.Contains(lower, "papas") {
		items = append(items, "Papas Fritas")
	}
	if strings.Contains(lower, "hamburguesa") && !containsItem(items, "Combo Clasico") && !containsItem(items, "Combo Parrillero") {
		items = append(items, "Hamburguesa")
	}
	if strings.Contains(lower, "gaseosa") {
		items = append(items, "Gaseosa")
	}

	return normalizeItems(items)
}

func normalizeItems(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	clean := make([]string, 0, len(items))

	for _, item := range items {
		item = strings.TrimSpace(item)
		item = strings.Trim(item, ".-")
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, item)
	}

	return clean
}

func containsItem(items []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item)) == target {
			return true
		}
	}
	return false
}
