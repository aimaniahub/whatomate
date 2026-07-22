package ai

import (
	"strings"

	"github.com/shridarpatil/whatomate/internal/models"
)

// Stage names for timeline / logging (Phase 13).
const (
	StageKnowledge = "knowledge"
	StageRAG       = "rag"
	StageLLM       = "llm"
	StageFallback  = "fallback"
)

// PipelineMode maps free-text AI modes to ordered stages.
type PipelineMode string

const (
	ModeOff             PipelineMode = "off"
	ModeKnowledgeOnly   PipelineMode = "knowledge_only"
	ModeKnowledgeRAG    PipelineMode = "knowledge_rag"
	ModeKnowledgeRAGLLM PipelineMode = "knowledge_rag_llm"
	ModeLLMOnly         PipelineMode = "llm_only"
	// Legacy modes pass-through
	ModeRAGOnly       PipelineMode = "rag_only"
	ModeRAGThenLocal  PipelineMode = "rag_then_local"
	ModeLocalOnly     PipelineMode = "local_only"
)

// Candidate is a grounded or generative answer candidate.
type Candidate struct {
	Text       string
	Source     string // knowledge | rag | llm | fallback
	Confidence float64
	Abstained  bool
}

// Plan describes which stages to run.
type Plan struct {
	Mode           PipelineMode
	TryKnowledge   bool
	TryRAG         bool
	TryLLM         bool
	UseFallbackMsg bool
}

// ResolvePlan maps settings free-text mode + hasRAG into a pipeline plan.
func ResolvePlan(freeText models.FreeTextAIMode, hasRAG, localReady bool) Plan {
	// Map legacy free-text modes first.
	switch freeText {
	case models.FreeTextAIOff:
		return Plan{Mode: ModeOff}
	case models.FreeTextAIRAGOnly:
		return Plan{Mode: ModeRAGOnly, TryRAG: hasRAG, UseFallbackMsg: true}
	case models.FreeTextAIRAGThenLocal:
		return Plan{Mode: ModeRAGThenLocal, TryRAG: hasRAG, TryLLM: localReady, UseFallbackMsg: true}
	case models.FreeTextAILocalOnly:
		return Plan{Mode: ModeLocalOnly, TryLLM: localReady}
	}

	// Empty / legacy resolve (matches resolveFreeTextMode)
	if hasRAG {
		return Plan{Mode: ModeRAGOnly, TryRAG: true, UseFallbackMsg: true}
	}
	if localReady {
		return Plan{Mode: ModeLocalOnly, TryLLM: true}
	}
	return Plan{Mode: ModeOff}
}

// PickBest returns the first non-empty non-abstained candidate in order.
func PickBest(cands ...Candidate) (Candidate, bool) {
	for _, c := range cands {
		if c.Abstained {
			continue
		}
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		return c, true
	}
	return Candidate{}, false
}

// KnowledgeHit is a simple static/FAQ substring match against AIContext static content names/keywords.
// Used as Stage 1 before RAG when type=static contexts match (Phase 13 knowledge engine light).
func KnowledgeHit(contexts []models.AIContext, userMessage string) (Candidate, bool) {
	msg := strings.ToLower(strings.TrimSpace(userMessage))
	if msg == "" {
		return Candidate{}, false
	}
	// Priority sort copy
	list := make([]models.AIContext, 0, len(contexts))
	for _, c := range contexts {
		if !c.IsEnabled || c.ContextType != models.ContextTypeStatic {
			continue
		}
		list = append(list, c)
	}
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].Priority > list[i].Priority {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	for _, c := range list {
		// Require keyword match when keywords configured
		if len(c.TriggerKeywords) > 0 {
			hit := false
			for _, kw := range c.TriggerKeywords {
				kw = strings.TrimSpace(strings.ToLower(kw))
				if kw != "" && strings.Contains(msg, kw) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		// Knowledge "answer" only when static content is short enough to send directly
		// (FAQ style). Long docs stay for LLM context via buildAIContext.
		content := strings.TrimSpace(c.StaticContent)
		if content == "" {
			continue
		}
		if len(content) <= 1200 && len(c.TriggerKeywords) > 0 {
			return Candidate{Text: content, Source: StageKnowledge, Confidence: 0.7}, true
		}
	}
	return Candidate{}, false
}
