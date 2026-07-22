package nodes

// Type identifies a graph node kind (mirrors handlers.ChatNodeType strings).
type Type string

const (
	Start       Type = "start"
	Message     Type = "message"
	Buttons     Type = "buttons"
	Prompt      Type = "prompt"
	APICall     Type = "api_call"
	Condition   Type = "condition"
	Timing      Type = "timing"
	SetVariable Type = "set_variable"
	AIResponse  Type = "ai_response"
	Transfer    Type = "transfer"
	Webhook     Type = "webhook"
	GotoFlow    Type = "goto_flow"
	WhatsAppFlow Type = "whatsapp_flow"
	End         Type = "end"
)

// Outcome is the result of executing a node (Phase 10 registry contract).
type Outcome struct {
	// Edge condition (default, button:id, true, …). Empty + Yield=false → terminal.
	Outcome string
	// Yield means park at this node until the next inbound.
	Yield bool
}

// Handler executes one node type. Full extract will move handler bodies here;
// Phase 10 establishes the registry contract and known type list.
type Handler interface {
	Type() Type
}

// Registry maps node type → handler.
type Registry struct {
	handlers map[Type]Handler
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[Type]Handler)}
}

// Register adds a handler for its type.
func (r *Registry) Register(h Handler) {
	if r == nil || h == nil {
		return
	}
	r.handlers[h.Type()] = h
}

// Get returns a handler or nil.
func (r *Registry) Get(t Type) Handler {
	if r == nil {
		return nil
	}
	return r.handlers[t]
}

// KnownTypes lists all v2 node types the product supports.
func KnownTypes() []Type {
	return []Type{
		Start, Message, Buttons, Prompt, APICall, Condition, Timing,
		SetVariable, AIResponse, Transfer, Webhook, GotoFlow, WhatsAppFlow, End,
	}
}

// IsKnown reports whether t is a supported node type.
func IsKnown(t Type) bool {
	for _, k := range KnownTypes() {
		if k == t {
			return true
		}
	}
	return false
}
