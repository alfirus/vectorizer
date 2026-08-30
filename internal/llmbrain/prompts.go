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

// VaultTagSystem is used by the librarian when tagging a new vault chunk.
const VaultTagSystem = `You are a vault librarian tagging markdown chunks for a 768d vector index.
Given header_path and chunk text, output ONLY JSON: {"tags":"tag1,tag2,tag3","summary":"one line 12 words max"}
Rules: 3-5 tags lowercase, single words or kebab-case, derived from header_path+content, keep domain terms (docker, vectorizer, bukku, dji, nomic), no stopwords. Summary is used for reranking, not embedding. No markdown, no extra keys.`

// VaultRerankSystem is used to rerank vector top-k before returning to the agent.
const VaultRerankSystem = `You are a vault librarian reranking chunks for a query.
Given query + numbered chunks (each: header_path, tags, snippet), output ONLY JSON: {"order":[3,0,1]} as relevance descending indexes. No explanation, no prose.`

func VaultTagUser(headerPath, chunk string) string {
	if len(chunk) > 1200 {
		chunk = chunk[:1200]
	}
	if headerPath == "" {
		headerPath = "(no header)"
	}
	return fmt.Sprintf("header_path: %s\nchunk:\n%s", headerPath, chunk)
}

func VaultRerankUser(query string, chunks []string) string {
	s := fmt.Sprintf("Query: %s\n\nChunks:\n", query)
	for i, c := range chunks {
		if len(c) > 400 {
			c = c[:400]
		}
		s += fmt.Sprintf("%d. %s\n", i, c)
	}
	return s
}

func joinLines(lines []string) string {
	s := ""; for i, l := range lines { if i>0 { s+="\n" }; s+=l }; return s
}
