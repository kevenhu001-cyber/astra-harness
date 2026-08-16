package tui

import "github.com/kevenhu001-cyber/astra-harness/internal/llm"

// modelPricing holds per-million-token USD pricing for popular models.
type modelPricing struct {
	Input  float64
	Output float64
}

// approximateCost returns an estimated USD cost for a model usage.
func approximateCost(model string, usage llm.Usage) float64 {
	p := pricingFor(model)
	in := float64(usage.InputTokens) / 1_000_000 * p.Input
	out := float64(usage.OutputTokens) / 1_000_000 * p.Output
	return in + out
}

func pricingFor(model string) modelPricing {
	pr := modelPricing{Input: 3.0, Output: 15.0} // Claude Sonnet default
	switch {
	case containsFold(model, "claude-opus-4"), containsFold(model, "claude-3-opus"):
		pr = modelPricing{Input: 15.0, Output: 75.0}
	case containsFold(model, "claude-sonnet-4"), containsFold(model, "claude-3-5-sonnet"):
		pr = modelPricing{Input: 3.0, Output: 15.0}
	case containsFold(model, "claude-haiku"), containsFold(model, "claude-3-haiku"):
		pr = modelPricing{Input: 0.80, Output: 4.0}
	case containsFold(model, "gpt-4o-mini"):
		pr = modelPricing{Input: 0.15, Output: 0.60}
	case containsFold(model, "gpt-4.1-mini"):
		pr = modelPricing{Input: 0.40, Output: 1.60}
	case containsFold(model, "gpt-4.1"), containsFold(model, "gpt-4o"):
		pr = modelPricing{Input: 2.50, Output: 10.0}
	case containsFold(model, "o1"), containsFold(model, "o3"):
		pr = modelPricing{Input: 15.0, Output: 60.0}
	case containsFold(model, "deepseek-reasoner"), containsFold(model, "deepseek-r1"):
		pr = modelPricing{Input: 0.55, Output: 2.19}
	case containsFold(model, "deepseek-chat"), containsFold(model, "deepseek-v3"):
		pr = modelPricing{Input: 0.27, Output: 1.10}
	case containsFold(model, "qwen-max"):
		pr = modelPricing{Input: 2.50, Output: 10.0}
	case containsFold(model, "qwen-plus"):
		pr = modelPricing{Input: 0.80, Output: 2.0}
	case containsFold(model, "qwen-coder"):
		pr = modelPricing{Input: 1.0, Output: 4.0}
	case containsFold(model, "llama3.1"), containsFold(model, "qwen2.5"):
		pr = modelPricing{Input: 0.0, Output: 0.0}
	}
	return pr
}
