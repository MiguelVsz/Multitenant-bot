package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"multi-tenant-bot/internal/models"

	"github.com/joho/godotenv"
)

type consoleAgent string

const (
	consoleAgentNone       = ""
	consoleAgentCarta      = "CARTA"
	consoleAgentDelivery   = "DELIVERY"
	consoleAgentPickup     = "PICKUP"
	consoleAgentOrders     = "ORDERS"
	consoleAgentUpdateData = "UPDATE_DATA"
	consoleAgentSAC        = "SAC"
)

type consoleSession struct {
	ActiveAgent    string
	CurrentState   string
	CurrentContext string
	Delivery       *DeliverySession
}

func RunConsoleChat() error {
	_ = godotenv.Load()

	scanner := bufio.NewScanner(os.Stdin)
	session := &consoleSession{}

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
			fmt.Println("Sesion finalizada.")
			return nil
		case "menu principal", "menu", "inicio", "reiniciar":
			resetConsoleSession(session)
			fmt.Println("Bot:")
			fmt.Println("  Regresamos al menu principal.")
			fmt.Println("  Puedes elegir domicilio, recoger en tienda, ver pedidos, actualizar datos, ver sedes o ver productos.")
			continue
		}

		reply := handleConsoleInput(session, input)
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
		return joinTransition(route.Message, HandleSAC(input))
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
		reply := HandleSAC(input)
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
		return joinTransition("Te ayudo con soporte.", HandleSAC(input))
	default:
		return "Te sigo ayudando con la carta. Dime si lo quieres a domicilio, para recoger en tienda, o si prefieres volver al menu principal."
	}
}

func applyDelivery(session *consoleSession, input string) string {
	if session.Delivery == nil {
		session.Delivery = &DeliverySession{}
	}

	resp := HandleDelivery(context.Background(), session.Delivery, input, input, []models.AIMessage{}, []models.Product{})
	session.Delivery = resp.NewSession

	if resp.NextState == StateDeliveryPlaced || resp.NextState == StateDeliveryIdle {
		message := resp.Message + "\n\nSi deseas, tambien puedes escribir menu principal para volver al inicio."
		resetConsoleSession(session)
		return message
	}

	return resp.Message
}

func applyPickup(session *consoleSession, input string) string {
	resp := HandlePickup(input, session.CurrentState, session.CurrentContext, []models.CoverageZone{})
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
	resp := HandleOrderVal(input, session.CurrentState, session.CurrentContext, []OrderDetail{}, "")
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
