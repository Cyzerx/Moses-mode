package config

import "strings"

const DefaultOllamaBaseURL = "http://127.0.0.1:11434/v1"

// LLMEndpoint normalizes the format and base actually used to send a request.
// Use the same identity when deciding whether a credential may be retained.
func LLMEndpoint(provider, rawBase string) (string, string) {
	base := strings.TrimRight(strings.TrimSpace(rawBase), "/")
	provider = strings.TrimSpace(provider)
	switch provider {
	case "openai":
		base = strings.TrimSuffix(base, "/chat/completions")
	case "openai-responses":
		base = strings.TrimSuffix(base, "/responses")
	case "ollama":
		base = strings.TrimRight(strings.TrimSuffix(base, "/chat/completions"), "/")
		if base == "" {
			base = DefaultOllamaBaseURL
		} else if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
	default:
		provider = "anthropic"
		base = strings.TrimSuffix(base, "/v1/messages")
	}
	return provider, strings.TrimRight(base, "/")
}

func SameLLMEndpoint(providerA, baseA, providerB, baseB string) bool {
	a, aURL := LLMEndpoint(providerA, baseA)
	b, bURL := LLMEndpoint(providerB, baseB)
	return a == b && aURL == bURL
}
