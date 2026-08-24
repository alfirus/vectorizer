package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Minimal eval harness: ingest LongMemEval-style jsonl then query, score by recall of expected answer substring.
// Usage: go run evals/run.go -base http://localhost:8091 -file evals/data.jsonl
// Honcho parity: see https://honcho.dev/evals/ and blog long-term benchmarks.

type Record struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Expected    string `json:"expected,omitempty"`
	Query       string `json:"query,omitempty"`
}

func post(base, path string, body interface{}) {
	b,_:=json.Marshal(body)
	req,_:=http.NewRequest("POST", base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type","application/json")
	if key:=os.Getenv("VECTORIZER_API_KEY"); key!="" { req.Header.Set("X-API-Key", key) }
	http.DefaultClient.Do(req)
}

func main(){
	base:=flag.String("base","http://localhost:8091","vectorizer base URL")
	file:=flag.String("file","", "jsonl eval file")
	flag.Parse()
	if *file=="" { fmt.Fprintln(os.Stderr, "-file required"); os.Exit(1)}
	data,_:=os.ReadFile(*file)
	lines:=strings.Split(string(data), "\n")
	var queries []Record
	for _, line:=range lines {
		if strings.TrimSpace(line)=="" { continue }
		var r Record; if json.Unmarshal([]byte(line), &r)!=nil { continue }
		if r.Query!="" { queries=append(queries, r); continue }
		post(*base, "/api/v1/messages", map[string]string{"workspace_id":r.WorkspaceID,"session_id":r.SessionID,"role":r.Role,"content":r.Content})
		time.Sleep(50*time.Millisecond)
	}
	hit:=0; reasoningHits:=0
	for _, q:=range queries {
		// Prefer chat if available for agentic reasoning
		useChat := q.Role=="" && q.Expected!=""
		found:=false
		if useChat {
			// Try dialectic chat (observer/observed), fallback to search
			b2,_:=json.Marshal(map[string]string{"query":q.Query,"observed":"default"})
			req2,_:=http.NewRequest("POST", *base+"/api/v1/workspaces/"+q.WorkspaceID+"/chat", bytes.NewReader(b2))
			req2.Header.Set("Content-Type","application/json")
			if resp2, err:=http.DefaultClient.Do(req2); err==nil && resp2.StatusCode==200 {
				var chatOut struct{ Answer string `json:"answer"`}
				_ = json.NewDecoder(resp2.Body).Decode(&chatOut); resp2.Body.Close()
				if strings.Contains(strings.ToLower(chatOut.Answer), strings.ToLower(q.Expected)) { found=true; reasoningHits++ }
			} else if resp2!=nil { resp2.Body.Close() }
		}
		if !found {
			b,_:=json.Marshal(map[string]interface{}{"query":q.Query,"where":map[string]string{"workspace_id":q.WorkspaceID}})
			req,_:=http.NewRequest("POST", *base+"/api/v1/messages/search", bytes.NewReader(b))
			req.Header.Set("Content-Type","application/json")
			resp,_:=http.DefaultClient.Do(req)
			var out struct{ Results []struct{ Document string `json:"document"` } `json:"results"`}
			json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			for _, r:=range out.Results { if strings.Contains(strings.ToLower(r.Document), strings.ToLower(q.Expected)) { found=true; break } }
		}
		if found { hit++ }
		fmt.Printf("query=%q expected=%q hit=%v\n", q.Query, q.Expected, found)
	}
	denom := len(queries); if denom==0 { denom=1 }
	fmt.Printf("recall %d/%d = %.2f (reasoning-grounded %d)\n", hit, len(queries), float64(hit)/float64(denom), reasoningHits)
}
