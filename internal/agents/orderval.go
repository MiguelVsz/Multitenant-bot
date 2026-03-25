package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	appinternal "multi-tenant-bot/internal"
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

func HandleOrderVal(userInput string, currentState string, currentContext string) OrderValResponse {
	var context map[string]string
	_ = json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = map[string]string{}
	}

	orders := mockActiveOrders()

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
			selected, ok = pickOrderWithAI(userInput, orders)
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

func mockActiveOrders() []OrderDetail {
	return []OrderDetail{
		{
			ID:      "ORD-1001",
			Status:  "En preparacion",
			Items:   []string{"1 Hamburguesa Clasica", "1 Papas Medianas"},
			Address: "Calle 123 #45-67, Bogota",
			Total:   "$38.000",
		},
		{
			ID:      "ORD-1002",
			Status:  "En camino",
			Items:   []string{"2 Combos BBQ"},
			Address: "Carrera 10 #20-30, Bogota",
			Total:   "$54.000",
		},
	}
}

func renderOrdersList(orders []OrderDetail) string {
	lines := []string{"Estas son tus ordenes activas:"}
	for i, order := range orders {
		lines = append(lines, fmt.Sprintf("%d. %s - %s - %s", i+1, order.ID, order.Status, order.Total))
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

func pickOrderWithAI(userInput string, orders []OrderDetail) (OrderDetail, bool) {
	apiKey := strings.TrimSpace(os.Getenv("AGENT_ORDERVAL_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GROQ_API_KEY"))
	}
	if apiKey == "" {
		return OrderDetail{}, false
	}

	client := appinternal.NewGroqClient(apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Debes identificar a cual orden se refiere el usuario.

Ordenes disponibles:
%s

Responde solo con uno de estos valores:
- el ID exacto de una orden disponible
- UNKNOWN si no es posible identificarla

No expliques nada.`, renderOrdersList(orders))

	reply, err := client.Chat(ctx, []appinternal.AIMessage{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userInput},
	})
	if err != nil {
		return OrderDetail{}, false
	}

	normalized := strings.TrimSpace(strings.ToUpper(reply))
	for _, order := range orders {
		if strings.Contains(normalized, order.ID) {
			return order, true
		}
	}

	return OrderDetail{}, false
}
