package internal

import (
	"context"
	"encoding/json"
	"errors"
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
	activeAgent := session.Metadata["active_agent"]

	if activeAgent == "orderval" {
		resp := agents.HandleOrderVal(msg.Text, session.Metadata["orderval_state"], session.Metadata["orderval_context"])
		aiReply = resp.Message
		session.Metadata["orderval_state"] = resp.NextState
		
		ctxBytes, _ := json.Marshal(resp.NewContext)
		session.Metadata["orderval_context"] = string(ctxBytes)

		if resp.NextState == "IDLE" {
			delete(session.Metadata, "active_agent")
		}
	} else {
		route := agents.HandleRouter(msg.Text)
		
		switch route.Intent {
		case agents.RouteIntentGreeting, agents.RouteIntentMainMenu:
			aiReply = buildWelcomeMessage(tenant.BotConfig)
		case agents.RouteIntentOrders:
			session.Metadata["active_agent"] = "orderval"
			resp := agents.HandleOrderVal(msg.Text, "ORDERVAL_START", "{}")
			aiReply = resp.Message
			session.Metadata["orderval_state"] = resp.NextState
			ctxBytes, _ := json.Marshal(resp.NewContext)
			session.Metadata["orderval_context"] = string(ctxBytes)
		case agents.RouteIntentCarta, agents.RouteIntentLocations:
			aiReply = route.Message + "\n\n" + buildAvailableInfo(tenant.BotConfig)
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

	sb.WriteString("Estas son las opciones disponibles:\n")
	sb.WriteString("📦 Mis ordenes (escribe 'pedidos')\n")
	if len(cfg.MeetingPoints) > 0 {
		sb.WriteString("📍 Sedes y Puntos de Encuentro\n")
	}
	if cfg.MenuLink != "" {
		sb.WriteString("🍕 Ver Carta\n")
	}

	return sb.String()
}

func buildAvailableInfo(cfg BotConfig) string {
	var sb strings.Builder
	if len(cfg.MeetingPoints) > 0 {
		sb.WriteString("📍 Puntos de Encuentro:\n- " + strings.Join(cfg.MeetingPoints, "\n- ") + "\n\n")
	}
	if cfg.MenuLink != "" {
		sb.WriteString("🍕 Puedes ver nuestra carta aquí: " + cfg.MenuLink)
	}
	return sb.String()
}
