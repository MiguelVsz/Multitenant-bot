package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	appinternal "multi-tenant-bot/internal"

	"github.com/joho/godotenv"
)

const (
	consoleAgentNone       = ""
	consoleAgentCarta      = RouteIntentCarta
	consoleAgentDelivery   = RouteIntentDelivery
	consoleAgentPickup     = RouteIntentPickup
	consoleAgentOrders     = RouteIntentOrders
	consoleAgentUpdateData = RouteIntentUpdateData
	consoleAgentSAC        = RouteIntentSAC
)

type consoleSession struct {
	ActiveAgent    string
	CurrentState   string
	CurrentContext string
	Delivery       *DeliverySession
}

func RunConsoleChat() error {
	_ = godotenv.Load()

	store, err := newAgentSessionStoreFromEnv()
	if err != nil {
		return fmt.Errorf("redis session store: %w", err)
	}

	userID := strings.TrimSpace(os.Getenv("CONSOLE_USER_ID"))
	if userID == "" {
		userID = "console-user"
	}

	scanner := bufio.NewScanner(os.Stdin)
	session := &consoleSession{}
	if store != nil {
		loaded, err := store.Load(context.Background(), userID)
		if err != nil {
			return fmt.Errorf("load console session: %w", err)
		}
		session = loaded
	}

	fmt.Println("Bot de pruebas iniciado.")
	fmt.Println("Puedes escribir cosas como: menu, sedes, domicilio, recoger en tienda, ver mis pedidos, actualizar datos.")
	fmt.Println("Escribe 'salir' para terminar y 'menu principal' para reiniciar el flujo.")

	for {
		fmt.Print("\nTu: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "salir", "exit", "quit":
			if store != nil {
				if err := store.Save(context.Background(), userID, session); err != nil {
					log.Printf("save console session: %v", err)
				}
			}
			fmt.Println("Sesion finalizada.")
			return nil
		case "menu principal", "menu", "inicio", "reiniciar":
			resetConsoleSession(session)
			if store != nil {
				if err := store.Save(context.Background(), userID, session); err != nil {
					log.Printf("save console session: %v", err)
				}
			}
			fmt.Println("Bot:")
			fmt.Println("  Regresamos al menu principal.")
			fmt.Println("  Puedes elegir domicilio, recoger en tienda, ver pedidos, actualizar datos, ver sedes o ver productos.")
			continue
		}

		reply := handleConsoleInput(session, input)
		if store != nil {
			if err := store.Save(context.Background(), userID, session); err != nil {
				log.Printf("save console session: %v", err)
			}
		}
		fmt.Println("Bot:")
		for _, line := range strings.Split(reply, "\n") {
			fmt.Printf("  %s\n", line)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error leyendo la consola: %w", err)
	}

	return nil
}

func handleConsoleInput(session *consoleSession, input string) string {
	if session.ActiveAgent != consoleAgentNone {
		return continueActiveFlow(session, input)
	}

	route := HandleRouter(input)
	switch route.Intent {
	case RouteIntentCarta:
		session.ActiveAgent = consoleAgentCarta
		return joinTransition(route.Message, renderStaticMenu(), "Si ya sabes lo que quieres, dime el producto y si lo prefieres a domicilio o para recoger en tienda.")
	case RouteIntentLocations:
		return joinTransition(route.Message, renderStaticLocations())
	case RouteIntentDelivery:
		session.ActiveAgent = consoleAgentDelivery
		session.Delivery = &DeliverySession{}
		return joinTransition(route.Message, applyDelivery(session, input))
	case RouteIntentPickup:
		session.ActiveAgent = consoleAgentPickup
		session.CurrentState = "IDLE"
		session.CurrentContext = "{}"
		return joinTransition(route.Message, applyPickup(session, input))
	case RouteIntentOrders:
		session.ActiveAgent = consoleAgentOrders
		session.CurrentState = "ORDERVAL_START"
		session.CurrentContext = "{}"
		return joinTransition(route.Message, applyOrderVal(session, input))
	case RouteIntentUpdateData:
		session.ActiveAgent = consoleAgentUpdateData
		session.CurrentState = "UPDATE_START"
		session.CurrentContext = "{}"
		return joinTransition(route.Message, applyUpdateData(session, input))
	case RouteIntentSAC:
		session.ActiveAgent = consoleAgentSAC
		return joinTransition(route.Message, handleSAC(input))
	default:
		return route.Message
	}
}

func continueActiveFlow(session *consoleSession, input string) string {
	switch session.ActiveAgent {
	case consoleAgentCarta:
		return applyCarta(session, input)
	case consoleAgentDelivery:
		return applyDelivery(session, input)
	case consoleAgentPickup:
		return applyPickup(session, input)
	case consoleAgentOrders:
		return applyOrderVal(session, input)
	case consoleAgentUpdateData:
		return applyUpdateData(session, input)
	case consoleAgentSAC:
		reply := handleSAC(input)
		if strings.Contains(strings.ToLower(input), "gracias") || strings.Contains(strings.ToLower(input), "listo") {
			resetConsoleSession(session)
		}
		return reply
	default:
		resetConsoleSession(session)
		return "No tenia un flujo activo valido. Regresamos al menu principal."
	}
}

func applyCarta(session *consoleSession, input string) string {
	route := HandleRouter(input)

	switch route.Intent {
	case RouteIntentCarta, RouteIntentGreeting, RouteIntentUnknown:
		return fmt.Sprintf(
			"Veo que te interesa esto de la carta: %s\n\nSi quieres, te ayudo a continuar. Dime si lo prefieres a domicilio o para recoger en tienda. Tambien puedo mostrarte las sedes o el menu principal.",
			input,
		)
	case RouteIntentDelivery:
		session.ActiveAgent = consoleAgentDelivery
		session.Delivery = &DeliverySession{}
		session.CurrentState = ""
		session.CurrentContext = ""
		return joinTransition("Perfecto, continuemos con tu pedido a domicilio.", applyDelivery(session, ""))
	case RouteIntentPickup:
		session.ActiveAgent = consoleAgentPickup
		session.Delivery = nil
		session.CurrentState = "IDLE"
		session.CurrentContext = "{}"
		return joinTransition("Perfecto, continuemos con recogida en tienda.", applyPickup(session, ""))
	case RouteIntentOrders:
		session.ActiveAgent = consoleAgentOrders
		session.Delivery = nil
		session.CurrentState = "ORDERVAL_START"
		session.CurrentContext = "{}"
		return joinTransition("Claro, revisemos tus pedidos.", applyOrderVal(session, input))
	case RouteIntentUpdateData:
		session.ActiveAgent = consoleAgentUpdateData
		session.Delivery = nil
		session.CurrentState = "UPDATE_START"
		session.CurrentContext = "{}"
		return joinTransition("Con gusto te ayudo a actualizar tus datos.", applyUpdateData(session, input))
	case RouteIntentLocations:
		return joinTransition("Claro, te comparto las sedes disponibles.", renderStaticLocations(), "Si quieres pedir algo, despues me dices si lo prefieres a domicilio o para recoger.")
	case RouteIntentMainMenu:
		resetConsoleSession(session)
		return "Regresamos al menu principal.\n\nPuedes elegir domicilio, recoger en tienda, ver pedidos, actualizar datos, ver sedes o ver productos."
	case RouteIntentSAC:
		session.ActiveAgent = consoleAgentSAC
		session.Delivery = nil
		session.CurrentState = ""
		session.CurrentContext = ""
		return joinTransition("Te ayudo con soporte.", handleSAC(input))
	default:
		return "Te sigo ayudando con la carta. Dime si lo quieres a domicilio, para recoger en tienda, o si prefieres volver al menu principal."
	}
}

func applyDelivery(session *consoleSession, input string) string {
	if session.Delivery == nil {
		session.Delivery = &DeliverySession{}
	}

	resp := HandleDelivery(session.Delivery, input)
	session.Delivery = resp.SessionData

	if resp.NextState == StateOrderPlaced || resp.NextState == StateCancelled {
		message := strings.Join(resp.Messages, "\n") + "\n\nSi deseas, tambien puedes escribir menu principal para volver al inicio."
		resetConsoleSession(session)
		return message
	}

	return strings.Join(resp.Messages, "\n")
}

func applyPickup(session *consoleSession, input string) string {
	resp := HandlePickup(input, session.CurrentState, session.CurrentContext)
	session.CurrentState = resp.NextState
	session.CurrentContext = mustMarshalContext(resp.NewContext)

	if resp.NextState == "FINISHED" || resp.NextState == "IDLE" {
		message := resp.Message + "\n\nTambien puedes escribir menu principal o contarme que necesitas."
		resetConsoleSession(session)
		return message
	}

	return resp.Message
}

func applyOrderVal(session *consoleSession, input string) string {
	resp := HandleOrderVal(input, session.CurrentState, session.CurrentContext)
	session.CurrentState = resp.NextState
	session.CurrentContext = mustMarshalContext(resp.NewContext)

	if resp.NextState == "IDLE" {
		message := resp.Message
		resetConsoleSession(session)
		return message
	}

	return resp.Message
}

func applyUpdateData(session *consoleSession, input string) string {
	resp := HandleUpdateData(input, session.CurrentState, session.CurrentContext)
	session.CurrentState = resp.NextState
	session.CurrentContext = mustMarshalContext(resp.NewContext)

	if resp.NextState == "IDLE" {
		message := resp.Message + "\n\nSi necesitas algo mas, tambien puedes escribirlo libremente."
		resetConsoleSession(session)
		return message
	}

	return resp.Message
}

func handleSAC(input string) string {
	apiKey := resolveSACKey()
	if apiKey == "" {
		return fmt.Sprintf(
			"Entiendo tu solicitud de soporte: %s\n\nPuedo ayudarte a registrar una PQRS de forma inicial. Configura AGENT_SAC_KEY o GROQ_API_KEY para obtener una respuesta especializada. Si prefieres, describe tu caso con mas detalle o escribe menu principal para volver.",
			input,
		)
	}

	client := appinternal.NewGroqClient(apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	reply, err := client.Chat(ctx, []appinternal.AIMessage{
		{Role: "system", Content: sacSystemPrompt(sacBusinessType())},
		{Role: "user", Content: input},
	})
	if err != nil {
		return fmt.Sprintf(
			"Entiendo tu solicitud de soporte: %s\n\nNo pude consultar al agente SAC en este momento (%v). Describe tu caso con mas detalle o escribe menu principal para volver.",
			input,
			err,
		)
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fmt.Sprintf(
			"Entiendo tu solicitud de soporte: %s\n\nNo recibi una respuesta util del agente SAC. Intenta reformular tu caso o escribe menu principal para volver.",
			input,
		)
	}

	return reply
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
	return fmt.Sprintf(`Eres un Especialista de Servicio al Cliente (SAC) de nivel 2 para una empresa de %s.
Tu objetivo es gestionar Peticiones, Quejas y Reclamos (PQR) de forma tecnica, resolutiva y empatica.
Tu prioridad es la solucion del problema, no la charla trivial.

Directrices de comportamiento:
- Claridad tecnica: resuelve problemas especificos de %s. Ve al grano.
- Estructura obligatoria: divide tu respuesta en VALIDACION, DIAGNOSTICO y PLAN DE ACCION.
- Tono: profesional y eficiente.
- Proactividad: proporciona pasos tecnicos o administrativos de inmediato.

Protocolo:
1. Recepcion y categorizacion (Peticion, Queja o Reclamo).
2. Validacion del contexto.
3. Resolucion guiada con instrucciones numeradas.
4. Cierre con numero de radicado simulado y tiempo de respuesta.

Restricciones:
- no sobre pasar los 155 a 355 caracteres en los mensajes.
- Prohibido contenido que no sea de soporte para %s.
- No inventes politicas imposibles de cumplir.
- Si falta informacion, pide solo los datos minimos necesarios para continuar.`, tipoDeNegocio, tipoDeNegocio, tipoDeNegocio)
}

func renderStaticMenu() string {
	return "Menu disponible hoy:\n1. Hamburguesa Clasica - $18.000\n2. Combo BBQ - $27.000\n3. Papas Medianas - $7.000\n4. Gaseosa 400ml - $5.000\n\nSi deseas, tambien puedes escribirme lo que necesites y con gusto te ayudare."
}

func renderStaticLocations() string {
	return "Sedes disponibles:\n1. Centro Comercial Andino\n2. Portal Norte\n3. Salitre Plaza\n\nSi ninguna opcion te sirve, tambien puedes escribirme lo que necesites y con gusto te ayudare."
}

func joinTransition(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "\n\n")
}

func mustMarshalContext(ctx map[string]string) string {
	if ctx == nil {
		return "{}"
	}
	raw, err := json.Marshal(ctx)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func resetConsoleSession(session *consoleSession) {
	session.ActiveAgent = consoleAgentNone
	session.CurrentState = ""
	session.CurrentContext = ""
	session.Delivery = nil
}
