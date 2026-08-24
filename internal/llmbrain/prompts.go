package llmbrain

import "fmt"

func AgentSystemPrompt(observer, observed string, observerCard, observedCard []string) string {
	obsCard := ""; if len(observerCard)>0 { obsCard = fmt.Sprintf("\nKnown about %s (observer):\n%s", observer, joinLines(observerCard)) }
	obsdCard := ""; if len(observedCard)>0 { obsdCard = fmt.Sprintf("\nKnown about %s (observed):\n%s", observed, joinLines(observedCard)) }
	perspective := ""
	if observer != observed {
		perspective = fmt.Sprintf("You are answering from %s's perspective about %s. This is directional.", observer, observed)
	} else {
		perspective = fmt.Sprintf("You are answering about '%s'.", observed)
	}
	return fmt.Sprintf(`You are a helpful context synthesis agent answering about users via memory tools.
%s
%s%s

Peer cards are constructed summaries from same observations, not separate source.

AVAILABLE TOOLS (respond with JSON {"tool":"name","args":{...}} if you need more context, else answer directly):
- search_memory: {"query":"..."} semantic over conclusions
- search_messages: {"query":"..."} semantic over messages
- grep_messages: {"pattern":"..."} exact text match
- get_reasoning_chain: {"conclusion_id":"..."} premises for grounding
- get_observation_context: {"chunk_id":"...","session_id":"..."} surrounding messages

Workflow: analyze query, search preferences first for advice questions, then strategic gather, then answer.`, perspective, obsCard, obsdCard)
}

func joinLines(lines []string) string {
	s := ""; for i, l := range lines { if i>0 { s+="\n" }; s+=l }; return s
}
