package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Graph is a light file-backed graph loaded from GRAPH.json.
// No Postgres — just vault/00-index/GRAPH.json + in-memory BFS.
type Graph struct {
	mu       sync.RWMutex
	nodes    map[string]graphNode
	edges    []graphEdge
	byFrom   map[string][]graphEdge
	byTo     map[string][]graphEdge
	graphPath string
}

type graphNode struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

type graphEdge struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight"`
}

func NewGraph(graphPath string) *Graph {
	g := &Graph{graphPath: graphPath}
	g.Reload()
	return g
}

func (g *Graph) Reload() error {
	data, err := os.ReadFile(g.graphPath)
	if err != nil {
		return err
	}
	var raw struct {
		Nodes []map[string]interface{} `json:"nodes"`
		Edges []graphEdge              `json:"edges"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	nodes := make(map[string]graphNode, len(raw.Nodes))
	for _, n := range raw.Nodes {
		id, _ := n["id"].(string)
		typ, _ := n["type"].(string)
		label, _ := n["label"].(string)
		nodes[id] = graphNode{ID: id, Type: typ, Label: label}
	}
	byFrom := make(map[string][]graphEdge)
	byTo := make(map[string][]graphEdge)
	for _, e := range raw.Edges {
		byFrom[e.From] = append(byFrom[e.From], e)
		byTo[e.To] = append(byTo[e.To], e)
	}
	g.mu.Lock()
	g.nodes = nodes
	g.edges = raw.Edges
	g.byFrom = byFrom
	g.byTo = byTo
	g.mu.Unlock()
	return nil
}

// Expand performs 1-hop neighbor expansion from chunk/doc IDs.
// Returns neighbor IDs (excluding input). entity hops are weighted lower to avoid explosion.
func (g *Graph) Expand(ids []string, hops int, maxNeighbors int) []string {
	if g == nil || hops <= 0 || len(ids) == 0 {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.nodes) == 0 {
		return nil
	}
	seed := make(map[string]bool, len(ids))
	for _, id := range ids {
		seed[id] = true
	}
	frontier := append([]string(nil), ids...)
	visited := make(map[string]bool)
	for _, id := range ids {
		visited[id] = true
	}
	var expanded []string
	for h := 0; h < hops; h++ {
		var nextFrontier []string
		for _, cur := range frontier {
			for _, e := range g.byFrom[cur] {
				if visited[e.To] {
					continue
				}
				// avoid exploding via popular entities like "Session" — limit per-entity fanout
				if strings.HasPrefix(e.To, "entity:") {
					// only traverse entity if weight >=0.8 (domain entities)
					if e.Weight < 0.7 {
						continue
					}
				}
				visited[e.To] = true
				expanded = append(expanded, e.To)
				nextFrontier = append(nextFrontier, e.To)
				if len(expanded) >= maxNeighbors {
					return expanded
				}
			}
			for _, e := range g.byTo[cur] {
				if visited[e.From] {
					continue
				}
				if strings.HasPrefix(e.From, "entity:") && e.Weight < 0.7 {
					continue
				}
				visited[e.From] = true
				expanded = append(expanded, e.From)
				nextFrontier = append(nextFrontier, e.From)
				if len(expanded) >= maxNeighbors {
					return expanded
				}
			}
		}
		if len(nextFrontier) == 0 {
			break
		}
		frontier = nextFrontier
	}
	return expanded
}

// GraphPathFromEnv returns vault graph path from VAULT_ROOT env or default.
func GraphPathFromEnv() string {
	if v := os.Getenv("VAULT_ROOT"); v != "" {
		return filepath.Join(v, "maisarah", "vault", "00-index", "GRAPH.json")
	}
	// Windows default
	return `C:/Users/alfir/SynologyDrive/ai/maisarah/vault/00-index/GRAPH.json`
}
