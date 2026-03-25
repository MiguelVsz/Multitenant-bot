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
	"unicode"
)

const (
	RouteIntentDelivery   = "delivery"
	RouteIntentPickup     = "pickup"
	RouteIntentOrders     = "orders"
	RouteIntentUpdateData = "update_data"
	RouteIntentLocations  = "locations"
	RouteIntentCarta      = "carta"
	RouteIntentSAC        = "sac"
	RouteIntentGreeting   = "greeting"
	RouteIntentMainMenu   = "main_menu"
	RouteIntentUnknown    = "unknown"
)

type routerGroqRequest struct {
	Model    string      `json:"model"`
	Messages []routerMsg `json:"messages"`
}

type routerMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type routerGroqResponse struct {
	Choices []struct {
		Message routerMsg `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type RouterResponse struct {
	Intent         string
	Confidence     string
	Message        string
	ShouldEscalate bool
}

func HandleRouter(userInput string) RouterResponse {
	if matched, ok := routeByAI(userInput); ok {
		logHandoff("ai", matched.Intent)
		return matched
	}

	normalized := normalizeRouteInput(userInput)

	switch {
	case looksLikeGreeting(normalized):
		resp := newRouteResponse(RouteIntentGreeting, "fallback", "Hola, en que te puedo ayudar. Puedo orientarte con domicilios, recogida en tienda, la carta, tus pedidos, sedes o actualizar tus datos.")
		logHandoff("fallback:greeting", resp.Intent)
		return resp
	case looksLikeMainMenu(normalized):
		resp := newRouteResponse(RouteIntentMainMenu, "fallback", "Claro, estas son las opciones disponibles: domicilio, recogida en tienda, carta, mis pedidos, sedes o actualizar datos.")
		logHandoff("fallback:main_menu", resp.Intent)
		return resp
	case looksLikeDelivery(normalized):
		resp := newRouteResponse(RouteIntentDelivery, "fallback", "Claro que si, con gusto te acompano con tu pedido a domicilio.")
		logHandoff("fallback:delivery", resp.Intent)
		return resp
	case looksLikePickup(normalized):
		resp := newRouteResponse(RouteIntentPickup, "fallback", "Perfecto, con gusto te ayudo con la recogida en tienda.")
		logHandoff("fallback:pickup", resp.Intent)
		return resp
	case looksLikeOrders(normalized):
		resp := newRouteResponse(RouteIntentOrders, "fallback", "Claro, revisemos juntos el estado de tus ordenes.")
		logHandoff("fallback:orders", resp.Intent)
		return resp
	case looksLikeUpdateData(normalized):
		resp := newRouteResponse(RouteIntentUpdateData, "fallback", "Con gusto te ayudo a actualizar tus datos.")
		logHandoff("fallback:update_data", resp.Intent)
		return resp
	case looksLikeLocations(normalized):
		resp := newRouteResponse(RouteIntentLocations, "fallback", "Claro, te comparto las sedes disponibles.")
		logHandoff("fallback:locations", resp.Intent)
		return resp
	case looksLikeCarta(normalized):
		resp := newRouteResponse(RouteIntentCarta, "fallback", "Claro, te comparto la carta para ayudarte mejor.")
		logHandoff("fallback:carta", resp.Intent)
		return resp
	case looksLikeSAC(normalized):
		resp := newRouteResponse(RouteIntentSAC, "fallback", "Lamento que estes pasando por eso. Voy a ayudarte con mucho gusto.")
		resp.ShouldEscalate = true
		logHandoff("fallback:sac", resp.Intent)
		return resp
	case looksLikeHumanMessage(normalized):
		resp := newRouteResponse(RouteIntentSAC, "fallback", "Te leo con gusto. Cuentame un poco mas para poder ayudarte mejor.")
		resp.ShouldEscalate = true
		logHandoff("fallback:human", resp.Intent)
		return resp
	default:
		logHandoff("fallback:unknown", RouteIntentUnknown)
		return routeUnknown()
	}
}

func routeByAI(userInput string) (RouterResponse, bool) {
	apiKey, keyName := resolveRouterKey()
	if apiKey == "" {
		return RouterResponse{}, false
	}

	prompt := `Eres el clasificador de intenciones de un bot de restaurante.
Clasifica el mensaje del usuario en UNA sola etiqueta.

Etiquetas validas:
- delivery     -> quiere que le lleven comida a casa
- pickup       -> quiere recoger en tienda
- orders       -> pregunta por un pedido que ya hizo
- update_data  -> quiere cambiar correo, telefono, direccion
- locations    -> pregunta donde queda el restaurante o sus sedes
- carta        -> quiere ver productos, precios, pedir comida o un producto especifico
- sac          -> tiene una queja, reclamo o necesita soporte
- greeting     -> saluda o dice algo sin intencion clara
- main_menu    -> pide ver las opciones disponibles del bot, quiere saber que puede hacer aqui
- unknown      -> el mensaje no tiene ninguna relacion con un restaurante

Ejemplos de carta:
"dame una hamburguesota" -> carta
"que hay de comer" -> carta
"me recomiendas algo" -> carta
"cuanto vale el combo" -> carta
"quiero pedir una pizza" -> carta
"me antoje de unas papas" -> carta

Ejemplos de main_menu:
"me repites las opciones" -> main_menu
"que puedo hacer aca" -> main_menu
"cuales son las opciones" -> main_menu
"me dices que servicios tienen" -> main_menu
"menu principal" -> main_menu

Ejemplos de greeting:
"hola como estan" -> greeting
"buenas que mas" -> greeting
"que onda loco" -> greeting
"todo bien" -> greeting

Regla principal: si hay cualquier intencion de pedir o preguntar por comida o productos, responde carta.
Responde SOLO la etiqueta, sin explicacion, sin puntos, sin mayusculas.`

	reqBody, err := json.Marshal(routerGroqRequest{
		Model: "llama-3.3-70b-versatile",
		Messages: []routerMsg{
			{Role: "system", Content: prompt},
			{Role: "user", Content: userInput},
		},
	})
	if err != nil {
		return RouterResponse{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return RouterResponse{}, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return RouterResponse{}, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RouterResponse{}, false
	}

	var parsed routerGroqResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return RouterResponse{}, false
	}
	if parsed.Error != nil || len(parsed.Choices) == 0 {
		return RouterResponse{}, false
	}

	if parsed.Usage != nil {
		logTokenUsage(keyName, apiKey, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, parsed.Usage.TotalTokens)
	}

	intent := strings.ToLower(strings.TrimSpace(parsed.Choices[0].Message.Content))
	fields := strings.Fields(intent)
	if len(fields) == 0 {
		return RouterResponse{}, false
	}
	intent = fields[0]

	switch intent {
	case RouteIntentDelivery:
		return newRouteResponse(RouteIntentDelivery, "ai", "Claro que si, con gusto te acompano con tu pedido a domicilio."), true
	case RouteIntentPickup:
		return newRouteResponse(RouteIntentPickup, "ai", "Perfecto, con gusto te ayudo con la recogida en tienda."), true
	case RouteIntentOrders:
		return newRouteResponse(RouteIntentOrders, "ai", "Claro, revisemos juntos el estado de tus ordenes."), true
	case RouteIntentUpdateData:
		return newRouteResponse(RouteIntentUpdateData, "ai", "Con gusto te ayudo a actualizar tus datos."), true
	case RouteIntentLocations:
		return newRouteResponse(RouteIntentLocations, "ai", "Claro, te comparto las sedes disponibles."), true
	case RouteIntentCarta:
		return newRouteResponse(RouteIntentCarta, "ai", "Claro, te comparto la carta para ayudarte mejor."), true
	case RouteIntentMainMenu:
		return newRouteResponse(RouteIntentMainMenu, "ai", "Claro, estas son las opciones disponibles: domicilio, recogida en tienda, carta, mis pedidos, sedes o actualizar datos."), true
	case RouteIntentGreeting:
		return newRouteResponse(RouteIntentGreeting, "ai", "Hola, en que te puedo ayudar. Puedo orientarte con domicilios, recogida en tienda, la carta, tus pedidos, sedes o actualizar tus datos."), true
	case RouteIntentSAC:
		resp := newRouteResponse(RouteIntentSAC, "ai", "Lamento que estes pasando por eso. Voy a ayudarte con mucho gusto.")
		resp.ShouldEscalate = true
		return resp, true
	default:
		return RouterResponse{}, false
	}
}

func resolveRouterKey() (string, string) {
	if v := strings.TrimSpace(os.Getenv("AGENT_ROUTER_KEY")); v != "" {
		return v, "AGENT_ROUTER_KEY"
	}
	if v := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); v != "" {
		return v, "GROQ_API_KEY"
	}
	return "", ""
}

func logTokenUsage(keyName, apiKey string, prompt, completion, total int) {
	fmt.Printf(
		"[ROUTER][TOKENS] key=%s(%s) prompt=%d completion=%d total=%d\n",
		keyName, maskKey(apiKey), prompt, completion, total,
	)
}

func logHandoff(via, intent string) {
	fmt.Printf(
		"[ROUTER][HANDOFF] via=%s -> agente=%s (intent=%s)\n",
		via, intentToAgentName(intent), intent,
	)
}

func intentToAgentName(intent string) string {
	switch intent {
	case RouteIntentDelivery:
		return "Agente Domicilios"
	case RouteIntentPickup:
		return "Agente Recogida en Tienda"
	case RouteIntentOrders:
		return "Agente Pedidos"
	case RouteIntentUpdateData:
		return "Agente Actualizar Datos"
	case RouteIntentLocations:
		return "Agente Sedes"
	case RouteIntentCarta:
		return "Agente Carta"
	case RouteIntentSAC:
		return "Agente SAC"
	case RouteIntentGreeting:
		return "Agente Saludo"
	case RouteIntentMainMenu:
		return "Agente Menu Principal"
	default:
		return "Sin agente (unknown)"
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return "..." + key[len(key)-8:]
}

func newRouteResponse(intent, confidence, message string) RouterResponse {
	return RouterResponse{
		Intent:     intent,
		Confidence: confidence,
		Message:    message,
	}
}

func routeUnknown() RouterResponse {
	return RouterResponse{
		Intent:         RouteIntentUnknown,
		Confidence:     "low",
		Message:        "No logre entender ese mensaje. Puedes contarme que necesitas o escribir menu principal para ver las opciones.",
		ShouldEscalate: true,
	}
}

func normalizeRouteInput(input string) string {
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u",
	)
	cleaned := replacer.Replace(input)
	var b strings.Builder
	b.Grow(len(cleaned))
	for _, r := range cleaned {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func looksLikeGreeting(input string) bool {
	return containsAny(input,
		"hola", "buenas", "buenos dias", "buenas tardes", "buenas noches",
		"buen dia", "que tal", "como estas", "como estan", "saludos", "hi",
	)
}

func looksLikeMainMenu(input string) bool {
	return containsAny(input,
		"menu principal", "inicio", "opciones", "que puedo hacer",
		"cuales son las opciones", "servicios",
	)
}

func looksLikeDelivery(input string) bool {
	return containsAny(input,
		"domicilio", "a domicilio", "envio", "entrega", "mandar", "llevar",
	)
}

func looksLikePickup(input string) bool {
	return containsAny(input,
		"recoger", "recogida", "recojo", "retiro", "retirar", "tienda",
	)
}

func looksLikeOrders(input string) bool {
	return containsAny(input,
		"pedido", "pedidos", "orden", "ordenes", "ver mis pedidos", "seguimiento",
	)
}

func looksLikeUpdateData(input string) bool {
	return containsAny(input,
		"actualizar datos", "actualizar", "cambiar correo", "cambiar direccion",
		"cambiar telefono", "modificar datos", "editar datos",
	)
}

func looksLikeLocations(input string) bool {
	return containsAny(input,
		"sedes", "sede", "ubicacion", "ubicaciones", "donde quedan",
		"direccion de la tienda", "puntos de recogida",
	)
}

func looksLikeCarta(input string) bool {
	return containsAny(input,
		"carta", "menu de comida", "productos", "precios", "que hay de comer",
		"que tienen", "hamburguesa", "combo", "papas", "gaseosa", "pizza",
		"quiero pedir", "quiero comer", "me recomiendas",
	)
}

func looksLikeSAC(input string) bool {
	return containsAny(input,
		"pqrs", "queja", "reclamo", "soporte", "peticion",
		"felicitacion", "sugerencia", "problema con mi pedido",
	)
}

func looksLikeHumanMessage(input string) bool {
	words := strings.Fields(input)
	if len(words) == 0 {
		return false
	}
	letterCount := 0
	for _, r := range input {
		if unicode.IsLetter(r) {
			letterCount++
		}
	}
	if letterCount < 3 {
		return false
	}
	if len(words) >= 2 {
		return true
	}
	return containsAny(input, "ayuda", "necesito", "quiero", "como", "que")
}

func containsAny(input string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return true
		}
	}
	return false
}
