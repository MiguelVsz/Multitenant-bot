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

// AskAIRecommendation usa la IA para recomendar un producto de la carta al usuario
func AskAIRecommendation(userQuery, productList, businessName string) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return "Te recomiendo revisar nuestra carta y elegir el producto que más te llame la atención. ¡Todos están deliciosos!"
	}

	systemPrompt := fmt.Sprintf(`Eres el asistente de ventas de %s.
Tu tarea es recomendar productos de la carta según lo que pida el cliente.
Sé entusiasta, breve y convincente. Máximo 2-3 oraciones.
No menciones precios a menos que el cliente lo pida.
Carta disponible:
%s`, businessName, productList)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.3-70b-versatile",
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userQuery},
		},
		"max_tokens": 150,
	})

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return "¡Todos nuestros productos son excelentes! Te recomiendo empezar con nuestra pizza más popular. ¿Te gustaría verla a domicilio o para recoger?"
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
		return "¡Todo lo que tenemos está delicioso! ¿Te ayudo a pedir algo específico?"
	}

	return strings.TrimSpace(result.Choices[0].Message.Content)
}
