package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/shridarpatil/whatomate/internal/crypto"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

type FlowAIEditRequest struct {
	Prompt string `json:"prompt"`
	Nodes  []any  `json:"nodes"`
	Edges  []any  `json:"edges"`
}

type OpenRouterRequest struct {
	Model          string               `json:"model"`
	Messages       []OpenRouterMessage  `json:"messages"`
	ResponseFormat map[string]string    `json:"response_format,omitempty"`
	Reasoning      *OpenRouterReasoning `json:"reasoning,omitempty"`
}

type OpenRouterReasoning struct {
	Enabled bool `json:"enabled"`
}

type OpenRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (a *App) FlowAIEdit(r *fastglue.Request) error {
	orgID, err := a.getOrgID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	var req FlowAIEditRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request payload", nil, "")
	}

	// Retrieve organization settings
	var org models.Organization
	if err := a.DB.Select("settings").Where("id = ?", orgID).First(&org).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Organization not found", nil, "")
	}

	// Extract and decrypt OpenRouter API Key
	apiKeyEnc, ok := org.Settings["openrouter_api_key"].(string)
	if !ok || apiKeyEnc == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "OpenRouter API Key not configured. Please set it in Settings -> AI Integration.", nil, "")
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, a.Config.App.EncryptionKey)
	if err != nil || apiKey == "" {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to decrypt OpenRouter API Key", nil, "")
	}

	model, ok := org.Settings["openrouter_default_model"].(string)
	if !ok || model == "" {
		model = "openai/gpt-4o-mini"
	}

	// Prepare graph data
	graphData, _ := json.Marshal(map[string]any{
		"nodes": req.Nodes,
		"edges": req.Edges,
	})

	systemPrompt := `You are an expert Chatbot Graph Editor. The user has provided their current graph state (nodes and edges) and a request. Modify the graph logically to fulfill the request. You must output pure, valid JSON matching this exact structure: { "nodes": [...], "edges": [...] }. Retain existing node/edge IDs where possible. If creating a new node, generate a unique ID using the prefix "node_ai_". Ensure every edge has a valid source, target, and sourceHandle.`

	userPrompt := "Current Graph:\n" + string(graphData) + "\n\nUser Request: " + req.Prompt

	orReq := OpenRouterRequest{
		Model: model,
		Messages: []OpenRouterMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
		Reasoning:      &OpenRouterReasoning{Enabled: true},
	}

	// Some reasoning models (like o1, o3, deepseek-r1) do not support response_format json_object.
	// For those models, remove the response_format to avoid API 400 errors.
	if strings.Contains(model, "o1-") || strings.Contains(model, "o3-") || strings.Contains(model, "deepseek-r1") || strings.Contains(model, "-reasoner") || strings.Contains(model, "-reasoning") || strings.Contains(model, "gpt-oss") {
		orReq.ResponseFormat = nil
	}

	reqBytes, _ := json.Marshal(orReq)
	httpReq, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(reqBytes))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create request", nil, "")
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("HTTP-Referer", "https://whatomate.com")
	httpReq.Header.Set("X-Title", "Whatomate Flow Copilot")

	resp, err := a.HTTPClient.Do(httpReq)
	if err != nil {
		a.Log.Error("OpenRouter request failed", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusBadGateway, "Failed to communicate with AI provider", nil, "")
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to read AI response", nil, "")
	}

	if resp.StatusCode != http.StatusOK {
		a.Log.Error("OpenRouter returned error", "status", resp.StatusCode, "body", string(bodyBytes))
		return r.SendErrorEnvelope(fasthttp.StatusBadGateway, "AI provider returned an error", nil, "")
	}

	var orResp OpenRouterResponse
	if err := json.Unmarshal(bodyBytes, &orResp); err != nil || len(orResp.Choices) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Invalid response from AI provider", nil, "")
	}

	content := orResp.Choices[0].Message.Content

	// Save raw content from AI to file for debugging
	_ = os.WriteFile("ai_copilot_raw.txt", []byte(content), 0644)

	// Robustly extract JSON block
	jsonStr, ok := extractJSON(content)
	if ok {
		_ = os.WriteFile("ai_copilot_extracted.json", []byte(jsonStr), 0644)
	}

	responsePayload := map[string]any{
		"raw_content": content,
	}

	if !ok {
		a.Log.Error("Failed to extract JSON from AI response", "content", content)
		responsePayload["error"] = "AI returned non-JSON content"
		return r.SendEnvelope(responsePayload)
	}

	var parsedContent map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsedContent); err != nil {
		a.Log.Error("AI returned malformed JSON", "json", jsonStr, "original_content", content, "error", err)
		responsePayload["error"] = "AI returned malformed JSON"
		return r.SendEnvelope(responsePayload)
	}

	if nodes, exists := parsedContent["nodes"]; exists {
		responsePayload["nodes"] = nodes
	} else {
		responsePayload["nodes"] = []any{}
	}

	if edges, exists := parsedContent["edges"]; exists {
		responsePayload["edges"] = edges
	} else {
		responsePayload["edges"] = []any{}
	}

	return r.SendEnvelope(responsePayload)
}

func extractJSON(content string) (string, bool) {
	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return "", false
	}

	// If it already starts with '{' and ends with '}', it's likely clean
	if strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}") {
		return content, true
	}

	// Try finding the first ```json block
	if idx := strings.Index(content, "```json"); idx != -1 {
		sub := content[idx+7:]
		if endIdx := strings.Index(sub, "```"); endIdx != -1 {
			return strings.TrimSpace(sub[:endIdx]), true
		}
	}

	// Try finding the first ``` block
	if idx := strings.Index(content, "```"); idx != -1 {
		sub := content[idx+3:]
		if endIdx := strings.Index(sub, "```"); endIdx != -1 {
			return strings.TrimSpace(sub[:endIdx]), true
		}
	}

	// Fallback to finding the first '{' and the last '}'
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start != -1 && end != -1 && end > start {
		return content[start : end+1], true
	}

	return "", false
}
