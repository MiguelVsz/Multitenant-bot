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

	// ── Salidas forzadas: siempre funcionan, en cualquier agente ─────────────────
	// Solo menu_principal y confirm_cancel pueden cambiar/cerrar un agente activo.
	// Esto evita que botones de mensajes viejos cambien el flujo accidentalmente.
	wantsReset := strings.Contains(textNorm, "finalizar") ||
		strings.Contains(textNorm, "reiniciar") ||
		strings.Contains(textNorm, "reset") ||
		strings.Contains(textNorm, "empezar de nuevo") ||
		strings.Contains(textNorm, "nueva sesion")

	isHardExit := wantsReset ||
		textNorm == "confirm_cancel" ||
		textNorm == "menu_principal" ||
		textNorm == "menu principal" ||
		textNorm == "menú principal"

	// ── Salidas suaves: solo cuando NO hay agente activo ─────────────────────────
	// Cuando hay un agente, los botones de otros flujos van AL agente (no lo reemplazan).
	isSoftExit := !isHardExit && activeAgent == "" && (
		strings.HasPrefix(textNorm, "menu_") ||
		textNorm == "confirm_addr_yes" ||
		textNorm == "confirm_addr_no" ||
		strings.Contains(textNorm, "volver") ||
		strings.Contains(textNorm, "salir") ||
		strings.Contains(textNorm, "cancelar"))

	wantsExit := isHardExit || isSoftExit

	if wantsExit {
		activeAgent = ""
		delete(session.Metadata, "active_agent")
		if wantsReset {
			session.History = []models.AIMessage{}
			delete(session.Metadata, "orderval_state")
			delete(session.Metadata, "orderval_context")
			delete(session.Metadata, "registration_state")
			delete(session.Metadata, "data_treatment_accepted")
		}
	}

	// ── 1. Cargar cliente de BD PRIMERO para sincronizar metadata ──────────────
	customer, err := h.repo.GetCustomerByPhone(ctx, tenant.ID, msg.From)
	if err != nil {
		return err
	}

	if customer != nil {
		session.Metadata["customer_id"] = customer.ID
		session.Metadata["customer_name"] = customer.Name
		if val, ok := customer.Metadata["data_treatment_accepted"].(string); ok && val != "" {
			session.Metadata["data_treatment_accepted"] = val
		}
		if val, ok := customer.Metadata["address"].(string); ok && val != "" {
			session.Metadata["customer_address"] = val
		}
	}

	// ── 2. Verificar consentimiento de datos ───────────────────────────────────
	accepted, _ := session.Metadata["data_treatment_accepted"].(string)
	if accepted != "si" {
		switch textNorm {
		case "accepted":
			session.Metadata["data_treatment_accepted"] = "si"
			if customerID, ok := session.Metadata["customer_id"].(string); ok && customerID != "" {
				_ = h.repo.UpdateCustomerMetadata(ctx, tenant.ID, msg.From, session.Metadata)
			}
			if customer != nil {
				return h.sendMainMenu(ctx, tenant, session, msg, "¡Bienvenido de vuelta a "+tenant.Name+" 🍕!")
			}
		case "declined":
			aiReply = "Entendido. Sin la aceptación del tratamiento de datos no podemos continuar. Si cambias de opinión, escríbenos. ¡Hasta luego!"
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
		default:
			if customer == nil {
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
		}
	}

	// ── 3. Flujo de registro (solo si el cliente no existe en BD) ──────────────
	if customer == nil {
		regState, _ := session.Metadata["registration_state"].(string)
		switch regState {
		case "", "AWAITING_NAME":
			if regState == "" {
				session.Metadata["registration_state"] = "AWAITING_NAME"
				aiReply = "¡Veo que eres nuevo! Para brindarte una mejor atención, ¿podrías decirme tu nombre completo?"
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			session.Metadata["customer_name"] = msg.Text
			session.Metadata["registration_state"] = "AWAITING_EMAIL"
			aiReply = fmt.Sprintf("¡Gusto en conocerte, %s! ¿Cuál es tu correo electrónico para confirmaciones?", msg.Text)
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)

		case "AWAITING_EMAIL":
			session.Metadata["customer_email"] = strings.TrimSpace(msg.Text)
			session.Metadata["registration_state"] = "AWAITING_ADDRESS"
			aiReply = "¡Perfecto! ¿Cuál es tu dirección principal para domicilios? (Ej: Calle 123 #45-67)"
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)

		case "AWAITING_ADDRESS":
			address := strings.TrimSpace(msg.Text)
			session.Metadata["pending_address"] = address
			session.Metadata["registration_state"] = "CONFIRMING_ADDRESS"
			buttons := []models.InteractiveButton{
				{ID: "confirm_addr_yes", Title: "✅ Sí, es correcta"},
				{ID: "confirm_addr_no", Title: "✏️ Cambiar dirección"},
			}
			_ = SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
				"Confirmar Dirección",
				fmt.Sprintf("Tu dirección registrada será:\n📍 *%s*\n\n¿Es correcta?", address),
				"Selecciona una opción", buttons)
			return h.sessions.Save(ctx, session)

		case "CONFIRMING_ADDRESS":
			if textNorm == "confirm_addr_yes" {
				address, _ := session.Metadata["pending_address"].(string)
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
				delete(session.Metadata, "pending_address")
				_ = h.repo.UpdateCustomerMetadata(ctx, tenant.ID, msg.From, session.Metadata)
				return h.sendMainMenu(ctx, tenant, session, msg, "¡Registro completado! Bienvenido a "+tenant.Name+" 🍕")
			}
			session.Metadata["registration_state"] = "AWAITING_ADDRESS"
			aiReply = "Sin problema. ¿Cuál es tu dirección correcta para domicilios?"
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
		}
		return h.sessions.Save(ctx, session)
	}

	// ── 4. Enrutar al agente activo ────────────────────────────────────────────
	switch activeAgent {
	case "orderval":
		// Si recibe un botón de otro flujo, redirigir al menú principal
		if strings.HasPrefix(textNorm, "menu_") {
			return h.sendMainMenu(ctx, tenant, session, msg, "Para consultar otra opción, seleccionándola desde aquí:")
		}
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
		// Comandos de menú de otros flujos: el SAC explica cómo cambiar
		if strings.HasPrefix(textNorm, "menu_") {
			aiReply = "Estoy aquí en *Soporte (PQR)* para ayudarte con tu consulta 💬\n\nSi quieres explorar otra opción del menú, ve al *Menú Principal*."
		} else if textNorm == "" {
			aiReply = "🎧 *Soporte y PQR*\n\nEstoy aquí para ayudarte. Cuéntame tu consulta, queja o sugerencia."
		} else {
			aiReply = agents.HandleSAC(msg.Text)
		}
		// SAC: siempre con botón de menú principal
		err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
			"", aiReply, "", []models.InteractiveButton{
				{ID: "menu_principal", Title: "🏠 Menú Principal"},
			})
		if err != nil {
			h.log.Error("failed to send sac button", "err", err)
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
		}
		session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
		return h.sessions.Save(ctx, session)
	case "delivery":
		var delSession agents.DeliverySession
		if dsVal := session.Metadata["delivery_context"]; dsVal != nil {
			if dsStr, ok := dsVal.(string); ok && dsStr != "" {
				_ = json.Unmarshal([]byte(dsStr), &delSession)
			}
		}
		delSession.PhoneNumber = msg.From
		delSession.CustomerID, _ = session.Metadata["customer_id"].(string)

		// Solo inyectar dirección registrada si NO está ya en el contexto de entrega
		if delSession.Address == "" {
			if addr, ok := session.Metadata["customer_address"].(string); ok && addr != "" {
				delSession.Address = addr
			}
		}

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
			session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
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
			// Si el agente canceló internamente (sin mensaje), ir al menú principal
			if aiReply == "" {
				return h.sendMainMenu(ctx, tenant, session, msg, "Pedido cancelado. ¿En qué más podemos ayudarte?")
			}
		}
	case "pickup":
		var pickSession map[string]string
		if psVal := session.Metadata["pickup_context"]; psVal != nil {
			_ = json.Unmarshal([]byte(psVal.(string)), &pickSession)
		}
		if pickSession == nil {
			pickSession = make(map[string]string)
		}
		if pickSession["customer_id"] == "" {
			if cid, ok := session.Metadata["customer_id"].(string); ok {
				pickSession["customer_id"] = cid
			}
		}

		// Obtener zonas y productos de la BD para el agente pickup
		zones, _ := h.repo.GetCoverageZones(ctx, tenant.ID)
		pickProducts, _ := h.repo.GetProducts(ctx, tenant.ID)
		pickState, _ := session.Metadata["pickup_state"].(string)
		resp := agents.HandlePickup(msg.Text, pickState, mustMarshalContext(pickSession), session.History, zones, pickProducts)

		aiReply = resp.Message
		session.Metadata["pickup_state"] = resp.NextState
		pkBytes, _ := json.Marshal(resp.NewContext)
		session.Metadata["pickup_context"] = string(pkBytes)

		if resp.NextState == "FINISHED" {
			customerID, _ := session.Metadata["customer_id"].(string)

			var cart []models.OrderItem
			if cStr := pickSession["cart"]; cStr != "" {
				json.Unmarshal([]byte(cStr), &cart)
			}
			var total float64
			for _, item := range cart {
				total += item.Subtotal
			}

			order := &models.Order{
				TenantID:        tenant.ID,
				CustomerID:      customerID,
				OrderType:       "pickup",
				Status:          "pending",
				DeliveryAddress: pickSession["store"], // Guardamos la sede en delivery_address
				Subtotal:        total,
				Total:           total,
				PaymentMethod:   "En tienda",
				Metadata:        map[string]interface{}{"notes": "Recogida"},
				Items:           cart,
			}
			if err := h.repo.CreateOrder(ctx, order); err != nil {
				h.log.Error("failed to create pickup order", "err", err)
			}
		}

		if resp.NextState == "FINISHED" || resp.NextState == "IDLE" {
			delete(session.Metadata, "active_agent")
			delete(session.Metadata, "pickup_context")
			delete(session.Metadata, "pickup_state")
			if aiReply == "" {
				return h.sendMainMenu(ctx, tenant, session, msg, "Recogida cancelada. ¿En qué más podemos ayudarte?")
			}
		}

		if resp.ShowZoneList {
			if len(zones) == 0 {
				aiReply = "No hay sedes configuradas actualmente."
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			var rows []ListRow
			for _, z := range zones {
				title := z.Name
				if len(title) > 24 { title = title[:24] }
				rows = append(rows, ListRow{
					ID:          title, // Lo que escribirá el cliente si presiona la opción en la lista
					Title:       title,
					Description: "Seleccionar esta sede",
				})
			}
			sections := []ListSection{{Title: "📍 Puntos de Recogida", Rows: rows}}
			err := SendWhatsAppList(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
				"Elegir Sede", aiReply, "Elige una opción:", "Ver Opciones", sections)
			if err != nil {
				h.log.Error("failed to send pickup zone list", "err", err)
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
			return h.sessions.Save(ctx, session)
		}

		if len(resp.Buttons) > 0 {
			err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
				"", aiReply, "", resp.Buttons)
			if err != nil {
				h.log.Error("failed to send pickup buttons", "err", err)
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
			return h.sessions.Save(ctx, session)
		}
	case "update_data":
		var udSession map[string]string
		if udVal := session.Metadata["update_data_context"]; udVal != nil {
			_ = json.Unmarshal([]byte(udVal.(string)), &udSession)
		}
		if udSession == nil {
			udSession = make(map[string]string)
		}
		if udSession["customer_id"] == "" {
			if cid, ok := session.Metadata["customer_id"].(string); ok {
				udSession["customer_id"] = cid
			}
		}

		udState, _ := session.Metadata["update_data_state"].(string)
		resp := agents.HandleUpdateData(msg.Text, udState, mustMarshalContext(udSession))

		// UPDATE_APPLY: hacer el update real en la BD
		if resp.NextState == agents.StateUpdateApply {
			fieldToUpdate := udSession["field_to_update"]
			newValue := udSession["new_value"]
			fieldLabel := udSession["field_label"]
			customerID := udSession["customer_id"]

			moreBtn := []models.InteractiveButton{
				{ID: "upd_field_1", Title: "1 Nombre"},
				{ID: "upd_field_4", Title: "4 Direccion"},
				{ID: "menu_principal", Title: "Menu Principal"},
			}
			if err := h.repo.UpdateCustomerField(ctx, tenant.ID, customerID, fieldToUpdate, newValue); err != nil {
				h.log.Error("failed to update customer field", "field", fieldToUpdate, "err", err)
				aiReply = fmt.Sprintf("Lo siento, no pudimos actualizar tu *%s*. Por favor intenta mas tarde.", fieldLabel)
			} else {
				aiReply = fmt.Sprintf("Tu *%s* fue actualizado a *%s*. Deseas modificar otro dato?", fieldLabel, newValue)
				session.Metadata["update_data_state"] = agents.StateUpdateSelectField
				if fieldToUpdate == "name" {
					session.Metadata["customer_name"] = newValue
				} else if fieldToUpdate == "address" {
					session.Metadata["customer_address"] = newValue
				}
			}
			udBytes, _ := json.Marshal(resp.NewContext)
			session.Metadata["update_data_context"] = string(udBytes)
			_ = SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken, "", aiReply, "", moreBtn)
			session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
			return h.sessions.Save(ctx, session)
		}

		aiReply = resp.Message
		session.Metadata["update_data_state"] = resp.NextState
		udBytes, _ := json.Marshal(resp.NewContext)
		session.Metadata["update_data_context"] = string(udBytes)

		if resp.NextState == "FINISHED" || resp.NextState == "IDLE" {
			delete(session.Metadata, "active_agent")
			delete(session.Metadata, "update_data_context")
			delete(session.Metadata, "update_data_state")
		}

		if len(resp.Buttons) > 0 {
			err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
				"", aiReply, "", resp.Buttons)
			if err != nil {
				h.log.Error("failed to send update_data buttons", "err", err)
				return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
			}
			session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
			return h.sessions.Save(ctx, session)
		}
	default:
		textTrim := strings.TrimSpace(msg.Text)
		var route agents.RouterResponse

		// Mapeo de botones/lista a intents
		switch textNorm {
		case "menu_domicilio":
			session.Metadata["active_agent"] = "delivery"
			delSession := &agents.DeliverySession{State: agents.StateDeliveryIdle}
			if addr, ok := session.Metadata["customer_address"].(string); ok {
				delSession.Address = addr
			}
			products, _ := h.repo.GetProducts(ctx, tenant.ID)
			resp := agents.HandleDelivery(ctx, delSession, "", "", session.History, products)
			aiReply = resp.Message
			
			// GUARDAR EL ESTADO MUTADO
			dsBytes, _ := json.Marshal(resp.NewSession)
			session.Metadata["delivery_context"] = string(dsBytes)

			if len(resp.Buttons) > 0 {
				_ = SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
					"", aiReply, "", resp.Buttons)
				return h.sessions.Save(ctx, session)
			}
			return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
		case "menu_recoger":
			route.Intent = agents.RouteIntentPickup
		case "menu_update":
			route.Intent = agents.RouteIntentUpdateData
		case "menu_1":
			route.Intent = agents.RouteIntentCarta
		case "menu_2":
			route.Intent = agents.RouteIntentLocations
		case "menu_3":
			route.Intent = agents.RouteIntentOrders
		case "menu_4":
			route.Intent = agents.RouteIntentSAC
		case "menu_principal":
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
				case "1":
					route.Intent = agents.RouteIntentCarta
				case "2":
					route.Intent = agents.RouteIntentLocations
				case "3":
					route.Intent = agents.RouteIntentOrders
				case "4":
					route.Intent = agents.RouteIntentSAC
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
				aiReply = "📦 No tienes órdenes activas en este momento.\n\n¿Deseas hacer un nuevo pedido?"
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
				aiReply = "Lo siento, hubo un problema al cargar la carta. Intenta de nuevo más tarde."
			} else if len(products) == 0 {
				aiReply = "Actualmente no tenemos productos disponibles en la carta."
			} else {
				// Detectar si el usuario quiere una recomendación IA
				inputLower := strings.ToLower(strings.TrimSpace(msg.Text))
				isRecomendacion := strings.Contains(inputLower, "recomi") ||
					strings.Contains(inputLower, "sugi") ||
					strings.Contains(inputLower, "qu") && strings.Contains(inputLower, "pedir") ||
					strings.Contains(inputLower, "qu") && strings.Contains(inputLower, "elijo")

				if isRecomendacion && len(products) > 0 {
					// Usar IA para recomendar producto
					var productList strings.Builder
					for _, p := range products {
						productList.WriteString(fmt.Sprintf("- %s: $%.0f", p.Name, p.Price))
						if p.Description != nil && *p.Description != "" {
							productList.WriteString(" (" + *p.Description + ")")
						}
						productList.WriteString("\n")
					}
					aiReply = agents.AskAIRecommendation(msg.Text, productList.String(), tenant.Name)
					buttons := []models.InteractiveButton{
						{ID: "menu_domicilio", Title: "🛵 Pedir a Domicilio"},
						{ID: "menu_recoger", Title: "🥡 Recoger en Tienda"},
						{ID: "menu_principal", Title: "🏠 Menú Principal"},
					}
					err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
						"Recomendación", aiReply, "", buttons)
					if err != nil {
						return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
					}
					session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
					return h.sessions.Save(ctx, session)
				}

				var sb strings.Builder
				sb.WriteString("🍕 *CARTA " + strings.ToUpper(tenant.Name) + "* 🍕\n")
				sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\n\n")

				hasPizzas := false
				for _, p := range products {
					if strings.Contains(strings.ToLower(p.Name), "pizza") {
						if !hasPizzas {
							sb.WriteString("🔥 *PIZZAS ARTESANALES*\n")
							sb.WriteString("─────────────────────\n")
							hasPizzas = true
						}
						sb.WriteString(fmt.Sprintf("🍕 *%s*\n", p.Name))
						if p.Description != nil && *p.Description != "" {
							sb.WriteString(fmt.Sprintf("   ╰ _%s_\n", *p.Description))
						}
						sb.WriteString(fmt.Sprintf("   💰 *%s*\n\n", formatPrice(p.Price)))
					}
				}

				hasOther := false
				for _, p := range products {
					if !strings.Contains(strings.ToLower(p.Name), "pizza") {
						if !hasOther {
							sb.WriteString("🥤 *COMPLEMENTOS Y BEBIDAS*\n")
							sb.WriteString("─────────────────────\n")
							hasOther = true
						}
						sb.WriteString(fmt.Sprintf("🔸 *%s*\n", p.Name))
						if p.Description != nil && *p.Description != "" {
							sb.WriteString(fmt.Sprintf("   ╰ _%s_\n", *p.Description))
						}
						sb.WriteString(fmt.Sprintf("   💰 *%s*\n\n", formatPrice(p.Price)))
					}
				}

				sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\n")
				sb.WriteString("✍️ *Escribe el nombre del producto* para iniciar tu pedido.")

				buttons := []models.InteractiveButton{
					{ID: "menu_domicilio", Title: "🛵 Pedir a Domicilio"},
					{ID: "menu_recoger", Title: "🥡 Recoger en Tienda"},
					{ID: "menu_principal", Title: "🏠 Menú Principal"},
				}
				err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
					"Carta "+tenant.Name, sb.String(), "", buttons)
				if err != nil {
					h.log.Error("failed to send carta buttons", "err", err)
					return h.finalizeMessage(ctx, tenant, session, msg, sb.String())
				}
				session.History = append(session.History, models.AIMessage{Role: "assistant", Content: sb.String()})
				return h.sessions.Save(ctx, session)
			}
		case agents.RouteIntentLocations:
			zones, err := h.repo.GetCoverageZones(ctx, tenant.ID)
			if err != nil {
				h.log.Error("failed to get coverage zones", "err", err)
				aiReply = "Lo siento, hubo un error al cargar nuestros puntos de venta."
			} else if len(zones) == 0 {
				aiReply = "No hay puntos de venta o zonas configuradas actualmente."
			} else {
				var sb strings.Builder
				sb.WriteString("📍 *SEDES Y COBERTURA*\n")
				sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\n\n")
				for _, z := range zones {
					sb.WriteString(fmt.Sprintf("🏪 *%s*\n", z.Name))
					sb.WriteString(fmt.Sprintf("   • Orden mínima: %s\n", formatPrice(z.MinOrder)))
					sb.WriteString(fmt.Sprintf("   • Domicilio: %s\n\n", formatPrice(z.DeliveryFee)))
				}
				buttons := []models.InteractiveButton{
					{ID: "menu_principal", Title: "🏠 Menú Principal"},
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
			// No pasar el disparador (ej "menu_4") a la IA para evitar alucinaciones
			aiReply = agents.HandleSAC("")
		case agents.RouteIntentPickup:
			session.Metadata["active_agent"] = "pickup"
			initCtx := map[string]string{}
			if cid, ok := session.Metadata["customer_id"].(string); ok {
				initCtx["customer_id"] = cid
			}
			pickZones, _ := h.repo.GetCoverageZones(ctx, tenant.ID)
			pickProds, _ := h.repo.GetProducts(ctx, tenant.ID)
			resp := agents.HandlePickup("", "IDLE", mustMarshalContext(initCtx), session.History, pickZones, pickProds)
			aiReply = resp.Message
			session.Metadata["pickup_state"] = resp.NextState
			pkBytes, _ := json.Marshal(resp.NewContext)
			session.Metadata["pickup_context"] = string(pkBytes)
			if len(resp.Buttons) > 0 {
				err := SendWhatsAppButton(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken,
					"", aiReply, "", resp.Buttons)
				if err != nil {
					h.log.Error("failed to send pickup initial buttons", "err", err)
					return h.finalizeMessage(ctx, tenant, session, msg, aiReply)
				}
				session.History = append(session.History, models.AIMessage{Role: "assistant", Content: aiReply})
				return h.sessions.Save(ctx, session)
			}
		case agents.RouteIntentUpdateData:
			session.Metadata["active_agent"] = "update_data"
			initCtx := map[string]string{}
			if cid, ok := session.Metadata["customer_id"].(string); ok {
				initCtx["customer_id"] = cid
			}
			resp := agents.HandleUpdateData("", "UPDATE_START", mustMarshalContext(initCtx))
			aiReply = resp.Message
			session.Metadata["update_data_state"] = resp.NextState
			udBytes, _ := json.Marshal(resp.NewContext)
			session.Metadata["update_data_context"] = string(udBytes)
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
	bodyText = "━━━━━━━━━━━━━━━\n" + bodyText + "\n━━━━━━━━━━━━━━━"
	
	sections := []ListSection{
		{
			Title: "🛎️ Servicios",
			Rows: []ListRow{
				{ID: "menu_domicilio", Title: "🛵 Pedido a Domicilio", Description: "Recibe tu pizza caliente en casa"},
				{ID: "menu_recoger", Title: "🥡 Recoger en Tienda", Description: "Pide ahora y retira en el local"},
			},
		},
		{
			Title: "📋 Consulta",
			Rows: []ListRow{
				{ID: "menu_1", Title: "🍕 Ver Carta Premium", Description: "Mira nuestras mejores pizzas y combos"},
				{ID: "menu_2", Title: "📍 Sedes y Ubicación", Description: "Encuentra tu punto Don Pepe más cercano"},
			},
		},
		{
			Title: "⚙️ Gestión",
			Rows: []ListRow{
				{ID: "menu_3", Title: "📦 Mis Órdenes", Description: "Rastrea tus pedidos activos"},
				{ID: "menu_update", Title: "👤 Mi Perfil", Description: "Actualiza tus datos y direcciones"},
				{ID: "menu_4", Title: "🎧 Soporte (PQR)", Description: "¿Necesitas ayuda? Habla con nosotros"},
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
