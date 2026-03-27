package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"multi-tenant-bot/internal/models"
	"net/http"
	"strings"
	"time"
)

// AskAIRecommendation usa la IA para recomendar un producto de la carta al usuario
func AskAIRecommendation(userQuery, productList, businessName string) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return "¡Todos nuestros productos son deliciosos! Te recomiendo explorar nuestra carta para elegir el que más te llame la atención. 🍕"
	}

	systemPrompt := fmt.Sprintf(`Eres el asistente experto en ventas de %s.
Tu tarea: recomendar 1-2 productos de la carta según lo que el cliente pida o su estado de ánimo.
Sé breve, entusiasta y convincente. Máximo 3 oraciones.
Menciona el nombre exacto del producto y su precio.
Si el cliente menciona algo específico (picante, vegetariano, dulce, etc.), recomienda lo más adecuado.

CARTA DISPONIBLE:
%s`, businessName, productList)

	return callGroqAI(systemPrompt, userQuery, 180)
}

// BuildCatalogContext construye un string con toda la información del catálogo para el sistema de IA
func BuildCatalogContext(products []models.Product, zones []models.CoverageZone, businessName string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== INFORMACIÓN DE %s ===\n\n", strings.ToUpper(businessName)))

	if len(products) > 0 {
		sb.WriteString("PRODUCTOS Y PRECIOS:\n")
		for _, p := range products {
			sb.WriteString(fmt.Sprintf("• %s: $%.0f", p.Name, p.Price))
			if p.Description != nil && *p.Description != "" {
				sb.WriteString(fmt.Sprintf(" — %s", *p.Description))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(zones) > 0 {
		sb.WriteString("SEDES / PUNTOS DE RECOGIDA:\n")
		for _, z := range zones {
			sb.WriteString(fmt.Sprintf("• %s", z.Name))
			if z.DeliveryFee > 0 {
				sb.WriteString(fmt.Sprintf(" (Domicilio: $%.0f)", z.DeliveryFee))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// AskAIWithCatalog usa la IA para responder consultas generales conociendo el catálogo completo
func AskAIWithCatalog(userQuery, catalogContext, businessName string) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return ""
	}

	systemPrompt := fmt.Sprintf(`Eres el asistente inteligente de %s. Conoces perfectamente el catálogo y puedes ayudar al cliente a elegir productos, responder preguntas y hacer recomendaciones.
Respuesta máxima: 3 oraciones. Sé amigable y directo.

%s`, businessName, catalogContext)

	return callGroqAI(systemPrompt, userQuery, 200)
}

// isRecommendationQuery detecta si el usuario está pidiendo una recomendación
func IsRecommendationQuery(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	keywords := []string{
		"recomien", "sugi", "qué pedir", "que pedir",
		"qué me das", "que me das", "qué quieres", "qué tienes",
		"qué hay", "que hay", "antojo", "se me antoja",
		"sorpréndeme", "sorprendeme", "tú decides", "tu decides",
		"no sé qué", "no se que", "qué elijo", "que elijo",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// callGroqAI hace una llamada a la API de Groq/LLaMA y devuelve el texto de respuesta
func callGroqAI(systemPrompt, userMsg string, maxTokens int) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      "llama-3.3-70b-versatile",
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(result.Choices[0].Message.Content)
}
