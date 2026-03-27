package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"multi-tenant-bot/internal/agents"
	"multi-tenant-bot/internal/models"
)

type WebhookConfig struct {
	VerifyToken string
	AppSecret   string
}

type WebhookHandler struct {
	cfg      WebhookConfig
	repo     *Repository
	sessions *SessionStore
	ai       AIClient
	log      *slog.Logger
}

func NewWebhookHandler(
	cfg WebhookConfig,
	repo *Repository,
	sessions *SessionStore,
	ai AIClient,
	log *slog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		cfg:      cfg,
		repo:     repo,
		sessions: sessions,
		ai:       ai,
		log:      log,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleVerification(w, r)
	case http.MethodPost:
		h.handleIncomingMessage(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *WebhookHandler) handleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.cfg.VerifyToken {
		h.log.Info("webhook verified by Meta")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(challenge))
		return
	}

	h.log.Warn("webhook verification failed", "mode", mode)
	writeError(w, http.StatusForbidden, "forbidden")
}

func (h *WebhookHandler) handleIncomingMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if signature != "" && h.cfg.AppSecret != "" && !validateMetaSignature(body, signature, h.cfg.AppSecret) {
		h.log.Warn("invalid webhook signature")
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var payload CloudAPIWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	for _, msg := range ExtractIncomingMessages(&payload) {
		if err := h.processMessage(r.Context(), msg); err != nil {
			h.log.Error("process webhook message", "err", err, "from", msg.From, "message_id", msg.MessageID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *WebhookHandler) processMessage(ctx context.Context, msg IncomingMessage) error {
	tenant, err := h.repo.ResolveTenantByPhoneNumberID(ctx, msg.PhoneNumberID)
	if err != nil {
		if errors.Is(err, ErrTenantNotFound) {
			h.log.Warn("tenant not found for incoming message", "phone_number_id", msg.PhoneNumberID)
			return nil
		}
		return err
	}

	session, err := h.sessions.Load(ctx, tenant.ID, msg.From)
	if err != nil {
		return err
	}

	if session.Metadata == nil {
		session.Metadata = make(map[string]interface{})
	}
	session.Metadata["last_message_id"] = msg.MessageID
	session.Metadata["last_message_type"] = msg.Type

	session.History = append(session.History, models.AIMessage{
		Role:    "user",
		Content: msg.Text,
	})

	var aiReply string
	
	textLower := strings.ToLower(strings.TrimSpace(msg.Text))
	textNorm := strings.ReplaceAll(textLower, "ú", "u")
	textNorm = strings.ReplaceAll(textNorm, "í", "i")
	
	activeAgent, _ := session.Metadata["active_agent"].(string)

	// Detección global de intenciones de regresar al menú o navegar fuera de un agente.
	wantsReset := strings.Contains(textNorm, "finalizar") ||
		strings.Contains(textNorm, "reiniciar") ||
		strings.Contains(textNorm, "reset") ||
		strings.Contains(textNorm, "empezar de nuevo") ||
		strings.Contains(textNorm, "nueva sesion")

	wantsExit := wantsReset ||
		strings.Contains(textNorm, "menu principal") ||
		strings.Contains(textNorm, "volver") ||
		strings.Contains(textNorm, "salir") ||
		strings.Contains(textNorm, "cancelar") ||
		// En SAC, los números de menú también actúan como salida de emergencia.
		(activeAgent == "sac" && (textNorm == "1" || textNorm == "2" || textNorm == "3" || textNorm == "4" || strings.Contains(textNorm, "menu") || strings.Contains(textNorm, "opciones")))

	if wantsExit {
		activeAgent = ""
		delete(session.Metadata, "active_agent")
		if wantsReset {
			// Limpiar historial para empezar de cero
			session.History = []models.AIMessage{}
			// Limpiar estados específicos de agentes
			delete(session.Metadata, "orderval_state")
			delete(session.Metadata, "orderval_context")
			delete(session.Metadata, "registration_state")
			delete(session.Metadata, "data_treatment_accepted")
		}
	}

	// 1. Verificar consentimiento de datos
	accepted, _ := session.Metadata["data_treatment_accepted"].(string)
	if accepted != "si" {
		switch textNorm {
		case "accepted":
			session.Metadata["data_treatment_accepted"] = "si"
			// Si el cliente ya existe, persistir actualización inmediatamente
			if customerID, ok := session.Metadata["customer_id"].(string); ok && customerID != "" {
				_ = h.repo.UpdateCustomerMetadata(ctx, tenant.ID, msg.From, session.Metadata)
			}
		case "declined":
			aiReply = "Entendido. Sin la aceptación del tratamiento de datos no podemos continuar con la atención. Si cambias de opinión, escribe 'Hola'. ¡Hasta luego!"
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
		default:
			// Si el cliente ya aceptó en el pasado (metadata de DB), saltamos esto
			// Pero aquí aún no hemos cargado al cliente. 
			// Lo cargaremos antes para evitar preguntar de nuevo.
		}
	}

	// 2. Verificar si el usuario está registrado en BD
	customer, err := h.repo.GetCustomerByPhone(ctx, tenant.ID, msg.From)
	if err != nil {
		return err
	}

	if customer == nil {
		regState, _ := session.Metadata["registration_state"].(string)
		switch regState {
		case "", "AWAITING_NAME":
			if regState == "" {
				// Solo preguntamos consentimiento si el cliente NO existe en BD
				accepted, _ := session.Metadata["data_treatment_accepted"].(string)
				if accepted != "si" {
					buttons := []models.InteractiveButton{
						{ID: "accepted", Title: "✅ Sí, acepto"},
						{ID: "declined", Title: "❌ No acepto"},
					}
					_ = SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
						"Aviso de Privacidad",
						"¡Hola! Antes de comenzar, ¿estás de acuerdo con nuestro tratamiento de datos personales para procesar tus pedidos?",
						"Por favor selecciona una opción", buttons)
					return h.sessions.Save(ctx, session)
				}
				session.Metadata["registration_state"] = "AWAITING_NAME"
				aiReply = "¡Veo que eres nuevo! Para brindarte una mejor atención, ¿podrías decirme tu nombre completo?"
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			session.Metadata["customer_name"] = msg.Text
			session.Metadata["registration_state"] = "AWAITING_EMAIL"
			aiReply = fmt.Sprintf("¡Gusto en conocerte, %s! Ahora, ¿cuál es tu correo electrónico para enviarte las confirmaciones?", msg.Text)
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)

		case "AWAITING_EMAIL":
			session.Metadata["customer_email"] = strings.TrimSpace(msg.Text)
			session.Metadata["registration_state"] = "AWAITING_ADDRESS"
			aiReply = "¡Perfecto! Finalmente, ¿cuál es tu dirección principal para domicilios? (Ej: Calle 123 #45-67)"
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)

		case "AWAITING_ADDRESS":
			address := strings.TrimSpace(msg.Text)
			newCustomer := &models.Customer{
				TenantID:      tenant.ID,
				WhatsAppPhone: msg.From,
				Name:          session.Metadata["customer_name"].(string),
				Email:         session.Metadata["customer_email"].(string),
				Metadata: map[string]interface{}{
					"data_treatment_accepted": "si",
					"accepted_at":             time.Now().Format(time.RFC3339),
					"address":                 address,
				},
			}
			if err := h.repo.CreateCustomer(ctx, newCustomer); err != nil {
				h.log.Error("failed to create customer", "err", err)
				return err
			}
			session.Metadata["registration_state"] = "COMPLETED"
			session.Metadata["customer_id"] = newCustomer.ID
			session.Metadata["customer_address"] = address
			session.Metadata["data_treatment_accepted"] = "si"
			
			welcomeMsg := "¡Registro completado con éxito! Bienvenido a " + tenant.Name + " 🍕"
			return h.sendMainMenu(ctx, tenant, session, msg, welcomeMsg)
		}
	} else {
		session.Metadata["customer_id"] = customer.ID
		session.Metadata["customer_name"] = customer.Name
		// Sincronizar desde Metadata de DB
		if val, ok := customer.Metadata["data_treatment_accepted"].(string); ok {
			session.Metadata["data_treatment_accepted"] = val
		}
		if val, ok := customer.Metadata["address"].(string); ok {
			session.Metadata["customer_address"] = val
		}
	}

	switch activeAgent {
	case "orderval":
		records, err := h.repo.GetActiveOrdersByPhone(ctx, tenant.ID, msg.From)
		if err != nil {
			h.log.Error("error getting orders", "err", err)
		}
		orders := mapOrderRecords(records)
		historyJSON, _ := json.Marshal(session.History)

		ordervalState, _ := session.Metadata["orderval_state"].(string)
		ordervalContext, _ := session.Metadata["orderval_context"].(string)
		resp := agents.HandleOrderVal(msg.Text, ordervalState, ordervalContext, orders, string(historyJSON))
		aiReply = resp.Message
		session.Metadata["orderval_state"] = resp.NextState
		
		ctxBytes, _ := json.Marshal(resp.NewContext)
		session.Metadata["orderval_context"] = string(ctxBytes)

		if resp.NextState == "IDLE" {
			delete(session.Metadata, "active_agent")
		}
	case "sac":
		aiReply = agents.HandleSAC(msg.Text)
	case "delivery":
		var delSession agents.DeliverySession
		if dsVal := session.Metadata["delivery_context"]; dsVal != nil {
			if dsStr, ok := dsVal.(string); ok && dsStr != "" {
				_ = json.Unmarshal([]byte(dsStr), &delSession)
			}
		}
		delSession.PhoneNumber = msg.From
		delSession.CustomerID, _ = session.Metadata["customer_id"].(string)

		products, _ := h.repo.GetProducts(ctx, tenant.ID)
		resp := agents.HandleDelivery(ctx, &delSession, msg.Text, textNorm, session.History, products)
		
		aiReply = resp.Message
		dsBytes, _ := json.Marshal(resp.NewSession)
		session.Metadata["delivery_context"] = string(dsBytes)
		session.Metadata["active_agent"] = "delivery"

		if len(resp.Buttons) > 0 {
			err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
				"", aiReply, "", resp.Buttons)
			if err != nil {
				h.log.Error("failed to send delivery buttons", "err", err)
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			return h.sessions.Save(ctx, session)
		}

		switch resp.NextState {
		case agents.StateDeliveryPlaced:
			// PERSISTIR EN BD
			customerID, _ := session.Metadata["customer_id"].(string)
			order := &models.Order{
				TenantID:        tenant.ID,
				CustomerID:      customerID,
				OrderType:       "delivery",
				Status:          "pending",
				DeliveryAddress: resp.NewSession.Address,
				Subtotal:        resp.NewSession.Total,
				Total:           resp.NewSession.Total,
				PaymentMethod:   textNorm,
				Items:           resp.NewSession.Cart,
			}
			if err := h.repo.CreateOrder(ctx, order); err != nil {
				h.log.Error("failed to create order", "err", err)
			}
			delete(session.Metadata, "active_agent")
			delete(session.Metadata, "delivery_context")
			aiReply = "¡Orden guardada con éxito! " + aiReply
		case agents.StateDeliveryIdle:
			delete(session.Metadata, "active_agent")
			delete(session.Metadata, "delivery_context")
		}
	default:
		textTrim := strings.TrimSpace(msg.Text)
		var route agents.RouterResponse
		
		// Mapeo de botones/lista a intents
		switch textNorm {
		case "menu_domicilio":
			session.Metadata["active_agent"] = "delivery"
			// Iniciar flujo de domicilio directamente en AWAITING_ADDRESS
			delSession := &agents.DeliverySession{State: agents.StateDeliveryAwaitingAddress}
			dsBytes, _ := json.Marshal(delSession)
			session.Metadata["delivery_context"] = string(dsBytes)
			aiReply = "¡Excelente elección! Vamos a tomar tu pedido a domicilio. ¿A qué dirección lo enviamos?"
			return h.finalizeMessage( ctx, tenant, session, msg, aiReply)
		case "menu_1":
			route.Intent = agents.RouteIntentCarta
		case "menu_2":
			route.Intent = agents.RouteIntentLocations
		case "menu_3":
			route.Intent = agents.RouteIntentOrders
		case "menu_4":
			route.Intent = agents.RouteIntentSAC
		case "MENU_PRINCIPAL":
			route.Intent = agents.RouteIntentMainMenu
		case "confirm_cancel":
			// Resetear cualquier agente activo
			delete(session.Metadata, "active_agent")
			delete(session.Metadata, "delivery_context")
			aiReply = "Entendido. He cancelado tu solicitud actual."
			return h.sendMainMenu(ctx, tenant, session, msg, aiReply)
		default:
			if len(textTrim) == 1 && textTrim >= "1" && textTrim <= "4" {
				switch textTrim {
				case "1": route.Intent = agents.RouteIntentCarta
				case "2": route.Intent = agents.RouteIntentLocations
				case "3": route.Intent = agents.RouteIntentOrders
				case "4": route.Intent = agents.RouteIntentSAC
				}
			} else {
				route = agents.HandleRouter(msg.Text)
			}
		}

		switch route.Intent {
		case agents.RouteIntentGreeting, agents.RouteIntentMainMenu:
			bodyText := "¡Hola! Bienvenido a " + tenant.Name + " 🍕\n¿En qué podemos ayudarte hoy?"
			if route.Intent == agents.RouteIntentMainMenu {
				bodyText = "¿En qué más podemos ayudarte?"
			}
			return h.sendMainMenu(ctx, tenant, session, msg, bodyText)

		case agents.RouteIntentOrders:
			records, err := h.repo.GetActiveOrdersByPhone(ctx, tenant.ID, msg.From)
			if err != nil {
				h.log.Error("error getting orders", "err", err)
			}
			orders := mapOrderRecords(records)
			
			if len(orders) == 0 {
				aiReply = route.Message + "\n\nNo tienes órdenes activas en este momento."
			} else {
				session.Metadata["active_agent"] = "orderval"
				historyJSON, _ := json.Marshal(session.History)
				resp := agents.HandleOrderVal(msg.Text, "ORDERVAL_START", "{}", orders, string(historyJSON))
				aiReply = resp.Message
				session.Metadata["orderval_state"] = resp.NextState
				ctxBytes, _ := json.Marshal(resp.NewContext)
				session.Metadata["orderval_context"] = string(ctxBytes)
			}
		case agents.RouteIntentCarta:
			products, err := h.repo.GetProducts(ctx, tenant.ID)
			if err != nil {
				h.log.Error("failed to get products", "err", err)
				aiReply = route.Message + "\n\nLo siento, hubo un problema al cargar la carta. Intenta de nuevo más tarde."
			} else if len(products) == 0 {
				aiReply = route.Message + "\n\nActualmente no tenemos productos disponibles en la carta."
			} else {
				var sb strings.Builder
				sb.WriteString("✨ *NUESTRA CARTA SELECCIONADA* ✨\n\n")
				
				// Agrupar por categorías (si existieran, si no, solo listamos con estilo)
				sb.WriteString("🍕 *PIZZAS ARTESANALES*\n")
				for _, p := range products {
					if strings.Contains(strings.ToLower(p.Name), "pizza") {
						desc := ""
						if p.Description != nil && *p.Description != "" {
							desc = fmt.Sprintf(" _(%s)_", *p.Description)
						}
						sb.WriteString(fmt.Sprintf("• *%s*: %s%s\n", p.Name, formatPrice(p.Price), desc))
					}
				}
				
				sb.WriteString("\n🥤 *BEBIDAS Y OTROS*\n")
				for _, p := range products {
					if !strings.Contains(strings.ToLower(p.Name), "pizza") {
						sb.WriteString(fmt.Sprintf("• *%s*: %s\n", p.Name, formatPrice(p.Price)))
					}
				}

				sb.WriteString("\n¿Qué se te antoja hoy? Escribe el nombre del producto para comenzar tu pedido.")
				
				buttons := []models.InteractiveButton{
					{ID: "MENU_PRINCIPAL", Title: "🏠 Menú Principal"},
				}
				err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
					"Carta Pizzería", sb.String(), "", buttons)
				if err != nil {
					h.log.Error("failed to send carta buttons", "err", err)
					return h.finalizeMessage(ctx, tenant, session, msg, sb.String())
				}
				return h.sessions.Save(ctx, session)
			}
		case agents.RouteIntentLocations:
			zones, err := h.repo.GetCoverageZones(ctx, tenant.ID)
			if err != nil {
				h.log.Error("failed to get coverage zones", "err", err)
				aiReply = route.Message + "\n\nLo siento, hubo un error al cargar nuestros puntos de venta."
			} else if len(zones) == 0 {
				aiReply = route.Message + "\n\nNo hay puntos de venta o zonas configuradas actualmente."
			} else {
				var sb strings.Builder
				sb.WriteString(route.Message + "\n\n📍 *PUNTOS DE VENTA Y ZONAS*\n\n")
				for _, z := range zones {
					sb.WriteString(fmt.Sprintf("- %s (Min. orden: %s, Domicilio: %s)\n", z.Name, formatPrice(z.MinOrder), formatPrice(z.DeliveryFee)))
				}
				
				buttons := []models.InteractiveButton{
					{ID: "MENU_PRINCIPAL", Title: "🏠 Menú Principal"},
					{ID: "menu_domicilio", Title: "🛵 Pedir Domicilio"},
				}
				err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken, 
					"Sedes y Cobertura", sb.String(), "", buttons)
				
				if err != nil {
					h.log.Error("failed to send locations buttons", "err", err)
					return h.finalizeMessage(ctx, tenant, session, msg, sb.String())
				}
				return h.sessions.Save(ctx, session)
			}
	case agents.RouteIntentSAC:
			session.Metadata["active_agent"] = "sac"
			aiReply = route.Message + "\n\n" + agents.HandleSAC(msg.Text)
		default:
			aiReply = route.Message
		}
	}

	session.History = append(session.History, models.AIMessage{
		Role:    "assistant",
		Content: aiReply,
	})

	if err := SendWhatsAppMessage(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken, aiReply); err != nil {
		h.log.Error("failed to send whatsapp message", "err", err)
	}

	return h.sessions.Save(ctx, session)
}

func (h *WebhookHandler) finalizeMessage(ctx context.Context, tenant *models.Tenant, session *ConversationSession, msg IncomingMessage, aiReply string) error {
	session.History = append(session.History, models.AIMessage{
		Role:    "assistant",
		Content: aiReply,
	})

	if err := SendWhatsAppMessage(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken, aiReply); err != nil {
		h.log.Error("failed to send whatsapp message", "err", err)
	}

	return h.sessions.Save(ctx, session)
}

func (h *WebhookHandler) sendMainMenu(ctx context.Context, tenant *models.Tenant, session *ConversationSession, msg IncomingMessage, bodyText string) error {
	sections := []ListSection{
		{
			Title: "Menú Don Pepe",
			Rows: []ListRow{
				{ID: "menu_domicilio", Title: "🏠 Pedido a Domicilio", Description: "Haz tu pedido y recíbelo en casa"},
				{ID: "menu_1", Title: "🍕 Ver Carta Interactiva", Description: "Mira nuestros productos y combos"},
				{ID: "menu_2", Title: "📍 Sedes y Cobertura", Description: "Nuestros puntos físicos"},
				{ID: "menu_3", Title: "📦 Mis Órdenes", Description: "Estado de tus pedidos actuales"},
				{ID: "menu_4", Title: "🎧 Soporte (PQR)", Description: "Ayuda, quejas o sugerencias"},
			},
		},
	}

	err := SendWhatsAppList(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
		tenant.Name, bodyText, "Selecciona una opción", "Ver Menú 📋", sections)
	
	if err != nil {
		h.log.Error("failed to send menu list", "err", err)
		aiReply := bodyText + "\n\n1. 🍕 Carta\n2. 📍 Sedes\n3. 📦 Pedidos\n4. 🎧 Soporte"
		return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
	}

	session.History = append(session.History, models.AIMessage{
		Role:    "assistant",
		Content: bodyText + " [Interactive Menu]",
	})
	return h.sessions.Save(ctx, session)
}

func buildWelcomeMessage(cfg models.BotConfig) string {
	// ... (no se toca, se mantiene por fallback si falla la lista)

	var sb strings.Builder
	
	if cfg.WelcomeMessage != "" {
		sb.WriteString(cfg.WelcomeMessage + "\n\n")
	} else {
		sb.WriteString("¡Hola! ¿En qué puedo ayudarte hoy?\n\n")
	}

	sb.WriteString("Responde con el número de la opción que prefieras:\n")
	sb.WriteString("1. 🍕 Ver y pedir de la Carta\n")
	sb.WriteString("2. 📍 Ver Puntos de Venta (Sedes)\n")
	sb.WriteString("3. 📦 Revisar mis órdenes\n")
	sb.WriteString("4. 🎧 Servicio al Cliente (PQRS)\n")

	return sb.String()
}

func mapOrderRecords(records []models.OrderRecord) []agents.OrderDetail {
	var res []agents.OrderDetail
	for _, r := range records {
		var items []string
		for _, item := range r.Items {
			items = append(items, fmt.Sprintf("%d x %s", item.Quantity, item.Name))
		}
		res = append(res, agents.OrderDetail{
			ID:      r.ID,
			Status:  r.Status,
			Items:   items,
			Address: r.Address,
			Total:   fmt.Sprintf("$%.2f", r.Total),
		})
	}
	return res
}

func formatPrice(price float64) string {
	return fmt.Sprintf("$%.0f", price)
}
