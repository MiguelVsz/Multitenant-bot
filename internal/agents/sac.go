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

func HandleSAC(input string) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return fmt.Sprintf(
			"Entiendo tu solicitud de soporte: %s\n\nPuedo ayudarte a registrar una PQRS de forma inicial. Configura AGENT_SAC_KEY o GROQ_API_KEY para obtener una respuesta especializada. Si prefieres, describe tu caso con mas detalle o escribe menu principal para volver.",
			input,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "llama-3.3-70b-versatile",
		"messages": []map[string]string{
			{"role": "system", "content": sacSystemPrompt(sacBusinessType())},
			{"role": "user", "content": input},
		},
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
		return fmt.Sprintf(
			"Entiendo tu solicitud de soporte: %s\n\nNo pude consultar al agente SAC en este momento (%v). Describe tu caso con mas detalle o escribe menu principal para volver.",
			input,
			err,
		)
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
		return "Lo siento, el agente SAC no ha respondido en este momento. Por favor describe tu caso nuevamente o escribe 'menu principal'."
	}

	return cleanAIReply(parsed.Choices[0].Message.Content)
}

func cleanAIReply(reply string) string {
	// Remover markdown bold markers
	reply = strings.ReplaceAll(reply, "**", "")

	// Etiquetas y prefijos de razonamiento interno a eliminar
	internalTags := []string{
		"VALIDACION", "DIAGNOSTICO", "PLAN DE ACCION", "PLAN DE ACCIÓN",
		"VALIDACIÓN", "DIAGNÓSTICO", "SELECCIONO", "SELECCIONO MENU",
		"ANADE", "AÑADE", "REGISTRO", "ERROR", "RADICADO", "TIEMPO DE RESPUESTA",
		"TIEMPO DE RESPUESTA ESTIMADO", "SOLUCION", "SOLUCIÓN",
	}
	
	lines := strings.Split(reply, "\n")
	var cleanedLines []string
	
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		upperLine := strings.ToUpper(trimmedLine)
		isInternal := false

		// 1. Eliminar líneas que son puramente etiquetas internas o contienen palabras prohibidas
		for _, tag := range internalTags {
			// Caso: "*TAG*" o "TAG:" o simplemente la palabra sola al inicio
			if strings.Contains(upperLine, "*"+tag+"*") || 
			   strings.HasPrefix(upperLine, tag+":") || 
			   strings.HasPrefix(upperLine, "- "+tag+":") ||
			   (len(trimmedLine) < len(tag)+5 && strings.Contains(upperLine, tag)) {
				
				isInternal = true
				break
			}
		}

	// Filtros adicionales para líneas con radicados
		if !isInternal {
			if strings.HasPrefix(upperLine, "SELECCIONO") ||
			   strings.HasPrefix(upperLine, "FALTA INFORMACIÓN") ||
			   strings.HasPrefix(upperLine, "FALTA INFORMACION") ||
			   strings.Contains(upperLine, "RADICADO:") ||
			   strings.Contains(upperLine, "TIEMPO DE RESPUESTA:") ||
			   strings.Contains(upperLine, "#SAC") {
				isInternal = true
			}
		}

		if !isInternal && strings.TrimSpace(line) != "" {
			cleanedLines = append(cleanedLines, line)
		}
	}
	
	return strings.Join(cleanedLines, "\n")
}

func resolveSACKey() string {
	if v := strings.TrimSpace(os.Getenv("AGENT_SAC_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); v != "" {
		return v
	}
	return ""
}

func sacBusinessType() string {
	if v := strings.TrimSpace(os.Getenv("SAC_BUSINESS_TYPE")); v != "" {
		return v
	}
	return "servicio de restaurante gourmet"
}

func sacSystemPrompt(tipoDeNegocio string) string {
	return fmt.Sprintf(`Eres el Especialista de Soporte al Cliente (SAC) de una empresa de %s.

REGLAS ESTRICTAS:
- Si el mensaje del usuario está vacío, saluda cordialmente, preséntate como soporte y pregunta en qué puedes ayudar. Máximo 2 oraciones.
- Si el usuario tiene una queja, reclamo o sugerencia, responde de forma empática, directa y ofrece una solución concreta.
- Responde SOLO el mensaje final para el cliente. NO incluyas encabezados internos como VALIDACION, DIAGNOSTICO, PLAN DE ACCION, RADICADO o TIEMPO DE RESPUESTA.
- Tono: profesional, cálido y resolutivo.
- Máximo 250 caracteres por respuesta.`, tipoDeNegocio)
}
