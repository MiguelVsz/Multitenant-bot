package llm

import (
	"context"
	"encoding/json"
	"strings"
)

type LLMClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type MockClient struct{}

func NewMockClient() *MockClient {
	return &MockClient{}
}

func (m *MockClient) Generate(_ context.Context, prompt string) (string, error) {
	lower := strings.ToLower(prompt)

	currentFlow := extractValue(lower, `"current_flow":`)
	message := extractValue(lower, `"message":`)

	intent := "UNKNOWN"
	redirectTo := "UNKNOWN"
	flowTarget := "START"
	nextStep := "FALLBACK_UNKNOWN"
	confidence := 0.42
	reasoning := "No strong signal detected; fallback routing applied."

	switch {
	case containsAny(message, "queja", "reclamo", "pqr", "problema", "mala experiencia", "soporte", "reembolso"):
		intent = "PQR"
		redirectTo = "PQR"
		nextStep = "START_PQR_TRIAGE"
		confidence = 0.95
		reasoning = "The message contains complaint or support language."
	case containsAny(message, "estado de mi pedido", "donde va mi pedido", "orden", "pedido", "tracking", "seguimiento"):
		intent = "ORDER_STATUS"
		redirectTo = "ORDER_STATUS"
		nextStep = "START_ORDER_LOOKUP"
		confidence = 0.92
		reasoning = "The message asks about an existing order."
	case containsAny(message, "recoger", "pickup", "retiro", "retirar en tienda"):
		intent = "PICKUP"
		redirectTo = "PICKUP"
		nextStep = "START_PICKUP"
		confidence = 0.94
		reasoning = "The message explicitly references in-store pickup."
	case containsAny(message, "domicilio", "entrega", "llevar", "enviarlo", "mandalo", "direccion"):
		intent = "DELIVERY"
		redirectTo = "DELIVERY"
		nextStep = "START_DELIVERY"
		confidence = 0.94
		reasoning = "The message explicitly references delivery."
	case containsAny(message, "hamburguesa", "pizza", "combo", "papas", "gaseosa", "menu", "carta", "productos", "precios", "que hay de comer", "recomiendame"):
		intent = "MENU"
		redirectTo = "MENU"
		nextStep = "START_MENU"
		confidence = 0.90
		reasoning = "The message is about products, prices, or menu exploration."
	case containsAny(message, "hola", "buenas", "hello", "hi"):
		intent = "GREETING"
		redirectTo = "GREETING"
		nextStep = "START_GREETING"
		confidence = 0.88
		reasoning = "The message is a greeting without a stronger operational signal."
	}

	if isMenuFlow(currentFlow) && containsAny(message, "hamburguesa", "pizza", "combo", "papas", "gaseosa", "quiero pedir", "quiero una", "me das", "lo quiero") {
		intent = "DELIVERY"
		redirectTo = "DELIVERY"
		flowTarget = "MID"
		nextStep = "MID_ORDER_CAPTURE"
		confidence = 0.96
		reasoning = "The user is inside a menu browsing flow and is now expressing a purchase action; redirect mid-conversation."
	}

	if isMenuFlow(currentFlow) && containsAny(message, "recoger", "pickup", "retiro", "retirar") {
		intent = "PICKUP"
		redirectTo = "PICKUP"
		flowTarget = "MID"
		nextStep = "MID_PICKUP_CAPTURE"
		confidence = 0.97
		reasoning = "The user is inside a menu browsing flow and is now choosing pickup; redirect mid-conversation."
	}

	decision := map[string]any{
		"intent":       intent,
		"confidence":   confidence,
		"redirect_to":  redirectTo,
		"flow_target":  flowTarget,
		"next_step":    nextStep,
		"context_used": strings.TrimSpace(currentFlow) != "",
		"reasoning":    reasoning,
	}

	raw, err := json.Marshal(decision)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func extractValue(prompt, key string) string {
	idx := strings.Index(prompt, key)
	if idx == -1 {
		return ""
	}
	rest := prompt[idx+len(key):]
	firstQuote := strings.Index(rest, `"`)
	if firstQuote == -1 {
		return ""
	}
	rest = rest[firstQuote+1:]
	endQuote := strings.Index(rest, `"`)
	if endQuote == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:endQuote])
}

func containsAny(input string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(input, value) {
			return true
		}
	}
	return false
}

func isMenuFlow(flow string) bool {
	return containsAny(flow, "menu", "carta", "product_discovery", "catalog", "browse")
}
