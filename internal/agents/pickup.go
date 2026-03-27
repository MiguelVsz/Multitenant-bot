package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"multi-tenant-bot/internal/models"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

const (
	StatePickupAwaitingCity    = "PICKUP_AWAITING_CITY"
	StatePickupAwaitingStore   = "PICKUP_AWAITING_STORE"
	StatePickupConfirmingStore = "PICKUP_CONFIRMING_STORE"
	StatePickupAwaitingProduct = "PICKUP_AWAITING_PRODUCT"
	StatePickupUpsell          = "PICKUP_UPSELL"
	StatePickupConfirming      = "PICKUP_CONFIRMING"
)

// PickupResponse define la estructura de retorno del agente pickup
type PickupResponse struct {
	Message      string
	NextState    string
	NewContext   map[string]string
	Buttons      []models.InteractiveButton
	ShowZoneList bool
}

// HandlePickup maneja el flujo de recogida en tienda usando zonas de cobertura de la BD y la IA para productos
func HandlePickup(
	userInput string,
	currentState string,
	currentContext string,
	history []models.AIMessage,
	zones []models.CoverageZone,
	products []models.Product,
) PickupResponse {
	var context map[string]string
	json.Unmarshal([]byte(currentContext), &context)
	if context == nil {
		context = make(map[string]string)
	}

	var res PickupResponse
	apiKey := os.Getenv("AGENT_PICKUP_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	
	menuBtn := models.InteractiveButton{ID: "menu_principal", Title: "🏠 Menú Principal"}

	switch currentState {
	case "IDLE", "":
		res.Message = "🥡 *Recoger en Tienda*\n━━━━━━━━━━━━━━━━\n\n¡Perfecto! ¿En qué *sede o ciudad* te gustaría recoger tu pedido?"
		res.NextState = "PICKUP_AWAITING_LOCATION"
		res.ShowZoneList = true

	case "PICKUP_AWAITING_LOCATION":
		// Guard: botones
		if strings.HasPrefix(normalizeText(userInput), "menu_") || userInput == "confirm_cancel" {
			res.Message = "Entendido, consulta cancelada."
			res.NextState = "IDLE"
			return res
		}

		recentHistory := []models.AIMessage{}

		action, aiMsg, storeName := processPickupLocationAI(userInput, zones, recentHistory, apiKey)

		if action == "reply" {
			res.Message = aiMsg
			res.NextState = "PICKUP_AWAITING_LOCATION"
			res.ShowZoneList = true
			res.NewContext = context
			return res
		}

		// Validar si la sede existe
		var matchedZone *models.CoverageZone
		for i, z := range zones {
			if strings.EqualFold(z.Name, storeName) {
				matchedZone = &zones[i]
				break
			}
		}

		if matchedZone == nil {
			// Fallback si la IA se equivoca
			res.Message = aiMsg + "\n(Pero no encuentro exactamente esa sede en mi sistema. ¿Puedes confirmar el nombre o elegir otra?)"
			res.NextState = "PICKUP_AWAITING_LOCATION"
			res.Buttons = []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar"},
			}
			res.NewContext = context
			return res
		}

		context["store"] = matchedZone.Name
		context["city"] = "local" // Opcional
		
		finalMsg := aiMsg
		if finalMsg == "" {
			finalMsg = fmt.Sprintf("✅ ¡Sede confirmada: %s!", matchedZone.Name)
		}
		
		res.Message = fmt.Sprintf("%s\n\n🍕 ¿Qué deseas pedir? Escríbelo, pídeme recomendaciones o dime qué se te antoja:", finalMsg)
		res.NextState = StatePickupAwaitingProduct
		res.Buttons = []models.InteractiveButton{
			{ID: "confirm_cancel", Title: "❌ Cancelar"},
		}

	case StatePickupAwaitingProduct:
		// Guard: comandos de sistema
		if strings.HasPrefix(normalizeText(userInput), "menu_") && userInput != "menu_1" {
			res.Message = "Parece que deseas explorar otra opción 🧭\n\nPara cambiar, primero cancela tu pedido actual con el botón de abajo."
			res.NextState = currentState
			res.NewContext = context
			res.Buttons = []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
			}
			return res
		}
		if userInput == "menu_principal" || userInput == "confirm_cancel" {
			res.Message = ""
			res.NextState = "IDLE"
			res.NewContext = context
			return res
		}

		// Caso 1: El usuario pide una recomendación
		if IsRecommendationQuery(userInput) {
			catalogStr := context["catalog"]
			if catalogStr == "" {
				catalogStr = buildProductCatalogString(products)
			}
			recommendation := AskAIRecommendation(userInput, catalogStr, "nuestra pizzería")
			if recommendation == "" {
				recommendation = buildSimpleRecommendation(products)
			}
			res.Message = recommendation
			res.NextState = StatePickupAwaitingProduct
			res.Buttons = []models.InteractiveButton{
				{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
			}
			res.NewContext = context
			return res
		}

		// Caso 2: El usuario intenta pedir un producto — validar contra catálogo con IA (Agente Maestro)
		if len(products) > 0 && apiKey != "" {
			recentHistory := history
			if len(recentHistory) > 8 {
				recentHistory = recentHistory[len(recentHistory)-8:]
			}
			
			storeAddr := context["store"] + ", " + context["city"]
			cartStr := context["products"]
			action, aiMsg, productName, quantity := processOrderAI(userInput, storeAddr, cartStr, products, recentHistory, apiKey)
			
			if action == "reply" {
				res.Message = aiMsg
				res.NextState = StatePickupAwaitingProduct
				res.Buttons = []models.InteractiveButton{
					{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				}
				res.NewContext = context
				return res
			}

			if action == "confirm_order" {
				totalStr := ""
				if t := context["products"]; t != "" {
					totalStr = "\n🛒 Pedido: " + t
				} else {
					res.Message = "Aún no has agregado productos a tu pedido. ¿Qué te gustaría pedir?"
					res.NextState = StatePickupAwaitingProduct
					res.NewContext = context
					return res
				}
				res.Message = fmt.Sprintf(
					"📝 *Resumen – Recogida en Tienda*\n━━━━━━━━━━━━━━━━\n📍 Punto: *%s*\n🏙️ Ciudad: *%s*%s\n\n💰 El precio final se confirma en tienda.\n\n¿Confirmas tu pedido?",
					context["store"], context["city"], totalStr,
				)
				res.NextState = StatePickupConfirming
				res.Buttons = []models.InteractiveButton{
					{ID: "pickup_final_ok", Title: "✅ Confirmar pedido"},
					{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
				}
				res.NewContext = context
				return res
			}

			if action == "add_product" {
				// Encontrar el producto real y confirmar
				var matched *models.Product
				for i, p := range products {
					if strings.EqualFold(p.Name, productName) {
						matched = &products[i]
						break
					}
				}
				if matched != nil {
					existing := context["products"]
					if existing == "" {
						context["products"] = fmt.Sprintf("%dx %s ($%.0f)", quantity, matched.Name, matched.Price*float64(quantity))
					} else {
						context["products"] += fmt.Sprintf(", %dx %s", quantity, matched.Name)
					}
					
					upsellMsg := aiMsg
					if upsellMsg == "" {
						upsellMsg = fmt.Sprintf("🍕 ¡Excelente elección! Agregué *%dx %s* a tu pedido. ¿Deseas pedir algo más?", quantity, matched.Name)
					}
					
					res.Message = upsellMsg
					res.NextState = StatePickupAwaitingProduct
					res.Buttons = []models.InteractiveButton{
						{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
					}
					res.NewContext = context
					return res
				}
			}
		} else if len(products) == 0 {
			// Sin catálogo en BD: guardar lo que el usuario escribe
			context["products"] = userInput
			res.Message = fmt.Sprintf("🍕 Entendido: *%s*\n\n¿Te gustaría añadir algo más?", userInput)
			res.NextState = StatePickupUpsell
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_upsell_yes", Title: "✅ Sí, agregar"},
				{ID: "pickup_upsell_no", Title: "👎 No, continuar"},
				menuBtn,
			}
			res.NewContext = context
			return res
		}

		// Caso 3: No se encontró el producto → mostrar carta disponible
		var sb strings.Builder
		sb.WriteString("❌ No encontré ese producto en nuestra carta.\n\n")
		sb.WriteString("📋 *Productos disponibles:*\n")
		for _, p := range products {
			sb.WriteString(fmt.Sprintf("• *%s* — $%.0f\n", p.Name, p.Price))
		}
		sb.WriteString("\n¿Cuál de estos deseas pedir?")
		res.Message = sb.String()
		res.NextState = StatePickupAwaitingProduct
		res.Buttons = []models.InteractiveButton{
			{ID: "confirm_cancel", Title: "❌ Cancelar pedido"},
		}

	case StatePickupUpsell:
		if userInput == "pickup_upsell_yes" || isPositive(userInput) {
			context["upsell"] = "si"
			res.Message = "¿Qué más deseas agregar? Escríbelo y lo busco en nuestra carta:"
			res.NextState = StatePickupAwaitingProduct
		} else {
			totalStr := ""
			if t := context["products"]; t != "" {
				totalStr = "\n🛒 Pedido: " + t
			}
			res.Message = fmt.Sprintf(
				"📝 *Resumen – Recogida en Tienda*\n━━━━━━━━━━━━━━━━\n📍 Punto: *%s*\n🏙️ Ciudad: *%s*%s\n\n💰 El precio final se confirma en tienda.\n\n¿Confirmas tu pedido?",
				context["store"], context["city"], totalStr,
			)
			res.NextState = StatePickupConfirming
			res.Buttons = []models.InteractiveButton{
				{ID: "pickup_final_ok", Title: "✅ Confirmar pedido"},
				{ID: "pickup_final_cancel", Title: "❌ Cancelar"},
			}
		}

	case StatePickupConfirming:
		if userInput == "pickup_final_ok" || isPositive(userInput) {
			res.Message = fmt.Sprintf(
				"✅ *¡Pedido confirmado!*\n\nTu pedido para recoger en *%s* ha sido registrado.\n\n⏱️ Tiempo estimado: 20-30 min.\n\n¡Gracias por elegirnos! 🍕",
				context["store"],
			)
			res.NextState = "FINISHED"
			res.Buttons = []models.InteractiveButton{menuBtn}
		} else {
			res.Message = "Pedido cancelado. Si deseas iniciar de nuevo, selecciona *Recoger en Tienda* desde el menú."
			res.NextState = "IDLE"
			res.Buttons = []models.InteractiveButton{menuBtn}
		}
	}

	res.NewContext = context
	return res
}

// processPickupLocationAI es el Agente Maestro para entender el punto de recogida seleccionado por el usuario.
// Retorna la acción ("reply" o "set_store") y el nombre confirmado del CoverageZone si elige uno.
func processPickupLocationAI(input string, zones []models.CoverageZone, history []models.AIMessage, apiKey string) (action, message, storeName string) {
	if apiKey == "" {
		return "reply", "No tengo IA configurada. Especifica el lugar.", ""
	}

	var zonesInfo strings.Builder
	for _, z := range zones {
		zonesInfo.WriteString(fmt.Sprintf("- %s\n", z.Name))
	}

	prompt := fmt.Sprintf(`Eres el encargado de puntos de recogida de una pizzería/restaurante. El usuario quiere recoger un pedido.

Tu objetivo es entender en qué CIUDAD o SEDE desea recoger su pedido.
Nuestras sedes disponibles son:
%s

Debes responder estrictamente en formato JSON:
{
  "action": "reply" | "set_store",
  "message": "Mensaje para responder o confirmar",
  "store": "Nombre exacto de la sede (si set_store)"
}

REGLAS:
1. Si el usuario pregunta qué sedes hay, saluda, o dice una ciudad general donde tenemos múltiples sedes, usa action="reply" y nómbrale fluidamente las sedes disponibles.
2. Si el usuario escoge o nombra inequívocamente una sede de nuestra lista (ej: "Sede Norte", "en la norte", "la primera"), usa action="set_store" y pon en "store" el NOMBRE EXACTO de la sede de nuestra lista. En "message" puedes poner algo muy breve o dejarlo vacío (ej: "¡Perfecto!").
3. NUNCA inventes sedes. SOLO puedes usar las de la lista proporcionada.
4. Si menciona una ciudad, revisa cuántas sedes hay daar y ayúdalo a escoger respondiendo con action="reply".

Responde SOLO valid JSON.`, zonesInfo.String())

	messages := []map[string]string{
		{"role": "system", "content": prompt},
	}
	
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
		"temperature": 0.4,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 { 
		return "reply", "Tuve un problema. ¿Me podrías decir de nuevo qué sede prefieres?", "" 
	}
	defer resp.Body.Close()

	var resData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&resData)

	var data struct {
		Action  string `json:"action"`
		Message string `json:"message"`
		Store   string `json:"store"`
	}
	err = json.Unmarshal([]byte(resData.Choices[0].Message.Content), &data)
	if err != nil {
		return "reply", "¿Me confirmas el nombre de la sede, por favor?", ""
	}

	if data.Action == "" { data.Action = "reply" }
	return data.Action, data.Message, data.Store
}

// buildProductCatalogString construye un string legible con el catálogo de productos
func buildProductCatalogString(products []models.Product) string {
	if len(products) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range products {
		sb.WriteString(fmt.Sprintf("- %s: $%.0f", p.Name, p.Price))
		if p.Description != nil && *p.Description != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", *p.Description))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildSimpleRecommendation devuelve una recomendación sin IA cuando la API no está disponible
func buildSimpleRecommendation(products []models.Product) string {
	if len(products) == 0 {
		return "¡Explora nuestra carta y elige lo que más te apetezca!"
	}
	best := products[0]
	for _, p := range products {
		// Preferir pizzas si hay
		if strings.Contains(strings.ToLower(p.Name), "pizza") {
			best = p
			break
		}
	}
	return fmt.Sprintf("¡Te recomiendo nuestra *%s* ($%.0f)! Es una de las favoritas de nuestros clientes. 🍕 ¿Te la incluyo en tu pedido?", best.Name, best.Price)
}

// filterZonesByCity filtra zonas por ciudad con búsqueda flexible
func filterZonesByCity(zones []models.CoverageZone, city string) []models.CoverageZone {
	cityNorm := normalizeText(city)
	var matched []models.CoverageZone
	for _, z := range zones {
		if strings.Contains(normalizeText(z.Name), cityNorm) || strings.Contains(cityNorm, normalizeText(z.Name)) {
			matched = append(matched, z)
		}
	}
	return matched
}

// normalizeText normaliza texto para comparación (minúsculas, sin tildes, sin caracteres especiales)
func normalizeText(s string) string {
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u",
		"ñ", "n", "Ñ", "n",
	)
	cleaned := replacer.Replace(s)
	var b strings.Builder
	for _, r := range cleaned {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '_' {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimSpace(b.String())
}
