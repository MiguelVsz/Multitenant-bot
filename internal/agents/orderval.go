package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	StateOrderValList   = "ORDERVAL_LIST"
	StateOrderValDetail = "ORDERVAL_DETAIL"
)

type OrderSummary struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Total  string `json:"total"`
}

type OrderDetail struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Items   []string `json:"items"`
	Address string   `json:"address"`
	Total   string   `json:"total"`
}

type OrderValResponse struct {
	Message    string
	NextState  string
	NewContext map[string]string
}

func HandleOrderVal(userInput string, currentState string, currentContext string, orders []OrderDetail, historyJSON string) OrderValResponse {
	var context map[string]string
	_ = json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = map[string]string{}
	}

	switch currentState {
	case "", "IDLE", "ORDERVAL_START":
		if len(orders) == 0 {
			return OrderValResponse{
				Message:    "No tienes ordenes registradas por ahora. Si deseas, puedo ayudarte con un nuevo pedido o con cualquier otra consulta.",
				NextState:  "IDLE",
				NewContext: context,
			}
		}

		context["orders_count"] = fmt.Sprintf("%d", len(orders))
		return OrderValResponse{
			Message:    renderOrdersList(orders) + "\n\nSi deseas el detalle de una orden, escribeme el numero, el ID o describeme cual quieres ver.",
			NextState:  StateOrderValList,
			NewContext: context,
		}

	case StateOrderValList:
		selected, ok := pickOrder(userInput, orders)
		if !ok {
			selected, ok = pickOrderWithAI(userInput, orders, historyJSON)
		}
		if !ok {
			return OrderValResponse{
				Message:    "No pude identificar la orden exacta. Puedes escribir el numero de la lista, el ID o describirla, por ejemplo: la que va en camino.",
				NextState:  StateOrderValList,
				NewContext: context,
			}
		}

		context["selected_order_id"] = selected.ID
		return OrderValResponse{
			Message:    renderOrderDetail(selected) + "\n\nSi quieres revisar otra orden, dimela. Si no, volvemos al menu principal.",
			NextState:  StateOrderValDetail,
			NewContext: context,
		}

	case StateOrderValDetail:
		if strings.Contains(strings.ToLower(userInput), "otra") {
			return OrderValResponse{
				Message:    renderOrdersList(orders) + "\n\nSi deseas el detalle de una orden, escribeme el numero, el ID o describeme cual quieres ver.",
				NextState:  StateOrderValList,
				NewContext: context,
			}
		}
		return OrderValResponse{
			Message:    "Perfecto, regresamos al menu principal. Si necesitas algo mas, tambien puedes escribirme libremente y con gusto te ayudo.",
			NextState:  "IDLE",
			NewContext: context,
		}

	default:
		return OrderValResponse{
			Message:    "Regresamos al menu principal. Si quieres consultar tus ordenes, solo dimelo.",
			NextState:  "IDLE",
			NewContext: context,
		}
	}
}

func renderOrdersList(orders []OrderDetail) string {
	lines := []string{"Estas son tus órdenes activas:"}
	for i, order := range orders {
		lines = append(lines, fmt.Sprintf("%d. *%s* - %s - Total: %s\n   _Productos: %s_", i+1, order.ID, order.Status, order.Total, strings.Join(order.Items, ", ")))
	}
	return strings.Join(lines, "\n")
}

func renderOrderDetail(order OrderDetail) string {
	return fmt.Sprintf(
		"Detalle de la orden %s:\nEstado: %s\nProductos: %s\nDireccion: %s\nTotal: %s",
		order.ID,
		order.Status,
		strings.Join(order.Items, ", "),
		order.Address,
		order.Total,
	)
}

func pickOrder(input string, orders []OrderDetail) (OrderDetail, bool) {
	trimmed := strings.TrimSpace(strings.ToUpper(input))
	for i, order := range orders {
		if trimmed == order.ID || trimmed == fmt.Sprintf("%d", i+1) {
			return order, true
		}
	}
	return OrderDetail{}, false
}

func pickOrderWithAI(userInput string, orders []OrderDetail, historyJSON string) (OrderDetail, bool) {
	apiKey := strings.TrimSpace(os.Getenv("AGENT_ORDERVAL_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GROQ_API_KEY"))
	}
	if apiKey == "" {
		return OrderDetail{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Debes identificar a cuál orden se refiere el usuario basándote en su descripción, el ID o los productos del pedido.

Órdenes disponibles (analiza los productos de cada una):
%s

Instrucciones:
1. Responde solo con el ID EXACTO de la orden si puedes deducirlo de la intención del usuario.
2. Si la descripción es ambigua o no coincide clara/lógicamente con los productos u órdenes disponibles, responde UNKNOWN.
3. No expliques nada, tu salida debe ser literalmente el ID o UNKNOWN.`, renderOrdersList(orders))

	var messages []map[string]string
	messages = append(messages, map[string]string{"role": "system", "content": prompt})

	var history []map[string]string
	if historyJSON != "" {
		_ = json.Unmarshal([]byte(historyJSON), &history)
		// Incluir solo los últimos 6 mensajes del historial
		startIdx := 0
		if len(history) > 6 {
			startIdx = len(history) - 6
		}
		for i := startIdx; i < len(history); i++ {
			if history[i]["role"] != "system" {
				messages = append(messages, history[i])
			}
		}
	} else {
		// Fallback si no hay historial enviado, usamos el input
		messages = append(messages, map[string]string{"role": "user", "content": userInput})
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.3-70b-versatile",
		"messages": messages,
	})

	req, _ := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OrderDetail{}, false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		return OrderDetail{}, false
	}

	reply := parsed.Choices[0].Message.Content

	normalized := strings.TrimSpace(strings.ToUpper(reply))
	for _, order := range orders {
		if strings.Contains(normalized, order.ID) {
			return order, true
		}
	}

	return OrderDetail{}, false
}
