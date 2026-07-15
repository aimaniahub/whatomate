package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactutil"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
)

func redactURLForLog(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid_url>"
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return parsed.Path
	}

	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func truncateLogValue(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}

	return value[:maxLen] + "...(truncated)"
}

// IncomingTextMessage represents a text, interactive, or media message from the webhook
type IncomingTextMessage struct {
	From       string `json:"from"`
	FromUserID string `json:"from_user_id,omitempty"` // BSUID
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Type       string `json:"type"`
	Text       *struct {
		Body string `json:"body"`
	} `json:"text,omitempty"`
	Interactive *struct {
		Type        string `json:"type"`
		ButtonReply *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"button_reply,omitempty"`
		ListReply *struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"list_reply,omitempty"`
		NFMReply *struct {
			ResponseJSON string `json:"response_json"`
			Body         string `json:"body"`
			Name         string `json:"name"`
		} `json:"nfm_reply,omitempty"`
	} `json:"interactive,omitempty"`
	Image *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		Caption  string `json:"caption,omitempty"`
	} `json:"image,omitempty"`
	Document *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		Filename string `json:"filename,omitempty"`
		Caption  string `json:"caption,omitempty"`
	} `json:"document,omitempty"`
	Audio *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
	} `json:"audio,omitempty"`
	Video *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		Caption  string `json:"caption,omitempty"`
	} `json:"video,omitempty"`
	Sticker *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		Animated bool   `json:"animated,omitempty"`
	} `json:"sticker,omitempty"`
	Context *struct {
		From string `json:"from"`
		ID   string `json:"id"` // WhatsApp message ID being replied to
	} `json:"context,omitempty"`
	Reaction *struct {
		MessageID string `json:"message_id"` // WhatsApp message ID being reacted to
		Emoji     string `json:"emoji"`      // The emoji reaction (empty string = remove reaction)
	} `json:"reaction,omitempty"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Name      string  `json:"name,omitempty"`
		Address   string  `json:"address,omitempty"`
	} `json:"location,omitempty"`
	Button *struct {
		Text    string `json:"text"`
		Payload string `json:"payload"`
	} `json:"button,omitempty"`
	Contacts []struct {
		Name struct {
			FormattedName string `json:"formatted_name"`
			FirstName     string `json:"first_name,omitempty"`
			LastName      string `json:"last_name,omitempty"`
		} `json:"name"`
		Phones []struct {
			Phone string `json:"phone"`
			Type  string `json:"type,omitempty"`
		} `json:"phones,omitempty"`
	} `json:"contacts,omitempty"`
}

// processIncomingMessageFull processes incoming WhatsApp messages with chatbot logic
func (a *App) processIncomingMessageFull(phoneNumberID string, msg IncomingTextMessage, profileName string) {
	a.Log.Info("Processing incoming message",
		"phone_number_id", phoneNumberID,
		"from", msg.From,
		"type", msg.Type,
		"profile_name", profileName,
	)

	// Find the WhatsApp account by phone_number_id (use cache)
	account, err := a.getWhatsAppAccountCached(phoneNumberID)
	if err != nil {
		a.Log.Error("WhatsApp account not found", "phone_id", phoneNumberID, "error", err)
		return
	}

	// Handle reaction messages specially - they update existing messages, not create new ones
	if msg.Type == "reaction" && msg.Reaction != nil {
		a.handleIncomingReaction(account, msg.From, msg.Reaction.MessageID, msg.Reaction.Emoji, profileName)
		return
	}

	// Get or create contact (always do this for all incoming messages)
	contact, isNewContact, err := contactutil.GetOrCreateContact(a.DB, account.OrganizationID, msg.From, profileName)
	if err != nil {
		a.Log.Error("Failed to get or create contact", "from", msg.From, "error", err)
		return
	}

	// Store BSUID if provided and not already set
	if msg.FromUserID != "" && contact.BSUID != msg.FromUserID {
		a.DB.Model(contact).Update("bsuid", msg.FromUserID)
		contact.BSUID = msg.FromUserID
	}

	// Dispatch webhook if new contact was created
	if isNewContact {
		a.DispatchWebhook(account.OrganizationID, models.WebhookEventContactCreated, ContactEventData{
			ContactID:       contact.ID.String(),
			ContactPhone:    contact.PhoneNumber,
			ContactName:     contact.ProfileName,
			WhatsAppAccount: account.Name,
		})
	}

	// Get message content - handle text, button replies, list replies, and media
	messageText := ""
	messageType := msg.Type
	buttonID := "" // Track button/list ID for conditional routing
	var mediaInfo *MediaInfo

	// Track flow response data for WhatsApp Flow forms
	var flowResponseData map[string]any

	if msg.Type == "text" && msg.Text != nil {
		messageText = msg.Text.Body
	} else if msg.Type == "button" && msg.Button != nil {
		// Template quick_reply button click — WhatsApp sends type "button"
		messageText = msg.Button.Text
		buttonID = msg.Button.Payload
		messageType = "button_reply"
	} else if msg.Type == "interactive" && msg.Interactive != nil {
		// Handle button reply
		if msg.Interactive.ButtonReply != nil {
			messageText = msg.Interactive.ButtonReply.Title
			buttonID = msg.Interactive.ButtonReply.ID
			messageType = "button_reply"
		}
		// Handle list reply
		if msg.Interactive.ListReply != nil {
			messageText = msg.Interactive.ListReply.Title
			buttonID = msg.Interactive.ListReply.ID
			messageType = "button_reply"
		}
		// Handle WhatsApp Flow reply (nfm_reply)
		if msg.Interactive.NFMReply != nil {
			messageText = msg.Interactive.NFMReply.Body
			messageType = "nfm_reply"
			// Parse the response JSON to extract form data
			if msg.Interactive.NFMReply.ResponseJSON != "" {
				var responseData map[string]any
				if err := json.Unmarshal([]byte(msg.Interactive.NFMReply.ResponseJSON), &responseData); err != nil {
					a.Log.Error("Failed to parse flow response JSON", "error", err, "response_json", msg.Interactive.NFMReply.ResponseJSON)
				} else {
					flowResponseData = responseData
					a.Log.Info("Parsed WhatsApp Flow response", "data", flowResponseData)
				}
			}
		}
	} else if msg.Type == "image" && msg.Image != nil {
		// Handle image message
		messageText = msg.Image.Caption
		mediaInfo = &MediaInfo{
			MediaMimeType: msg.Image.MimeType,
		}
		// Download and save media locally
		waAccount := a.toWhatsAppAccount(account)
		if localPath, err := a.DownloadAndSaveMedia(context.Background(), msg.Image.ID, msg.Image.MimeType, waAccount); err != nil {
			a.Log.Error("Failed to download image", "error", err, "media_id", msg.Image.ID)
		} else {
			mediaInfo.MediaURL = localPath
		}
	} else if msg.Type == "document" && msg.Document != nil {
		// Handle document message
		messageText = msg.Document.Caption
		mediaInfo = &MediaInfo{
			MediaMimeType: msg.Document.MimeType,
			MediaFilename: msg.Document.Filename,
		}
		// Download and save media locally
		waAccount := a.toWhatsAppAccount(account)
		if localPath, err := a.DownloadAndSaveMedia(context.Background(), msg.Document.ID, msg.Document.MimeType, waAccount); err != nil {
			a.Log.Error("Failed to download document", "error", err, "media_id", msg.Document.ID)
		} else {
			mediaInfo.MediaURL = localPath
		}
	} else if msg.Type == "video" && msg.Video != nil {
		// Handle video message
		messageText = msg.Video.Caption
		mediaInfo = &MediaInfo{
			MediaMimeType: msg.Video.MimeType,
		}
		// Download and save media locally
		waAccount := a.toWhatsAppAccount(account)
		if localPath, err := a.DownloadAndSaveMedia(context.Background(), msg.Video.ID, msg.Video.MimeType, waAccount); err != nil {
			a.Log.Error("Failed to download video", "error", err, "media_id", msg.Video.ID)
		} else {
			mediaInfo.MediaURL = localPath
		}
	} else if msg.Type == "audio" && msg.Audio != nil {
		// Handle audio message
		mediaInfo = &MediaInfo{
			MediaMimeType: msg.Audio.MimeType,
		}
		// Download and save media locally
		waAccount := a.toWhatsAppAccount(account)
		if localPath, err := a.DownloadAndSaveMedia(context.Background(), msg.Audio.ID, msg.Audio.MimeType, waAccount); err != nil {
			a.Log.Error("Failed to download audio", "error", err, "media_id", msg.Audio.ID)
		} else {
			mediaInfo.MediaURL = localPath
		}
	} else if msg.Type == "sticker" && msg.Sticker != nil {
		// Handle sticker message (treat like image)
		mediaInfo = &MediaInfo{
			MediaMimeType: msg.Sticker.MimeType,
		}
		// Download and save media locally
		waAccount := a.toWhatsAppAccount(account)
		if localPath, err := a.DownloadAndSaveMedia(context.Background(), msg.Sticker.ID, msg.Sticker.MimeType, waAccount); err != nil {
			a.Log.Error("Failed to download sticker", "error", err, "media_id", msg.Sticker.ID)
		} else {
			mediaInfo.MediaURL = localPath
		}
	} else if msg.Type == "location" && msg.Location != nil {
		// Handle location message - store as JSON in content
		locationData := map[string]any{
			"latitude":  msg.Location.Latitude,
			"longitude": msg.Location.Longitude,
		}
		if msg.Location.Name != "" {
			locationData["name"] = msg.Location.Name
		}
		if msg.Location.Address != "" {
			locationData["address"] = msg.Location.Address
		}
		if jsonBytes, err := json.Marshal(locationData); err == nil {
			messageText = string(jsonBytes)
		}
	} else if msg.Type == "contacts" && len(msg.Contacts) > 0 {
		// Handle contacts message - store as JSON in content
		contactsData := make([]map[string]any, 0, len(msg.Contacts))
		for _, c := range msg.Contacts {
			contact := map[string]any{
				"name": c.Name.FormattedName,
			}
			if len(c.Phones) > 0 {
				phones := make([]string, 0, len(c.Phones))
				for _, p := range c.Phones {
					phones = append(phones, p.Phone)
				}
				contact["phones"] = phones
			}
			contactsData = append(contactsData, contact)
		}
		if jsonBytes, err := json.Marshal(contactsData); err == nil {
			messageText = string(jsonBytes)
		}
	}

	// Save incoming message to messages table (always, even if chatbot is disabled)
	var replyToWAMID string
	if msg.Context != nil && msg.Context.ID != "" {
		replyToWAMID = msg.Context.ID
	}
	a.saveIncomingMessage(account, contact, msg.ID, messageType, messageText, mediaInfo, replyToWAMID)

	// Clear chatbot tracking since client has replied
	a.ClearContactChatbotTracking(contact.ID)

	// Check for active agent transfer - skip chatbot processing if transferred
	if a.hasActiveAgentTransfer(account.OrganizationID, contact.ID) {
		a.Log.Info("Contact has active agent transfer, skipping chatbot processing",
			"contact_id", contact.ID,
			"phone_number", contact.PhoneNumber)
		return
	}

	// Check if chatbot is enabled for this account (use cache)
	settings, err := a.getChatbotSettingsCached(account.OrganizationID, account.Name)
	if err != nil {
		a.Log.Error("Failed to load chatbot settings", "error", err, "account", account.Name, "org_id", account.OrganizationID)
		return
	}
	if !settings.IsEnabled {
		a.Log.Debug("Chatbot not enabled for this account, creating transfer for agent queue", "account", account.Name, "settings_id", settings.ID)
		// Create transfer to agent queue when chatbot is disabled
		a.createTransferToQueue(account, contact, models.TransferSourceChatbotDisabled)
		return
	}
	a.Log.Info("Chatbot settings loaded", "settings_id", settings.ID, "is_enabled", settings.IsEnabled, "ai_enabled", settings.AI.Enabled, "ai_provider", settings.AI.Provider, "default_response", settings.DefaultResponse)

	// Check business hours if enabled
	if settings.BusinessHours.Enabled && len(settings.BusinessHours.Hours) > 0 {
		if !a.isWithinBusinessHours(settings.BusinessHours.Hours) {
			// If automated responses are not allowed outside hours, send out-of-hours message and stop
			if !settings.BusinessHours.AllowAutomatedOutside {
				a.Log.Info("Outside business hours, sending out of hours message")
				if settings.BusinessHours.OutOfHoursMessage != "" {
					if err := a.sendAndSaveTextMessage(account, contact, settings.BusinessHours.OutOfHoursMessage); err != nil {
						a.Log.Error("Failed to send out of hours message", "error", err, "contact", contact.PhoneNumber)
					}
				}
				return
			}
			// AllowAutomatedOutsideHours is true, continue processing flows/keywords/AI
			a.Log.Info("Outside business hours but automated responses allowed, continuing")
		}
	}

	// Only process text and interactive messages for chatbot
	if messageText == "" {
		a.Log.Debug("Skipping message with no text content for chatbot", "type", msg.Type)
		return
	}

	a.Log.Info("Processing message", "text", messageText, "buttonID", buttonID, "from", msg.From)

	// Get or create active session for this contact
	session, isNewSession := a.getOrCreateSession(account.OrganizationID, contact.ID, account.Name, msg.From, settings.SessionTimeoutMins)

	// Log incoming message to session
	a.logSessionMessage(session.ID, models.DirectionIncoming, messageText, "keyword_check")

	// Check for transfer keyword BEFORE sending greeting (transfer takes priority)
	keywordResponse, keywordMatched := a.matchKeywordRules(account.OrganizationID, account.Name, messageText)
	if keywordMatched && keywordResponse.ResponseType == models.ResponseTypeTransfer {
		a.Log.Info("Transfer keyword matched", "response", keywordResponse.Body)
		// Check business hours - if outside hours, send out of hours message instead
		if settings.BusinessHours.Enabled && len(settings.BusinessHours.Hours) > 0 {
			if !a.isWithinBusinessHours(settings.BusinessHours.Hours) {
				a.Log.Info("Outside business hours, sending out of hours message instead of transfer")
				if settings.BusinessHours.OutOfHoursMessage != "" {
					if err := a.sendAndSaveTextMessage(account, contact, settings.BusinessHours.OutOfHoursMessage); err != nil {
						a.Log.Error("Failed to send out of hours message", "error", err, "contact", contact.PhoneNumber)
					}
				}
				return
			}
		}
		// Within business hours - send transfer message and create transfer
		if keywordResponse.Body != "" {
			if err := a.sendAndSaveTextMessage(account, contact, keywordResponse.Body); err != nil {
				a.Log.Error("Failed to send transfer message", "error", err, "contact", contact.PhoneNumber)
			}
		}
		a.createTransferFromKeyword(account, contact)
		return
	}

	// Check if user is in an active flow. After Phase 4.2 every flow has
	// a v2 Graph populated; any flow without one is a misconfiguration
	// (manual DB edit or failed backfill) — log and exit cleanly.
	if session.CurrentFlowID != nil {
		flow, err := a.getChatbotFlowByIDCached(account.OrganizationID, *session.CurrentFlowID)
		if err != nil || flow == nil {
			a.Log.Error("Active chatbot flow not loadable", "error", err, "session", session.ID, "flow", session.CurrentFlowID)
			a.exitFlow(session)
			return
		}
		if flow.Graph == nil {
			a.Log.Error("Active chatbot flow has no v2 graph; ignoring inbound", "session", session.ID, "flow", flow.ID)
			a.exitFlow(session)
			return
		}

		// Dynamic Buttons Node Breakout / Fallback logic:
		// If the user typed free text (buttonID is empty) at a buttons node, we try to:
		// 1. Check if the message matches another flow trigger keyword.
		// 2. Check if the message matches a keyword rule (non-transfer).
		// 3. Fallback to AI if configured, answer the query, and re-send the buttons.
		if buttonID == "" {
			graph, err := parseChatGraph(flow.Graph)
			if err == nil && graph != nil {
				node := graph.getNode(session.CurrentStep)
				if node != nil && node.Type == ChatNodeButtons {
					// 1. Try to match trigger keywords for a different flow
					if nextFlow := a.matchFlowTrigger(account.OrganizationID, messageText); nextFlow != nil && nextFlow.ID != flow.ID {
						a.Log.Info("Active buttons node breakout to different flow", "from_flow", flow.Name, "to_flow", nextFlow.Name)
						session.CurrentFlowID = &nextFlow.ID
						session.CurrentStep = ""
						session.StepRetries = 0
						session.SessionData = models.JSONB{
							"_flow_id":   nextFlow.ID.String(),
							"_flow_name": nextFlow.Name,
						}
						if err := a.runChatGraph(account, contact, session, nextFlow, messageText, buttonID, flowResponseData); err != nil {
							a.Log.Error("Chat graph runner failed at flow start", "error", err, "session", session.ID, "flow", nextFlow.ID)
						}
						return
					}

					// 2. Try to match non-transfer keyword rules
					if keywordMatched && keywordResponse.ResponseType != models.ResponseTypeTransfer {
						a.Log.Info("Active buttons node breakout to keyword rule", "response", keywordResponse.Body)
						if len(keywordResponse.Buttons) > 0 {
							if err := a.sendAndSaveInteractiveButtons(account, contact, keywordResponse.Body, keywordResponse.Buttons); err != nil {
								a.Log.Error("Failed to send breakout keyword buttons", "error", err)
							}
						} else {
							if err := a.sendAndSaveTextMessage(account, contact, keywordResponse.Body); err != nil {
								a.Log.Error("Failed to send breakout keyword text", "error", err)
							}
						}
						a.logSessionMessage(session.ID, models.DirectionOutgoing, keywordResponse.Body, "keyword_response")

						// Re-send the current buttons node menu so they are not stuck
						bodyText := stringFromConfig(node.Config, "body", "message", "text")
						if bodyText == "" {
							bodyText = node.Label
						}
						bodyText = processTemplate(bodyText, session.SessionData)
						btnList := buttonsFromConfig(node.Config)
						for _, b := range btnList {
							for _, key := range []string{"title", "url", "phone_number"} {
								if s, ok := b[key].(string); ok && s != "" {
									b[key] = processTemplate(s, session.SessionData)
								}
							}
						}
						if err := a.sendAndSaveInteractiveButtons(account, contact, bodyText, btnList); err != nil {
							a.Log.Error("Failed to re-send buttons menu after keyword breakout", "error", err)
						}
						return
					}

					// Intercept contact query to bypass AI
					if isContactOrLocationQuery(messageText) {
						a.Log.Info("Active buttons node contact bypass triggered", "text", messageText)
						contactCard := getContactCardText()
						if err := a.sendAndSaveTextMessage(account, contact, contactCard); err != nil {
							a.Log.Error("Failed to send contact bypass message", "error", err)
						}
						a.logSessionMessage(session.ID, models.DirectionOutgoing, contactCard, "contact_bypass")

						// Re-send the current buttons node menu so they are not stuck
						bodyText := stringFromConfig(node.Config, "body", "message", "text")
						if bodyText == "" {
							bodyText = node.Label
						}
						bodyText = processTemplate(bodyText, session.SessionData)
						btnList := buttonsFromConfig(node.Config)
						for _, b := range btnList {
							for _, key := range []string{"title", "url", "phone_number"} {
								if s, ok := b[key].(string); ok && s != "" {
									b[key] = processTemplate(s, session.SessionData)
								}
							}
						}
						if err := a.sendAndSaveInteractiveButtons(account, contact, bodyText, btnList); err != nil {
							a.Log.Error("Failed to re-send buttons menu after contact bypass", "error", err)
						}
						return
					}

					// 3. Fallback to AI / RAG if available
					if a.canAttemptAI(settings, account.Name) {
						a.Log.Info("Active buttons node fallback to AI", "text", messageText)
						aiResponse, err := a.generateAIResponse(settings, session, messageText)
						if err == nil && aiResponse != "" {
							if err := a.sendAndSaveTextMessage(account, contact, aiResponse); err != nil {
								a.Log.Error("Failed to send fallback AI response", "error", err)
							}
							a.logSessionMessage(session.ID, models.DirectionOutgoing, aiResponse, "ai_response")

							// After answering, always bring the user back to the main menu
							// so they can navigate (Registration Hub / Service Query / Ask Darvi AI / Contact Us)
							if err := a.sendGreetingMenu(account, contact, settings); err != nil {
								a.Log.Error("Failed to re-send main menu after AI fallback", "error", err)
							}
							return
						}
					}
				}
			}
		}

		if err := a.runChatGraph(account, contact, session, flow, messageText, buttonID, flowResponseData); err != nil {
			a.Log.Error("Chat graph runner failed", "error", err, "session", session.ID, "flow", flow.ID)
		}
		return
	}

	// Try to match flow trigger keywords first (before greeting to avoid duplicate messages)
	if flow := a.matchFlowTrigger(account.OrganizationID, messageText); flow != nil {
		if flow.Graph == nil {
			a.Log.Error("Triggered chatbot flow has no v2 graph; ignoring", "flow", flow.ID)
			return
		}
		session.CurrentFlowID = &flow.ID
		session.CurrentStep = ""
		session.StepRetries = 0
		session.SessionData = models.JSONB{
			"_flow_id":   flow.ID.String(),
			"_flow_name": flow.Name,
		}
		if err := a.runChatGraph(account, contact, session, flow, messageText, buttonID, flowResponseData); err != nil {
			a.Log.Error("Chat graph runner failed at flow start", "error", err, "session", session.ID, "flow", flow.ID)
		}
		return
	}

	// Send greeting message for new sessions (only if no flow was triggered)
	if isNewSession && settings.DefaultResponse != "" {
		// If it's NOT a simple greeting (e.g., they asked a question immediately),
		// try to generate an AI answer first so they get their question answered.
		if !isSimpleGreeting(messageText) {
			answered := false
			// Intercept contact query to bypass AI
			if isContactOrLocationQuery(messageText) {
				a.Log.Info("New session contact bypass triggered", "text", messageText)
				contactCard := getContactCardText()
				if err := a.sendAndSaveTextMessage(account, contact, contactCard); err == nil {
					a.logSessionMessage(session.ID, models.DirectionOutgoing, contactCard, "contact_bypass")
					answered = true
				}
			}
			// Try keyword rule first
			if !answered && keywordMatched && keywordResponse.ResponseType != models.ResponseTypeTransfer {
				a.Log.Info("New session non-greeting matched keyword", "response", keywordResponse.Body)
				if len(keywordResponse.Buttons) > 0 {
					if err := a.sendAndSaveInteractiveButtons(account, contact, keywordResponse.Body, keywordResponse.Buttons); err == nil {
						a.logSessionMessage(session.ID, models.DirectionOutgoing, keywordResponse.Body, "keyword_response")
						answered = true
					}
				} else {
					if err := a.sendAndSaveTextMessage(account, contact, keywordResponse.Body); err == nil {
						a.logSessionMessage(session.ID, models.DirectionOutgoing, keywordResponse.Body, "keyword_response")
						answered = true
					}
				}
			}
			// If not answered by keyword, try AI / RAG
			if !answered && a.canAttemptAI(settings, account.Name) {
				a.Log.Info("New session non-greeting attempting AI response", "text", messageText)
				aiResponse, err := a.generateAIResponse(settings, session, messageText)
				if err == nil && aiResponse != "" {
					if err := a.sendAndSaveTextMessage(account, contact, aiResponse); err == nil {
						a.logSessionMessage(session.ID, models.DirectionOutgoing, aiResponse, "ai_response")
					}
				}
			}
		}

		a.Log.Info("New session - sending greeting message", "contact", contact.PhoneNumber)
		if err := a.sendGreetingMenu(account, contact, settings); err != nil {
			a.Log.Error("Failed to send greeting menu", "error", err, "contact", contact.PhoneNumber)
		}
		a.logSessionMessage(session.ID, models.DirectionOutgoing, settings.DefaultResponse, "greeting")
		return // After greeting, don't process further for new sessions
	}

	// Handle non-transfer keyword matches (transfer was already handled above)
	if keywordMatched && keywordResponse.ResponseType != models.ResponseTypeTransfer {
		a.Log.Info("Keyword rule matched", "response_type", keywordResponse.ResponseType, "response", keywordResponse.Body)

		// Handle regular text response
		if len(keywordResponse.Buttons) > 0 {
			if err := a.sendAndSaveInteractiveButtons(account, contact, keywordResponse.Body, keywordResponse.Buttons); err != nil {
				a.Log.Error("Failed to send interactive buttons", "error", err, "contact", contact.PhoneNumber)
			}
		} else {
			if err := a.sendAndSaveTextMessage(account, contact, keywordResponse.Body); err != nil {
				a.Log.Error("Failed to send text message", "error", err, "contact", contact.PhoneNumber)
			}
		}
		// Log outgoing message
		a.logSessionMessage(session.ID, models.DirectionOutgoing, keywordResponse.Body, "keyword_response")
		return
	}

	// Intercept contact query to bypass AI in global flow
	if isContactOrLocationQuery(messageText) {
		a.Log.Info("Global flow contact bypass triggered", "text", messageText)
		contactCard := getContactCardText()
		if err := a.sendAndSaveTextMessage(account, contact, contactCard); err != nil {
			a.Log.Error("Failed to send contact bypass message", "error", err)
		}
		a.logSessionMessage(session.ID, models.DirectionOutgoing, contactCard, "contact_bypass")

		// Re-send the greeting menu buttons
		if err := a.sendGreetingMenu(account, contact, settings); err != nil {
			a.Log.Error("Failed to send greeting menu after contact bypass", "error", err)
		}
		return
	}

	// If no keyword matched, try AI / RAG response if available
	if a.canAttemptAI(settings, account.Name) {
		a.Log.Info("Attempting AI response", "provider", settings.AI.Provider, "model", settings.AI.Model)
		aiResponse, err := a.generateAIResponse(settings, session, messageText)
		if err != nil {
			a.Log.Error("AI response failed", "error", err, "provider", settings.AI.Provider, "model", settings.AI.Model)
			// Fall through to default response
		} else if aiResponse != "" {
			a.Log.Info("AI response generated successfully", "response_length", len(aiResponse))
			if err := a.sendAndSaveTextMessage(account, contact, aiResponse); err != nil {
				a.Log.Error("Failed to send AI response", "error", err, "contact", contact.PhoneNumber)
			}
			a.logSessionMessage(session.ID, models.DirectionOutgoing, aiResponse, "ai_response")

			// Always re-send the main menu after an AI/RAG answer so the user
			// can continue navigating (Registration Hub / Service Query / Ask Darvi AI / Contact Us).
			if err := a.sendGreetingMenu(account, contact, settings); err != nil {
				a.Log.Error("Failed to send main menu after AI response", "error", err)
			}
			return
		} else {
			a.Log.Warn("AI returned empty response")
		}
	} else {
		a.Log.Info("AI not configured", "ai_enabled", settings.AI.Enabled, "has_provider", settings.AI.Provider != "", "has_api_key", settings.AI.APIKey != "")
	}

	// If no AI response or AI not enabled, send fallback message (for existing sessions)
	// Greeting is already sent for new sessions above
	if settings.FallbackMessage != "" && !isNewSession {
		a.Log.Info("Sending fallback message", "response", settings.FallbackMessage)
		if len(settings.FallbackButtons) > 0 {
			fallbackButtons := make([]map[string]any, 0)
			for _, btn := range settings.FallbackButtons {
				if btnMap, ok := btn.(map[string]any); ok {
					fallbackButtons = append(fallbackButtons, btnMap)
				}
			}
			if len(fallbackButtons) > 0 {
				if err := a.sendAndSaveInteractiveButtons(account, contact, settings.FallbackMessage, fallbackButtons); err != nil {
					a.Log.Error("Failed to send fallback buttons", "error", err, "contact", contact.PhoneNumber)
				}
			} else {
				if err := a.sendAndSaveTextMessage(account, contact, settings.FallbackMessage); err != nil {
					a.Log.Error("Failed to send fallback message", "error", err, "contact", contact.PhoneNumber)
				}
			}
		} else {
			if err := a.sendAndSaveTextMessage(account, contact, settings.FallbackMessage); err != nil {
				a.Log.Error("Failed to send fallback message", "error", err, "contact", contact.PhoneNumber)
			}
		}
		a.logSessionMessage(session.ID, models.DirectionOutgoing, settings.FallbackMessage, "fallback_response")
	} else if !isNewSession {
		a.Log.Info("No fallback message configured for existing session")
	}
}

// KeywordResponse holds the response content and optional buttons
type KeywordResponse struct {
	Body         string
	Buttons      []map[string]any
	ResponseType models.ResponseType // text, transfer
}

// matchKeywordRules checks if the message matches any keyword rules
func (a *App) matchKeywordRules(orgID uuid.UUID, accountName, messageText string) (*KeywordResponse, bool) {
	// Use cached keyword rules (includes both account-specific and global rules)
	rules, err := a.getKeywordRulesCached(orgID, accountName)
	if err != nil {
		a.Log.Error("Failed to fetch keyword rules", "error", err)
		return nil, false
	}

	messageLower := strings.ToLower(messageText)

	for _, rule := range rules {
		for _, keyword := range rule.Keywords {
			keywordLower := strings.ToLower(keyword)
			matched := false

			switch rule.MatchType {
			case models.MatchTypeExact:
				if rule.CaseSensitive {
					matched = messageText == keyword
				} else {
					matched = messageLower == keywordLower
				}
			case models.MatchTypeContains:
				if rule.CaseSensitive {
					matched = strings.Contains(messageText, keyword)
				} else {
					matched = strings.Contains(messageLower, keywordLower)
				}
			case models.MatchTypeStartsWith:
				if rule.CaseSensitive {
					matched = strings.HasPrefix(messageText, keyword)
				} else {
					matched = strings.HasPrefix(messageLower, keywordLower)
				}
			case models.MatchTypeRegex:
				re, err := regexp.Compile(keyword)
				if err == nil {
					matched = re.MatchString(messageText)
				}
			default:
				// Default to contains
				matched = strings.Contains(messageLower, keywordLower)
			}

			if matched {
				response := &KeywordResponse{
					ResponseType: rule.ResponseType,
				}

				// For transfer type, use body as the transfer message
				if rule.ResponseType == models.ResponseTypeTransfer {
					if body, ok := rule.ResponseContent["body"].(string); ok {
						response.Body = body
					}
					return response, true
				}

				// Get response body
				if body, ok := rule.ResponseContent["body"].(string); ok {
					response.Body = body
				}

				// Get buttons if present
				if buttons, ok := rule.ResponseContent["buttons"].([]any); ok && len(buttons) > 0 {
					response.Buttons = make([]map[string]any, 0, len(buttons))
					for _, btn := range buttons {
						if btnMap, ok := btn.(map[string]any); ok {
							response.Buttons = append(response.Buttons, btnMap)
						}
					}
				}

				if response.Body != "" {
					return response, true
				}
			}
		}
	}

	return nil, false
}

// sendAndSaveTextMessage sends a text message and saves it to the database
// Uses the unified SendOutgoingMessage for consistent behavior
func (a *App) sendAndSaveTextMessage(account *models.WhatsAppAccount, contact *models.Contact, message string) error {
	ctx := context.Background()
	_, err := a.SendOutgoingMessage(ctx, OutgoingMessageRequest{
		Account: account,
		Contact: contact,
		Type:    models.MessageTypeText,
		Content: message,
	}, ChatbotSendOptions())
	return err
}

// sendAndSaveInteractiveButtons sends an interactive button message and saves it to the database.
// Buttons with type "url" are automatically separated and sent as CTA URL messages,
// since WhatsApp doesn't allow mixing reply buttons and URL buttons in the same message.
func (a *App) sendAndSaveInteractiveButtons(account *models.WhatsAppAccount, contact *models.Contact, bodyText string, buttons []map[string]any) error {
	// Separate reply buttons from CTA buttons (url / phone)
	replyButtons := make([]map[string]any, 0, len(buttons))
	ctaButtons := make([]map[string]any, 0)
	for _, btn := range buttons {
		btnType, _ := btn["type"].(string)
		switch btnType {
		case "url":
			ctaButtons = append(ctaButtons, btn)
		case "phone":
			// Convert phone button to CTA URL with tel: scheme
			phoneNumber, _ := btn["phone_number"].(string)
			if phoneNumber != "" {
				ctaButtons = append(ctaButtons, map[string]any{
					"title": btn["title"],
					"url":   "tel:" + phoneNumber,
				})
			}
		default:
			replyButtons = append(replyButtons, btn)
		}
	}

	// WhatsApp doesn't allow mixing reply and CTA buttons.
	// If both exist (legacy configs), ignore CTA buttons.
	if len(replyButtons) > 0 && len(ctaButtons) > 0 {
		ctaButtons = nil
	}

	// Send reply buttons (with the body text)
	if len(replyButtons) > 0 {
		waButtons := make([]whatsapp.Button, 0, len(replyButtons))
		for i, btn := range replyButtons {
			if i >= 10 {
				break
			}
			buttonID, _ := btn["id"].(string)
			buttonTitle, _ := btn["title"].(string)
			if buttonID == "" {
				buttonID = fmt.Sprintf("btn_%d", i+1)
			}
			if buttonTitle == "" {
				continue
			}
			waButtons = append(waButtons, whatsapp.Button{
				ID:    buttonID,
				Title: buttonTitle,
			})
		}

		if len(waButtons) > 0 {
			interactiveType := "button"
			if len(waButtons) > 3 {
				interactiveType = "list"
			}
			ctx := context.Background()
			if _, err := a.SendOutgoingMessage(ctx, OutgoingMessageRequest{
				Account:         account,
				Contact:         contact,
				Type:            models.MessageTypeInteractive,
				InteractiveType: interactiveType,
				BodyText:        bodyText,
				Buttons:         waButtons,
			}, ChatbotSendOptions()); err != nil {
				return err
			}
		}
	}

	// Send CTA-only buttons (no reply buttons mixed in)
	// WhatsApp allows max 2 CTA buttons, each sent as a separate cta_url message.
	if len(ctaButtons) > 2 {
		ctaButtons = ctaButtons[:2]
	}
	for i, ctaBtn := range ctaButtons {
		btnTitle, _ := ctaBtn["title"].(string)
		btnURL, _ := ctaBtn["url"].(string)
		if btnTitle != "" && btnURL != "" {
			// First CTA button carries the body text
			ctaBody := bodyText
			if i > 0 {
				ctaBody = btnTitle
			}
			if err := a.sendAndSaveCTAURLButton(account, contact, ctaBody, btnTitle, btnURL); err != nil {
				return err
			}
		}
	}

	// No buttons at all — fall back to text
	if len(replyButtons) == 0 && len(ctaButtons) == 0 {
		return a.sendAndSaveTextMessage(account, contact, bodyText)
	}

	return nil
}

// sendAndSaveCTAURLButton sends a CTA URL button message and saves it to the database
// Uses the unified SendOutgoingMessage for consistent behavior
func (a *App) sendAndSaveCTAURLButton(account *models.WhatsAppAccount, contact *models.Contact, bodyText, buttonText, url string) error {
	ctx := context.Background()
	_, err := a.SendOutgoingMessage(ctx, OutgoingMessageRequest{
		Account:         account,
		Contact:         contact,
		Type:            models.MessageTypeInteractive,
		InteractiveType: "cta_url",
		BodyText:        bodyText,
		ButtonText:      buttonText,
		URL:             url,
	}, ChatbotSendOptions())
	return err
}

// sendAndSaveFlowMessage sends a WhatsApp Flow message and saves it to the database
// Uses the unified SendOutgoingMessage for consistent behavior
func (a *App) sendAndSaveFlowMessage(account *models.WhatsAppAccount, contact *models.Contact, flowID, headerText, bodyText, ctaText, flowToken, firstScreen string) error {
	ctx := context.Background()
	_, err := a.SendOutgoingMessage(ctx, OutgoingMessageRequest{
		Account:         account,
		Contact:         contact,
		Type:            models.MessageTypeFlow,
		FlowID:          flowID,
		FlowHeader:      headerText,
		BodyText:        bodyText,
		FlowCTA:         ctaText,
		FlowToken:       flowToken,
		FlowFirstScreen: firstScreen,
	}, ChatbotSendOptions())
	return err
}

// getOrCreateSession finds an active session or creates a new one
// Returns the session and a boolean indicating if it's a new session
func (a *App) getOrCreateSession(orgID, contactID uuid.UUID, accountName, phoneNumber string, timeoutMins int) (*models.ChatbotSession, bool) {
	now := time.Now()

	// Look for an active session that hasn't timed out
	var session models.ChatbotSession
	timeout := now.Add(-time.Duration(timeoutMins) * time.Minute)
	result := a.DB.Where("organization_id = ? AND contact_id = ? AND whats_app_account = ? AND status = ? AND last_activity_at > ?",
		orgID, contactID, accountName, models.SessionStatusActive, timeout).First(&session)

	if result.Error == nil {
		// Update last activity
		a.DB.Model(&session).Update("last_activity_at", now)
		return &session, false // existing session
	}

	// Create new session
	session = models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: accountName,
		PhoneNumber:     phoneNumber,
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       now,
		LastActivityAt:  now,
	}
	if err := a.DB.Create(&session).Error; err != nil {
		a.Log.Error("Failed to create session", "error", err)
	}
	return &session, true // new session
}

// logSessionMessage logs a message to the chatbot session
func (a *App) logSessionMessage(sessionID uuid.UUID, direction models.Direction, message, stepName string) {
	msg := models.ChatbotSessionMessage{
		BaseModel: models.BaseModel{ID: uuid.New()},
		SessionID: sessionID,
		Direction: direction,
		Message:   message,
		StepName:  stepName,
	}
	if err := a.DB.Create(&msg).Error; err != nil {
		a.Log.Error("Failed to log session message", "error", err)
	}
}

// matchFlowTrigger checks if the message triggers any flow
func (a *App) matchFlowTrigger(orgID uuid.UUID, messageText string) *models.ChatbotFlow {
	// Use cached flows (includes steps)
	flows, err := a.getChatbotFlowsCached(orgID)
	if err != nil {
		a.Log.Error("Failed to fetch chatbot flows", "error", err)
		return nil
	}

	for _, flow := range flows {
		for _, keyword := range flow.TriggerKeywords {
			if flowTriggerKeywordMatches(messageText, keyword) {
				return &flow
			}
		}
	}
	return nil
}

// flowTriggerKeywordMatches decides if a flow trigger keyword applies to the
// user message. Multi-word keywords use substring match; single tokens use
// whole-word matching so short triggers like "hi" do not match inside "this"
// / "which" and restart the entire welcome flow mid-conversation.
func flowTriggerKeywordMatches(message, keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || strings.TrimSpace(message) == "" {
		return false
	}
	msgLower := strings.ToLower(message)
	kwLower := strings.ToLower(keyword)

	// Phrase triggers (e.g. "main menu") keep contains semantics.
	if strings.Contains(kwLower, " ") {
		return strings.Contains(msgLower, kwLower)
	}

	// Whole-word match for single tokens.
	re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
	if err != nil {
		return strings.Contains(msgLower, kwLower)
	}
	return re.MatchString(message)
}

// startFlow initiates a chatbot flow for a user
func (a *App) exitFlow(session *models.ChatbotSession) {
	now := time.Now()
	a.DB.Model(session).Updates(map[string]any{
		"current_step": "",
		"step_retries": 0,
		"status":       models.SessionStatusCompleted,
		"completed_at": now,
	})

	// Clear chatbot tracking so SLA doesn't fire after flow exit
	a.ClearContactChatbotTracking(session.ContactID)
}

// closeSession ends the chatbot session and clears contact tracking
// It takes the full flow to find next steps when skipping
// executeConfiguredAPI builds and executes an HTTP request from a chatbot API config.
// replaceVar is called to substitute variables in the URL, body, and header values.
// Returns the response body and status code.
func (a *App) executeConfiguredAPI(apiConfig models.JSONB, replaceVar func(string) string) ([]byte, int, error) {
	apiURL, ok := apiConfig["url"].(string)
	if !ok || apiURL == "" {
		return nil, 0, fmt.Errorf("API URL is required")
	}
	apiURL = replaceVar(apiURL)
	logURL := redactURLForLog(apiURL)

	method := "GET"
	if m, ok := apiConfig["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	var bodyReader io.Reader
	if bodyTemplate, ok := apiConfig["body"].(string); ok && bodyTemplate != "" {
		bodyReader = strings.NewReader(replaceVar(bodyTemplate))
	}

	req, err := http.NewRequest(method, apiURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if headers, ok := apiConfig["headers"].(map[string]any); ok {
		for key, value := range headers {
			if strVal, ok := value.(string); ok {
				req.Header.Set(key, replaceVar(strVal))
			}
		}
	}

	a.Log.Info("Executing configured API request", "method", method, "url", logURL)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		a.Log.Error("Configured API request failed", "method", method, "url", logURL, "error", err)
		return nil, 0, fmt.Errorf("API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limitReader := io.LimitReader(resp.Body, 1024*1024)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		a.Log.Error("Failed to read configured API response", "method", method, "url", logURL, "status_code", resp.StatusCode, "error", err)
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		a.Log.Warn(
			"Configured API request returned non-2xx",
			"method", method,
			"url", logURL,
			"status_code", resp.StatusCode,
			"response_preview", truncateLogValue(string(body), 300),
		)
	} else {
		a.Log.Info(
			"Configured API request completed",
			"method", method,
			"url", logURL,
			"status_code", resp.StatusCode,
			"response_bytes", len(body),
		)
	}

	return body, resp.StatusCode, nil
}

type ApiResponse struct {
	Message      string
	Buttons      []map[string]any
	MappedData   map[string]any // Data extracted via response_mapping
	ResponseData map[string]any // Full API response data
}

// canAttemptAI reports whether we should try generateAIResponse for this org.
// True when a local LLM provider is fully configured, OR when at least one
// enabled RAG AI Context exists (RAG answers without a local provider key).
func (a *App) canAttemptAI(settings *models.ChatbotSettings, whatsAppAccount string) bool {
	if settings == nil {
		return false
	}
	if settings.AI.Enabled && settings.AI.Provider != "" && settings.AI.APIKey != "" {
		return true
	}
	return a.hasEnabledRAGContext(settings.OrganizationID, whatsAppAccount)
}

// hasEnabledRAGContext returns true if the org has any enabled type=rag AI context.
func (a *App) hasEnabledRAGContext(orgID uuid.UUID, whatsAppAccount string) bool {
	contexts, err := a.getAIContextsCached(orgID, whatsAppAccount)
	if err != nil {
		return false
	}
	for _, ctx := range contexts {
		if ctx.IsEnabled && ctx.ContextType == models.ContextTypeRAG {
			return true
		}
	}
	return false
}

// aiContextMatchesKeywords returns true when the context has no trigger keywords
// (always match) or when userMessage contains any keyword (case-insensitive).
func aiContextMatchesKeywords(ctx models.AIContext, userMessage string) bool {
	if len(ctx.TriggerKeywords) == 0 {
		return true
	}
	msg := strings.ToLower(userMessage)
	for _, kw := range ctx.TriggerKeywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if strings.Contains(msg, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// filterMatchingAIContexts returns enabled contexts that match the user message
// keywords, sorted by priority descending (higher first).
func filterMatchingAIContexts(contexts []models.AIContext, userMessage string) []models.AIContext {
	matched := make([]models.AIContext, 0, len(contexts))
	for _, ctx := range contexts {
		if !ctx.IsEnabled {
			continue
		}
		if !aiContextMatchesKeywords(ctx, userMessage) {
			continue
		}
		matched = append(matched, ctx)
	}
	// Priority desc (stable enough for small lists)
	for i := 0; i < len(matched); i++ {
		for j := i + 1; j < len(matched); j++ {
			if matched[j].Priority > matched[i].Priority {
				matched[i], matched[j] = matched[j], matched[i]
			}
		}
	}
	return matched
}

// normalizeRAGChatURL ensures the configured URL points at the /chat endpoint.
func normalizeRAGChatURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	lower := strings.ToLower(raw)
	if strings.HasSuffix(lower, "/chat") {
		return raw
	}
	return raw + "/chat"
}

// jsonConfigInt reads an int from JSONB config (float64/int/json.Number/string).
func jsonConfigInt(cfg models.JSONB, key string, def int) int {
	if cfg == nil {
		return def
	}
	switch v := cfg[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	case string:
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &out); err == nil && out > 0 {
			return out
		}
	}
	return def
}

// jsonConfigFloat reads a float from JSONB config.
func jsonConfigFloat(cfg models.JSONB, key string) (float64, bool) {
	if cfg == nil {
		return 0, false
	}
	switch v := cfg[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// callRAGContext POSTs to an external RAG chat API and returns the answer.
// Expects pdf_rag-compatible response: { "answer": "...", "abstained": bool }.
func (a *App) callRAGContext(aiCtx models.AIContext, session *models.ChatbotSession, userMessage string) (answer string, abstained bool, err error) {
	if aiCtx.ApiConfig == nil {
		return "", false, fmt.Errorf("RAG context %q has empty api_config", aiCtx.Name)
	}

	rawURL, _ := aiCtx.ApiConfig["url"].(string)
	chatURL := normalizeRAGChatURL(rawURL)
	if chatURL == "" {
		return "", false, fmt.Errorf("RAG context %q missing url", aiCtx.Name)
	}

	timeoutSecs := jsonConfigInt(aiCtx.ApiConfig, "timeout_seconds", 45)
	if timeoutSecs < 10 {
		timeoutSecs = 10
	}
	if timeoutSecs > 120 {
		timeoutSecs = 120
	}

	// History for multi-turn RAG (last 4 turns). Drop the trailing user turn
	// when it duplicates the current question (already logged before AI runs).
	historyLimit := 4
	var history []map[string]string
	if session != nil {
		msgs := a.getSessionHistory(session.ID, historyLimit*2)
		qNorm := strings.TrimSpace(userMessage)
		for _, m := range msgs {
			role := "user"
			if m.Direction == models.DirectionOutgoing {
				role = "assistant"
			}
			content := strings.TrimSpace(m.Message)
			if content == "" {
				continue
			}
			history = append(history, map[string]string{
				"role":    role,
				"content": content,
			})
		}
		if n := len(history); n > 0 && history[n-1]["role"] == "user" && history[n-1]["content"] == qNorm {
			history = history[:n-1]
		}
	}

	body := map[string]any{
		"question": userMessage,
		"source":   "whatomate",
		"history":  history,
	}
	if session != nil {
		body["session_id"] = session.ID.String()
	}
	if lang, ok := aiCtx.ApiConfig["language"].(string); ok && lang != "" {
		body["language"] = lang
	} else {
		body["language"] = "en"
	}
	if topK := jsonConfigInt(aiCtx.ApiConfig, "top_k", 0); topK > 0 {
		body["top_k"] = topK
	}
	if minScore, ok := jsonConfigFloat(aiCtx.ApiConfig, "min_score"); ok {
		body["min_score"] = minScore
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", false, fmt.Errorf("marshal RAG request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", false, fmt.Errorf("create RAG request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if headers, ok := aiCtx.ApiConfig["headers"].(map[string]any); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok && s != "" {
				req.Header.Set(k, s)
			}
		}
	}
	// Convenience: api_key field → X-API-Key when header not set
	if req.Header.Get("X-API-Key") == "" {
		if key, ok := aiCtx.ApiConfig["api_key"].(string); ok && key != "" {
			req.Header.Set("X-API-Key", key)
		}
	}

	// Use a client whose Timeout matches RAG budget. The shared App.HTTPClient
	// is often 30s and would abort free-model RAG calls early even when
	// timeout_seconds is 45–60. Keep Transport (SSRF-safe dialer) when present.
	client := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	if a.HTTPClient != nil && a.HTTPClient.Transport != nil {
		client.Transport = a.HTTPClient.Transport
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("RAG request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("RAG API status %d: %s", resp.StatusCode, truncateLogValue(string(respBody), 200))
	}

	var result struct {
		Answer    string `json:"answer"`
		Abstained bool   `json:"abstained"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", false, fmt.Errorf("parse RAG response: %w", err)
	}

	a.Log.Info("RAG context answered",
		"context", aiCtx.Name,
		"url", redactURLForLog(chatURL),
		"answer_len", len(result.Answer),
		"abstained", result.Abstained,
	)
	return strings.TrimSpace(result.Answer), result.Abstained, nil
}

// tryRAGResponse attempts matching type=rag AI contexts in priority order.
// ok=true means at least one RAG context was eligible (matched keywords).
func (a *App) tryRAGResponse(orgID uuid.UUID, whatsAppAccount string, session *models.ChatbotSession, userMessage string) (answer string, ok bool, err error) {
	contexts, err := a.getAIContextsCached(orgID, whatsAppAccount)
	if err != nil || len(contexts) == 0 {
		return "", false, nil
	}

	matched := filterMatchingAIContexts(contexts, userMessage)
	var ragList []models.AIContext
	for _, c := range matched {
		if c.ContextType == models.ContextTypeRAG {
			ragList = append(ragList, c)
		}
	}
	if len(ragList) == 0 {
		return "", false, nil
	}

	var lastErr error
	for _, ragCtx := range ragList {
		ans, abstained, callErr := a.callRAGContext(ragCtx, session, userMessage)
		if callErr != nil {
			a.Log.Warn("RAG context call failed", "context", ragCtx.Name, "error", callErr)
			lastErr = callErr
			continue
		}
		if abstained || ans == "" {
			a.Log.Info("RAG context abstained or empty", "context", ragCtx.Name, "abstained", abstained)
			continue
		}
		return ans, true, nil
	}
	// Eligible but no usable answer
	return "", true, lastErr
}

// ragUserFacingFallback is sent when RAG is configured but returns no usable
// answer and no local LLM fallback is available. Prefer settings.FallbackMessage.
const ragUserFacingFallback = "I could not find that in our documents. Please try rephrasing your question, or choose Contact Us / Contact Expert from the menu."

// generateAIResponse generates a reply for free-text / flow AI nodes.
// Priority: matching RAG AI Contexts (external retrieve+generate) → local LLM
// with static/api context only (never injects rag blobs).
//
// Always prefers a user-visible string over a hard error when RAG was tried
// and failed/abstained without a local provider — so WhatsApp users are not
// left with silence after an ai_response node or free-text fallback.
func (a *App) generateAIResponse(settings *models.ChatbotSettings, session *models.ChatbotSession, userMessage string) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", fmt.Errorf("empty user message for AI")
	}

	whatsAppAccount := ""
	if session != nil {
		whatsAppAccount = session.WhatsAppAccount
	}
	orgID := uuid.Nil
	if settings != nil {
		orgID = settings.OrganizationID
	}
	if orgID == uuid.Nil && session != nil {
		orgID = session.OrganizationID
	}

	// 1) External RAG short-circuit (no full-document dump into local LLM)
	ragTried := false
	if answer, tried, err := a.tryRAGResponse(orgID, whatsAppAccount, session, userMessage); tried {
		ragTried = true
		if answer != "" {
			return cleanAIResponse(answer), nil
		}
		if err != nil {
			a.Log.Warn("RAG failed, falling back to local AI provider if configured", "error", err)
		} else {
			a.Log.Info("RAG abstained/empty, falling back to local AI provider if configured")
		}
	}

	// 2) Legacy local provider path
	localReady := settings != nil && settings.AI.Enabled && settings.AI.Provider != "" && settings.AI.APIKey != ""
	if !localReady {
		if ragTried || a.hasEnabledRAGContext(orgID, whatsAppAccount) {
			// Soft fallback — still a successful reply for the chat layer
			fallback := ragUserFacingFallback
			if settings != nil && strings.TrimSpace(settings.FallbackMessage) != "" {
				fallback = settings.FallbackMessage
			}
			a.Log.Info("Returning user-facing RAG/local fallback message")
			return fallback, nil
		}
		return "", fmt.Errorf("AI provider not configured")
	}

	// Build context from static/api AIContext entries only (keyword-filtered)
	contextData := a.buildAIContext(orgID, session, userMessage)

	var answer string
	var err error

	switch settings.AI.Provider {
	case models.AIProviderOpenAI:
		answer, err = a.generateOpenAIResponse(settings, session, userMessage, contextData)
	case models.AIProviderAnthropic:
		answer, err = a.generateAnthropicResponse(settings, session, userMessage, contextData)
	case models.AIProviderGoogle:
		answer, err = a.generateGoogleResponse(settings, session, userMessage, contextData)
	case models.AIProviderOpenRouter:
		answer, err = a.generateOpenRouterResponse(settings, session, userMessage, contextData)
	default:
		return "", fmt.Errorf("unsupported AI provider: %s", settings.AI.Provider)
	}

	if err != nil {
		// If local LLM fails after RAG already failed, still give the user text
		if ragTried {
			fallback := ragUserFacingFallback
			if strings.TrimSpace(settings.FallbackMessage) != "" {
				fallback = settings.FallbackMessage
			}
			a.Log.Warn("Local AI failed after RAG; using fallback message", "error", err)
			return fallback, nil
		}
		return "", err
	}

	return cleanAIResponse(answer), nil
}

// cleanAIResponse cleans the AI response content by stripping reasoning/thinking blocks
func cleanAIResponse(text string) string {
	// Strip <think>...</think> tags if they exist (standard for thinking models)
	reThink := regexp.MustCompile(`(?s)<think>.*?</think>`)
	text = reThink.ReplaceAllString(text, "")

	// Strip <thought>...</thought> tags if they exist
	reThought := regexp.MustCompile(`(?s)<thought>.*?</thought>`)
	text = reThought.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}

// buildAIContext fetches and combines static/api AI context data for the local LLM.
// type=rag is never injected here (handled by tryRAGResponse). Only contexts that
// match trigger keywords (or have none) are included, highest priority first.
func (a *App) buildAIContext(orgID uuid.UUID, session *models.ChatbotSession, userMessage string) string {
	// Get WhatsApp account for cache key
	whatsAppAccount := ""
	if session != nil {
		whatsAppAccount = session.WhatsAppAccount
	}

	// Use cached AI contexts
	contexts, err := a.getAIContextsCached(orgID, whatsAppAccount)
	if err != nil || len(contexts) == 0 {
		return ""
	}

	matched := filterMatchingAIContexts(contexts, userMessage)
	var contextParts []string

	for _, ctx := range matched {
		var content string

		switch ctx.ContextType {
		case models.ContextTypeStatic:
			content = ctx.StaticContent

		case models.ContextTypeAPI:
			// Start with static content/prompt if provided
			content = ctx.StaticContent

			// Fetch data from external API and append
			apiContent, err := a.fetchAPIContext(ctx.ApiConfig, session, userMessage)
			if err != nil {
				a.Log.Error("Failed to fetch API context", "context_name", ctx.Name, "error", err)
				// Still use static content if API fails
			} else if apiContent != "" {
				if content != "" {
					content = content + "\n\nData:\n" + apiContent
				} else {
					content = apiContent
				}
			}

		case models.ContextTypeRAG:
			// Handled separately in tryRAGResponse — never dump into local prompt.
			continue
		}

		if content != "" {
			contextParts = append(contextParts, fmt.Sprintf("### %s\n%s", ctx.Name, content))
		}
	}

	if len(contextParts) == 0 {
		return ""
	}

	return "## Context Information\n\n" + strings.Join(contextParts, "\n\n")
}

// fetchAPIContext fetches context data from an external API
func (a *App) fetchAPIContext(apiConfig models.JSONB, session *models.ChatbotSession, userMessage string) (string, error) {
	if apiConfig == nil {
		return "", fmt.Errorf("API config is empty")
	}

	// Build session data for variable replacement
	sessionData := models.JSONB{}
	if session != nil {
		sessionData = session.SessionData
		if sessionData == nil {
			sessionData = models.JSONB{}
		}
		sessionData["phone_number"] = session.PhoneNumber
		sessionData["user_message"] = userMessage
	}

	replaceVar := func(s string) string { return processTemplate(s, sessionData) }
	respBody, statusCode, err := a.executeConfiguredAPI(apiConfig, replaceVar)
	if err != nil {
		return "", err
	}

	if statusCode < 200 || statusCode >= 300 {
		return "", fmt.Errorf("API returned status %d", statusCode)
	}

	// Check for response_path to extract specific field
	if responsePath, ok := apiConfig["response_path"].(string); ok && responsePath != "" {
		var jsonResp map[string]any
		if err := json.Unmarshal(respBody, &jsonResp); err == nil {
			if value := getNestedValue(jsonResp, responsePath); value != nil {
				return formatValue(value), nil
			}
		}
	}

	return string(respBody), nil
}

// generateOpenAIResponse generates a response using OpenAI API
func (a *App) generateOpenAIResponse(settings *models.ChatbotSettings, session *models.ChatbotSession, userMessage string, contextData string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"

	// Build messages array
	messages := []map[string]string{}

	// Build system prompt with context
	systemPrompt := settings.AI.SystemPrompt
	if contextData != "" {
		if systemPrompt != "" {
			systemPrompt = systemPrompt + "\n\n" + contextData
		} else {
			systemPrompt = contextData
		}
	}

	// Add system prompt if configured
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// Add conversation history if enabled
	if settings.AI.IncludeHistory && session != nil {
		history := a.getSessionHistory(session.ID, settings.AI.HistoryLimit)
		for _, msg := range history {
			role := "user"
			if msg.Direction == models.DirectionOutgoing {
				role = "assistant"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": msg.Message,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userMessage,
	})

	payload := map[string]any{
		"model":      settings.AI.Model,
		"messages":   messages,
		"max_tokens": settings.AI.MaxTokens,
	}

	if settings.AI.Temperature > 0 {
		payload["temperature"] = settings.AI.Temperature
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.AI.APIKey)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)
		return "", fmt.Errorf("OpenAI API error: %s", errResp.Error.Message)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) > 0 {
		return strings.TrimSpace(result.Choices[0].Message.Content), nil
	}

	return "", fmt.Errorf("no response from OpenAI")
}

// generateAnthropicResponse generates a response using Anthropic API
func (a *App) generateAnthropicResponse(settings *models.ChatbotSettings, session *models.ChatbotSession, userMessage string, contextData string) (string, error) {
	url := "https://api.anthropic.com/v1/messages"

	// Build messages array
	messages := []map[string]string{}

	// Add conversation history if enabled
	if settings.AI.IncludeHistory && session != nil {
		history := a.getSessionHistory(session.ID, settings.AI.HistoryLimit)
		for _, msg := range history {
			role := "user"
			if msg.Direction == models.DirectionOutgoing {
				role = "assistant"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": msg.Message,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userMessage,
	})

	payload := map[string]any{
		"model":      settings.AI.Model,
		"messages":   messages,
		"max_tokens": settings.AI.MaxTokens,
	}

	// Build system prompt with context
	systemPrompt := settings.AI.SystemPrompt
	if contextData != "" {
		if systemPrompt != "" {
			systemPrompt = systemPrompt + "\n\n" + contextData
		} else {
			systemPrompt = contextData
		}
	}

	// Add system prompt if configured
	if systemPrompt != "" {
		payload["system"] = systemPrompt
	}

	if settings.AI.Temperature > 0 {
		payload["temperature"] = settings.AI.Temperature
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", settings.AI.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)
		return "", fmt.Errorf("anthropic API error: %s", errResp.Error.Message)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	for _, content := range result.Content {
		if content.Type == "text" {
			return strings.TrimSpace(content.Text), nil
		}
	}

	return "", fmt.Errorf("no text response from Anthropic")
}

// generateGoogleResponse generates a response using Google Gemini API
func (a *App) generateGoogleResponse(settings *models.ChatbotSettings, session *models.ChatbotSession, userMessage string, contextData string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		settings.AI.Model, settings.AI.APIKey)

	// Build contents array
	contents := []map[string]any{}

	// Add conversation history if enabled
	if settings.AI.IncludeHistory && session != nil {
		history := a.getSessionHistory(session.ID, settings.AI.HistoryLimit)
		for _, msg := range history {
			role := "user"
			if msg.Direction == models.DirectionOutgoing {
				role = "model"
			}
			contents = append(contents, map[string]any{
				"role": role,
				"parts": []map[string]string{
					{"text": msg.Message},
				},
			})
		}
	}

	// Add current user message
	contents = append(contents, map[string]any{
		"role": "user",
		"parts": []map[string]string{
			{"text": userMessage},
		},
	})

	payload := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": settings.AI.MaxTokens,
		},
	}

	// Build system prompt with context
	systemPrompt := settings.AI.SystemPrompt
	if contextData != "" {
		if systemPrompt != "" {
			systemPrompt = systemPrompt + "\n\n" + contextData
		} else {
			systemPrompt = contextData
		}
	}

	// Add system instruction if configured
	if systemPrompt != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		}
	}

	if settings.AI.Temperature > 0 {
		payload["generationConfig"].(map[string]any)["temperature"] = settings.AI.Temperature
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)
		return "", fmt.Errorf("google AI API error: %s", errResp.Error.Message)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text), nil
	}

	return "", fmt.Errorf("no response from Google AI")
}

// generateOpenRouterResponse generates a response using OpenRouter API
func (a *App) generateOpenRouterResponse(settings *models.ChatbotSettings, session *models.ChatbotSession, userMessage string, contextData string) (string, error) {
	url := "https://openrouter.ai/api/v1/chat/completions"

	// Build messages array
	messages := []map[string]string{}

	// Build system prompt with context
	systemPrompt := settings.AI.SystemPrompt
	if contextData != "" {
		if systemPrompt != "" {
			systemPrompt = systemPrompt + "\n\n" + contextData
		} else {
			systemPrompt = contextData
		}
	}

	// Add system prompt if configured
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// Add conversation history if enabled
	if settings.AI.IncludeHistory && session != nil {
		history := a.getSessionHistory(session.ID, settings.AI.HistoryLimit)
		for _, msg := range history {
			role := "user"
			if msg.Direction == models.DirectionOutgoing {
				role = "assistant"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": msg.Message,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userMessage,
	})

	payload := map[string]any{
		"model":      settings.AI.Model,
		"messages":   messages,
		"max_tokens": settings.AI.MaxTokens,
	}

	if settings.AI.Temperature > 0 {
		payload["temperature"] = settings.AI.Temperature
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.AI.APIKey)
	req.Header.Set("HTTP-Referer", "https://whatomate.com")
	req.Header.Set("X-Title", "Whatomate")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)
		if errResp.Error.Message != "" {
			return "", fmt.Errorf("OpenRouter API error: %s", errResp.Error.Message)
		}
		return "", fmt.Errorf("OpenRouter API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) > 0 {
		return strings.TrimSpace(result.Choices[0].Message.Content), nil
	}

	return "", fmt.Errorf("no response from OpenRouter")
}

// getSessionHistory retrieves recent messages from the session
func (a *App) getSessionHistory(sessionID uuid.UUID, limit int) []models.ChatbotSessionMessage {
	var messages []models.ChatbotSessionMessage
	a.DB.Where("session_id = ?", sessionID).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages)

	// Reverse to get chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages
}

// Reaction represents a reaction on a message
type Reaction struct {
	Emoji     string `json:"emoji"`
	FromPhone string `json:"from_phone,omitempty"` // Phone number if from contact
	FromUser  string `json:"from_user,omitempty"`  // User ID if from agent
}

// handleIncomingReaction handles incoming reaction messages from WhatsApp
func (a *App) handleIncomingReaction(account *models.WhatsAppAccount, fromPhone, messageWAMID, emoji, profileName string) {
	a.Log.Info("Handling incoming reaction",
		"from", fromPhone,
		"message_wamid", messageWAMID,
		"emoji", emoji,
	)

	// Find the message being reacted to
	// WhatsApp encodes phone numbers in the WAMID prefix, so the same message
	// has different WAMIDs from sender vs recipient perspective.
	// We match on the suffix after "FQIA" + 4 chars (type indicator like "ERgS" or "EhgU")
	var message models.Message
	if err := a.DB.Where("whats_app_message_id = ?", messageWAMID).First(&message).Error; err != nil {
		// Try matching on WAMID suffix (the unique message ID part)
		if idx := strings.Index(messageWAMID, "FQIA"); idx != -1 {
			// Extract suffix after "FQIA" + 4 char type indicator (e.g., "ERgS", "EhgU")
			suffixStart := idx + 8
			if suffixStart < len(messageWAMID) {
				suffix := messageWAMID[suffixStart:]
				if err := a.DB.Where("whats_app_message_id LIKE ?", "%"+suffix).First(&message).Error; err != nil {
					a.Log.Warn("Message not found for reaction", "wamid", messageWAMID, "suffix", suffix)
					return
				}
			} else {
				a.Log.Warn("Message not found for reaction - invalid WAMID format", "wamid", messageWAMID)
				return
			}
		} else {
			a.Log.Warn("Message not found for reaction - no FQIA pattern", "wamid", messageWAMID)
			return
		}
	}

	// Get or create contact
	contact, _, _ := contactutil.GetOrCreateContact(a.DB, account.OrganizationID, fromPhone, profileName)

	// Parse existing reactions from Metadata
	var metadata map[string]any
	if message.Metadata != nil {
		metadata = message.Metadata
	} else {
		metadata = make(map[string]any)
	}

	// Get or initialize reactions array
	var reactions []Reaction
	if reactionsRaw, ok := metadata["reactions"]; ok {
		if reactionsArray, ok := reactionsRaw.([]any); ok {
			for _, r := range reactionsArray {
				if rMap, ok := r.(map[string]any); ok {
					emoji, _ := rMap["emoji"].(string)
					reactions = append(reactions, Reaction{
						Emoji:     emoji,
						FromPhone: getStringFromMap(rMap, "from_phone"),
						FromUser:  getStringFromMap(rMap, "from_user"),
					})
				}
			}
		}
	}

	// Remove existing reaction from this contact (each contact can only have one reaction)
	var newReactions []Reaction
	for _, r := range reactions {
		if r.FromPhone != fromPhone {
			newReactions = append(newReactions, r)
		}
	}

	// Add new reaction if emoji is not empty (empty = remove reaction)
	if emoji != "" {
		newReactions = append(newReactions, Reaction{
			Emoji:     emoji,
			FromPhone: fromPhone,
		})
	}

	// Update metadata
	metadata["reactions"] = newReactions

	// Save to database
	if err := a.DB.Model(&message).Update("metadata", metadata).Error; err != nil {
		a.Log.Error("Failed to update message reactions", "error", err)
		return
	}

	a.Log.Info("Updated message reaction", "message_id", message.ID, "reactions_count", len(newReactions))

	// Broadcast via WebSocket
	a.broadcastReactionUpdate(account.OrganizationID, message.ID, contact.ID, newReactions)
}

// Helper function to safely get string from map
func getStringFromMap(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// MediaInfo holds media-related information for an incoming message
type MediaInfo struct {
	MediaURL      string
	MediaMimeType string
	MediaFilename string
}

// saveIncomingMessage saves an incoming message to the messages table
func (a *App) saveIncomingMessage(account *models.WhatsAppAccount, contact *models.Contact, whatsappMsgID, msgType, content string, mediaInfo *MediaInfo, replyToWAMID string) {
	now := time.Now()

	message := models.Message{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    account.OrganizationID,
		WhatsAppAccount:   account.Name,
		ContactID:         contact.ID,
		WhatsAppMessageID: whatsappMsgID,
		Direction:         models.DirectionIncoming,
		MessageType:       models.MessageType(msgType),
		Content:           content,
		Status:            models.MessageStatusReceived,
	}

	// Handle reply context - look up the original message by WhatsApp message ID
	if replyToWAMID != "" {
		var replyToMsg models.Message
		if err := a.DB.Where("whats_app_message_id = ?", replyToWAMID).First(&replyToMsg).Error; err == nil {
			message.IsReply = true
			message.ReplyToMessageID = &replyToMsg.ID
		} else {
			a.Log.Warn("Reply-to message not found", "reply_to_wamid", replyToWAMID)
		}
	}

	// Add media fields if present
	if mediaInfo != nil {
		message.MediaURL = mediaInfo.MediaURL
		message.MediaMimeType = mediaInfo.MediaMimeType
		message.MediaFilename = mediaInfo.MediaFilename
	}

	if err := a.DB.Create(&message).Error; err != nil {
		a.Log.Error("Failed to save incoming message", "error", err)
		return
	}

	// If the chatbot will handle this conversation (enabled + no active
	// agent transfer), pre-mark the message as read so the contact-list
	// unread badge doesn't briefly flash before the bot's reply arrives.
	// See issue #280.
	if a.willChatbotHandle(account, contact) {
		a.DB.Model(&models.Message{}).Where("id = ?", message.ID).
			Update("status", models.MessageStatusRead)
		message.Status = models.MessageStatusRead
	}

	// Update contact's last message info
	preview := content
	if len(preview) > 100 {
		preview = preview[:97] + "..."
	}
	if msgType != "text" && msgType != "button_reply" && msgType != "nfm_reply" {
		preview = "[" + msgType + "]"
	}

	a.DB.Model(contact).Updates(map[string]any{
		"last_message_at":      now,
		"last_message_preview": preview,
		"is_read":              false,
		"whats_app_account":    account.Name,
		"last_inbound_at":      now,
	})

	a.Log.Info("Saved incoming message", "message_id", message.ID, "contact_id", contact.ID, "media_url", message.MediaURL)

	// Broadcast new message via WebSocket
	a.broadcastNewMessage(account.OrganizationID, &message, contact)

	// Dispatch webhook for incoming message
	a.DispatchWebhook(account.OrganizationID, models.WebhookEventMessageIncoming, MessageEventData{
		MessageID:       message.ID.String(),
		ContactID:       contact.ID.String(),
		ContactPhone:    contact.PhoneNumber,
		ContactName:     contact.ProfileName,
		MessageType:     models.MessageType(msgType),
		Content:         content,
		WhatsAppAccount: account.Name,
		Direction:       models.DirectionIncoming,
	})
}

// isWithinBusinessHours checks if current time is within configured business hours
func (a *App) isWithinBusinessHours(businessHours models.JSONBArray) bool {
	now := time.Now()
	currentDay := int(now.Weekday()) // 0 = Sunday, 1 = Monday, etc.
	currentTime := now.Format("15:04")

	for _, bh := range businessHours {
		bhMap, ok := bh.(map[string]any)
		if !ok {
			continue
		}

		// Get day (0-6, Sunday-Saturday)
		day, ok := bhMap["day"].(float64)
		if !ok {
			continue
		}

		if int(day) != currentDay {
			continue
		}

		// Check if enabled for this day
		enabled, ok := bhMap["enabled"].(bool)
		if !ok || !enabled {
			return false // Day exists but is disabled
		}

		// Get start and end times
		startTime, ok := bhMap["start_time"].(string)
		if !ok {
			continue
		}
		endTime, ok := bhMap["end_time"].(string)
		if !ok {
			continue
		}

		// Compare times (simple string comparison works for HH:MM format)
		if currentTime >= startTime && currentTime <= endTime {
			return true
		}
		return false // Found the day but outside hours
	}

	// If no matching day found, assume outside business hours
	return false
}

// isSimpleGreeting checks if a message is a simple, common greeting
func isSimpleGreeting(msg string) bool {
	m := strings.TrimSpace(strings.ToLower(msg))
	if len(m) > 12 {
		return false
	}
	greetings := map[string]bool{
		"hi":          true,
		"hello":       true,
		"hey":         true,
		"hello there": true,
		"hi there":    true,
		"hola":        true,
		"start":       true,
		"menu":        true,
		"get started": true,
		"hihi":        true,
		"hii":         true,
		"hiii":        true,
		"helo":        true,
		"namaste":     true,
	}
	return greetings[m]
}

// isContactOrLocationQuery checks if the user is asking for contact, maps, or address
func isContactOrLocationQuery(msg string) bool {
	m := strings.TrimSpace(strings.ToLower(msg))
	keywords := []string{"contact", "address", "location", "maps", "office", "bengaluru", "direction", "where is"}
	for _, k := range keywords {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// getContactCardText returns the formatted static contact details card
func getContactCardText() string {
	return "📞 *Darvi Group – Contact Details*\n\n" +
		"📍 *Address:*\n" +
		"Darvi Group Office, Bengaluru, Karnataka, India\n\n" +
		"🗺️ *Google Maps Link:*\n" +
		"https://maps.google.com/?q=Darvi+Group+Bengaluru\n\n" +
		"💬 *Customer Support:*\n" +
		"For specific queries or other assistance, please reach out to our support team."
}

// sendGreetingMenu sends the default greeting response with buttons if configured
func (a *App) sendGreetingMenu(account *models.WhatsAppAccount, contact *models.Contact, settings *models.ChatbotSettings) error {
	body := settings.DefaultResponse
	if body == "" {
		// No greeting configured — nothing to send.
		return nil
	}
	if len(settings.GreetingButtons) > 0 {
		greetingButtons := make([]map[string]any, 0)
		for _, btn := range settings.GreetingButtons {
			if btnMap, ok := btn.(map[string]any); ok {
				greetingButtons = append(greetingButtons, btnMap)
			}
		}
		if len(greetingButtons) > 0 {
			return a.sendAndSaveInteractiveButtons(account, contact, body, greetingButtons)
		}
	}
	return a.sendAndSaveTextMessage(account, contact, body)
}


