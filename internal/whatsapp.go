package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"multi-tenant-bot/internal/models"
)

type CloudAPIWebhook struct {
	Object string          `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

type WebhookEntry struct {
	ID      string           `json:"id"`
	Changes []WebhookChange `json:"changes"`
}

type WebhookChange struct {
	Field string               `json:"field"`
	Value WhatsAppChangeValue `json:"value"`
}

type WhatsAppChangeValue struct {
	MessagingProduct string            `json:"messaging_product"`
	Metadata         WhatsAppMetadata  `json:"metadata"`
	Contacts         []WhatsAppContact `json:"contacts"`
	Messages         []WhatsAppMessage `json:"messages"`
	Statuses         []WhatsAppStatus  `json:"statuses"`
}

type WhatsAppMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type WhatsAppContact struct {
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
	WaID string `json:"wa_id"`
}

type WhatsAppMessage struct {
	From      string               `json:"from"`
	ID        string               `json:"id"`
	Timestamp string               `json:"timestamp"`
	Type      string               `json:"type"`
	Text      WhatsAppText       `json:"text"`
	Interactive *WhatsAppInteractive `json:"interactive,omitempty"`
	Context   WhatsAppContext      `json:"context"`
}

type WhatsAppInteractive struct {
	Type        string               `json:"type"`
	ButtonReply *WhatsAppButtonReply `json:"button_reply,omitempty"`
	ListReply   *WhatsAppListReply   `json:"list_reply,omitempty"`
}

type WhatsAppButtonReply struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type WhatsAppListReply struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type WhatsAppText struct {
	Body string `json:"body"`
}

type WhatsAppContext struct {
	MessageID string `json:"id"`
}

type WhatsAppStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	RecipientID string `json:"recipient_id"`
	Timestamp   string `json:"timestamp"`
}

type IncomingMessage struct {
	PhoneNumberID string `json:"phone_number_id"`
	From          string `json:"from"`
	MessageID     string `json:"message_id"`
	Text          string `json:"text"`
	Type          string `json:"type"`
}

func ExtractIncomingMessages(payload *CloudAPIWebhook) []IncomingMessage {
	var messages []IncomingMessage

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				text := msg.Text.Body
				if msg.Type == "interactive" && msg.Interactive != nil {
					if msg.Interactive.ButtonReply != nil {
						text = msg.Interactive.ButtonReply.ID
					} else if msg.Interactive.ListReply != nil {
						text = msg.Interactive.ListReply.ID
					}
				}

				messages = append(messages, IncomingMessage{
					PhoneNumberID: change.Value.Metadata.PhoneNumberID,
					From:          msg.From,
					MessageID:     msg.ID,
					Text:          text,
					Type:          msg.Type,
				})
			}
		}
	}

	return messages
}

func SendWhatsAppMessage(ctx context.Context, phoneNumberID, to, token, message string) error {
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text": map[string]interface{}{
			"body":        message,
			"preview_url": false,
		},
	}
	return sendJSON(ctx, phoneNumberID, token, payload)
}

func SendWhatsAppButton(ctx context.Context, phoneNumberID, to, token, headerText, bodyText, footerText string, buttons []models.InteractiveButton) error {
	if len(buttons) > 3 {
		return fmt.Errorf("max 3 buttons allowed")
	}

	type actionButton struct {
		Type  string `json:"type"`
		Reply struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"reply"`
	}

	var actionButtons []actionButton
	for _, b := range buttons {
		ab := actionButton{Type: "reply"}
		ab.Reply.ID = b.ID
		ab.Reply.Title = b.Title
		actionButtons = append(actionButtons, ab)
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "interactive",
		"interactive": map[string]interface{}{
			"type": "button",
			"body": map[string]interface{}{
				"text": bodyText,
			},
			"action": map[string]interface{}{
				"buttons": actionButtons,
			},
		},
	}

	if headerText != "" {
		payload["interactive"].(map[string]interface{})["header"] = map[string]interface{}{
			"type": "text",
			"text": headerText,
		}
	}
	if footerText != "" {
		payload["interactive"].(map[string]interface{})["footer"] = map[string]interface{}{
			"text": footerText,
		}
	}

	return sendJSON(ctx, phoneNumberID, token, payload)
}

type ListSection struct {
	Title string
	Rows  []ListRow
}

type ListRow struct {
	ID          string
	Title       string
	Description string
}

func SendWhatsAppList(ctx context.Context, phoneNumberID, to, token, headerText, bodyText, footerText, buttonLabel string, sections []ListSection) error {
	type row struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
	}
	type section struct {
		Title string `json:"title,omitempty"`
		Rows  []row  `json:"rows"`
	}

	var wSections []section
	for _, s := range sections {
		var wRows []row
		for _, r := range s.Rows {
			wRows = append(wRows, row{ID: r.ID, Title: r.Title, Description: r.Description})
		}
		wSections = append(wSections, section{Title: s.Title, Rows: wRows})
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "interactive",
		"interactive": map[string]interface{}{
			"type": "list",
			"body": map[string]interface{}{
				"text": bodyText,
			},
			"action": map[string]interface{}{
				"button":   buttonLabel,
				"sections": wSections,
			},
		},
	}

	if headerText != "" {
		payload["interactive"].(map[string]interface{})["header"] = map[string]interface{}{
			"type": "text",
			"text": headerText,
		}
	}
	if footerText != "" {
		payload["interactive"].(map[string]interface{})["footer"] = map[string]interface{}{
			"text": footerText,
		}
	}

	return sendJSON(ctx, phoneNumberID, token, payload)
}

func sendJSON(ctx context.Context, phoneNumberID, token string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/messages", phoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp api error %d: %s", resp.StatusCode, string(errBody))
	}

	return nil
}
