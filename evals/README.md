# Evals — recall + reasoning-grounded chat

Minimal LongMemEval-style harness: ingest fact records into a scratch
workspace, then score queries by recall of an expected answer substring.

## Files

- `run.go` — the harness. Usage:
  `go run evals/run.go -base http://localhost:8091 -file evals/data/sample.jsonl`
  Set `VECTORIZER_API_KEY` if the server requires auth.
- `data/sample.jsonl` — smoke set (8 fact records + 5 recall queries).
  Fast; run on every PR.
- `data/longmemeval_slice.jsonl` — multi-session slice (8 facts spread
  across 4 sessions + 6 queries). The last query needs two-hop
  reasoning (headcount x shortlist x policy); with the LLM brain enabled
  the harness tries the agentic `/chat` endpoint first
  (reasoning-grounded) and falls back to plain search scoring.

## Record format (one JSON object per line)

Ingest: `{"workspace_id": ..., "session_id": ..., "role": "user"|"assistant", "content": ...}`
Query: `{"workspace_id": ..., "query": ..., "expected": "<substring>"}`

A query with no `role` is tried via chat first when a brain is enabled;
the harness reports `reasoning-grounded N` alongside plain recall.

## Release gate

Releases are gated on the smoke set: `evals/gate.sh` runs the sample
file and fails below `EVAL_MIN_RECALL` (default `0.8`). Run the
longmemeval slice before minor/major releases; investigate any
reasoning-grounded miss before tagging.

Long-term benchmark notes live in the team blog, not in this repo
(previous doc pointer to a `blog/` directory referred to external notes).
