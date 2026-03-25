package internal

type CloudAPIWebhook struct {
	Object string         `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

type WebhookEntry struct {
	ID      string          `json:"id"`
	Changes []WebhookChange `json:"changes"`
}

type WebhookChange struct {
	Field string              `json:"field"`
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
	From      string          `json:"from"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Text      WhatsAppText    `json:"text"`
	Context   WhatsAppContext `json:"context"`
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
				messages = append(messages, IncomingMessage{
					PhoneNumberID: change.Value.Metadata.PhoneNumberID,
					From:          msg.From,
					MessageID:     msg.ID,
					Text:          msg.Text.Body,
					Type:          msg.Type,
				})
			}
		}
	}

	return messages
}
