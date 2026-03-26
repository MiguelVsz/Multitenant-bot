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

	"multi-tenant-bot/internal/agents"
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
		session.Metadata = make(map[string]string)
	}
	session.Metadata["last_message_id"] = msg.MessageID
	session.Metadata["last_message_type"] = msg.Type

	session.History = append(session.History, AIMessage{
		Role:    "user",
		Content: msg.Text,
	})

	var aiReply string
	
	textLower := strings.ToLower(strings.TrimSpace(msg.Text))
	textNorm := strings.ReplaceAll(textLower, "ú", "u")
	textNorm = strings.ReplaceAll(textNorm, "í", "i")
	
	activeAgent := session.Metadata["active_agent"]

	// Detección global de intenciones de regresar al menú o navegar fuera de un agente.
	wantsExit := strings.Contains(textNorm, "menu principal") ||
		strings.Contains(textNorm, "volver") ||
		strings.Contains(textNorm, "salir") ||
		strings.Contains(textNorm, "cancelar") ||
		// En SAC, los números de menú también actúan como salida de emergencia.
		(activeAgent == "sac" && (textNorm == "1" || textNorm == "2" || textNorm == "3" || textNorm == "4" || strings.Contains(textNorm, "menu") || strings.Contains(textNorm, "opciones")))

	if wantsExit {
		activeAgent = ""
		delete(session.Metadata, "active_agent")
	}

	if activeAgent == "orderval" {
		records, err := h.repo.GetActiveOrdersByPhone(ctx, tenant.ID, msg.From)
		if err != nil {
			h.log.Error("error getting orders", "err", err)
		}
		orders := mapOrderRecords(records)
		historyJSON, _ := json.Marshal(session.History)

		resp := agents.HandleOrderVal(msg.Text, session.Metadata["orderval_state"], session.Metadata["orderval_context"], orders, string(historyJSON))
		aiReply = resp.Message
		session.Metadata["orderval_state"] = resp.NextState
		
		ctxBytes, _ := json.Marshal(resp.NewContext)
		session.Metadata["orderval_context"] = string(ctxBytes)

		if resp.NextState == "IDLE" {
			delete(session.Metadata, "active_agent")
		}
	} else if activeAgent == "sac" {
		aiReply = agents.HandleSAC(msg.Text)
	} else {
		textTrim := strings.TrimSpace(msg.Text)
		var route agents.RouterResponse
		
		switch textTrim {
		case "1":
			route = agents.RouterResponse{Intent: agents.RouteIntentCarta, Message: "Excelente, te compartiré nuestra lista de productos."}
		case "2":
			route = agents.RouterResponse{Intent: agents.RouteIntentLocations, Message: "Claro, aquí tienes nuestras zonas de cobertura y puntos."}
		case "3":
			route = agents.RouterResponse{Intent: agents.RouteIntentOrders, Message: "Muy bien, revisemos el estado de tus órdenes."}
		case "4":
			route = agents.RouterResponse{Intent: agents.RouteIntentSAC, Message: "Entendido, te contactaremos con Soporte (PQR)."}
		default:
			route = agents.HandleRouter(msg.Text)
		}
		
		switch route.Intent {
		case agents.RouteIntentGreeting, agents.RouteIntentMainMenu:
			aiReply = buildWelcomeMessage(tenant.BotConfig)
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
				sb.WriteString(route.Message + "\n\n📋 *NUESTRA CARTA*\n\n")
				for i, p := range products {
					desc := ""
					if p.Description != nil && *p.Description != "" {
						desc = fmt.Sprintf(" - %s", *p.Description)
					}
					sb.WriteString(fmt.Sprintf("%d. %s: $%.0f%s\n", i+1, p.Name, p.Price, desc))
				}
				sb.WriteString("\n¿Qué deseas ordenar? Escribe el nombre del producto o elige otra opción escribiendo *menu principal*.")
				aiReply = sb.String()
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
					sb.WriteString(fmt.Sprintf("- %s (Min. orden: $%.0f, Domicilio: $%.0f)\n", z.Name, z.MinOrder, z.DeliveryFee))
				}
				sb.WriteString("\nSi necesitas algo más, escribe *menu principal*.")
				aiReply = sb.String()
			}
	case agents.RouteIntentSAC:
			session.Metadata["active_agent"] = "sac"
			aiReply = route.Message + "\n\n" + agents.HandleSAC(msg.Text)
		default:
			aiReply = route.Message
		}
	}

	session.History = append(session.History, AIMessage{
		Role:    "assistant",
		Content: aiReply,
	})

	if err := SendWhatsAppMessage(ctx, msg.PhoneNumberID, msg.From, tenant.WhatsAppToken, aiReply); err != nil {
		h.log.Error("failed to send whatsapp message", "err", err)
	}

	return h.sessions.Save(ctx, session)
}

func buildWelcomeMessage(cfg BotConfig) string {
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

func mapOrderRecords(records []OrderRecord) []agents.OrderDetail {
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
