package store

// EstimateTokens ~4 chars per token (tiktoken approximation)
func EstimateTokens(text string) int { return (len(text) + 3) / 4 }
func (s *Store) EstimateTokens(text string) int { return EstimateTokens(text) }

// FitContextWithinTokens selects newest messages + truncated representation within budget
func FitContextWithinTokens(messages []map[string]interface{}, representation string, budget int) ([]map[string]interface{}, string) {
	return fitContextWithinTokens(messages, representation, budget)
}
func (s *Store) FitContextWithinTokens(messages []map[string]interface{}, representation string, budget int) ([]map[string]interface{}, string) {
	return fitContextWithinTokens(messages, representation, budget)
}
func fitContextWithinTokens(messages []map[string]interface{}, representation string, budget int) ([]map[string]interface{}, string) {
	if budget <= 0 { budget = 10000 }
	repTokens := EstimateTokens(representation)
	remaining := budget - repTokens
	if remaining < 0 {
		// Truncate representation
		maxChars := budget * 4
		if maxChars < len(representation) { representation = representation[:maxChars] }
		return nil, representation
	}
	// Pick newest messages first
	var selected []map[string]interface{}
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		doc, _ := messages[i]["document"].(string)
		t := EstimateTokens(doc)
		if used+t > remaining { break }
		used += t
		selected = append([]map[string]interface{}{messages[i]}, selected...)
	}
	return selected, representation
}
