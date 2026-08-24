package chromadb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is a ChromaDB v2 API client.
type Client struct {
	BaseURL    string
	Tenant     string
	Database   string
	httpClient *http.Client
}

func New(baseURL, tenant, database string) *Client {
	return &Client{
		BaseURL:    baseURL,
		Tenant:     tenant,
		Database:   database,
		httpClient: &http.Client{},
	}
}

// collectionsBase returns the base path for collection operations.
func (c *Client) collectionsBase() string {
	return fmt.Sprintf("%s/api/v2/tenants/%s/databases/%s/collections",
		c.BaseURL, c.Tenant, c.Database)
}

// Collection represents a ChromaDB collection.
type Collection struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// EnsureCollection creates or gets an existing collection.
func (c *Client) EnsureCollection(name string, metadata map[string]interface{}) (*Collection, error) {
	body := map[string]interface{}{
		"name":           name,
		"get_or_create":  true,
		"configuration": map[string]interface{}{
			"hnsw": map[string]interface{}{
				"space":         "cosine",
				"ef_construction": 200,
				"ef_search":     128,
			},
		},
	}
	if metadata != nil {
		body["metadata"] = metadata
	}

	resp, err := c.doJSON(http.MethodPost, c.collectionsBase(), body)
	if err != nil {
		return nil, fmt.Errorf("ensure collection %s: %w", name, err)
	}

	var coll Collection
	if err := json.Unmarshal(resp, &coll); err != nil {
		return nil, fmt.Errorf("parse collection response: %w", err)
	}
	return &coll, nil
}

// GetCollection retrieves a collection by name.
func (c *Client) GetCollection(name string) (*Collection, error) {
	url := c.collectionsBase() + "/" + name
	resp, err := c.doJSON(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("get collection %s: %w", name, err)
	}

	var coll Collection
	if err := json.Unmarshal(resp, &coll); err != nil {
		return nil, fmt.Errorf("parse collection response: %w", err)
	}
	return &coll, nil
}

// DeleteCollection deletes a collection by ID.
func (c *Client) DeleteCollection(collectionID string) error {
	url := c.collectionsBase() + "/" + collectionID
	resp, err := c.doJSON(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("delete collection %s: %w", collectionID, err)
	}
	_ = resp // 204 No Content expected
	return nil
}

// AddDocuments adds documents with embeddings to a collection.
func (c *Client) AddDocuments(collectionID string, ids []string, documents []string, metadatas []map[string]interface{}, embeddings [][]float32) error {
	if len(ids) != len(documents) || len(ids) != len(metadatas) || len(ids) != len(embeddings) {
		return fmt.Errorf("ids, documents, metadatas, and embeddings must have the same length")
	}

	body := map[string]interface{}{
		"ids":         ids,
		"documents":   documents,
		"metadatas":   metadatas,
		"embeddings":  embeddings,
	}

	url := c.collectionsBase() + "/" + collectionID + "/add"
	resp, err := c.doJSON(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("add documents to collection %s: %w", collectionID, err)
	}
	_ = resp // {"ids": [...]} or empty on success
	return nil
}

// UpsertDocuments upserts documents with embeddings to a collection.
func (c *Client) UpsertDocuments(collectionID string, ids []string, documents []string, metadatas []map[string]interface{}, embeddings [][]float32) error {
	if len(ids) != len(documents) || len(ids) != len(metadatas) || len(ids) != len(embeddings) {
		return fmt.Errorf("ids, documents, metadatas, and embeddings must have the same length")
	}

	body := map[string]interface{}{
		"ids":         ids,
		"documents":   documents,
		"metadatas":   metadatas,
		"embeddings":  embeddings,
	}

	url := c.collectionsBase() + "/" + collectionID + "/upsert"
	resp, err := c.doJSON(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("upsert documents to collection %s: %w", collectionID, err)
	}
	_ = resp // {"ids": [...]} or empty on success
	return nil
}

// QueryResult represents a single query result.
type QueryResult struct {
	ID        string            `json:"id"`
	Document  string            `json:"document,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Distance  float32           `json:"distance,omitempty"`
}

// Query performs a semantic search on a collection.
func (c *Client) Query(collectionID string, queryEmbeddings [][]float32, nResults int, where map[string]interface{}, include []string) ([]QueryResult, error) {
	body := map[string]interface{}{
		"query_embeddings": queryEmbeddings,
		"n_results":        nResults,
	}
	if where != nil {
		body["where"] = where
	}
	if include == nil {
		include = []string{"documents", "metadatas", "distances"}
	}
	body["include"] = include

	url := c.collectionsBase() + "/" + collectionID + "/query"
	resp, err := c.doJSON(http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("query collection %s: %w", collectionID, err)
	}

	var result struct {
		Documents  [][]string                  `json:"documents"`
		Metadatas  [][]map[string]interface{}   `json:"metadatas"`
		Distances  [][]float32                 `json:"distances"`
		Ids        [][]string                  `json:"ids"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse query response: %w", err)
	}

	var results []QueryResult
	for i := range result.Ids[0] {
		r := QueryResult{
			ID:       result.Ids[0][i],
			Distance: result.Distances[0][i],
		}
		if len(result.Documents) > 0 && len(result.Documents[0]) > i {
			r.Document = result.Documents[0][i]
		}
		if len(result.Metadatas) > 0 && len(result.Metadatas[0]) > i {
			r.Metadata = result.Metadatas[0][i]
		}
		results = append(results, r)
	}

	return results, nil
}

// ListCollections returns all collections in the database.
func (c *Client) ListCollections() ([]Collection, error) {
	url := c.collectionsBase()
	resp, err := c.doJSON(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}

	var result struct {
		Collections []Collection `json:"collections"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		// ChromaDB v2 returns an array directly in some versions
		var colls []Collection
		if err2 := json.Unmarshal(resp, &colls); err2 == nil {
			return colls, nil
		}
		return nil, fmt.Errorf("parse list collections response: %w", err)
	}

	return result.Collections, nil
}

// Count returns the number of documents in a collection.
func (c *Client) Count(collectionID string) (int, error) {
	url := c.collectionsBase() + "/" + collectionID + "/count"
	resp, err := c.doJSON(http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("count documents in collection %s: %w", collectionID, err)
	}

	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("parse count response: %w", err)
	}
	return result.Count, nil
}

// DeleteDocuments deletes documents from a collection.
func (c *Client) DeleteDocuments(collectionID string, ids []string) error {
	body := map[string]interface{}{
		"ids": ids,
	}

	url := c.collectionsBase() + "/" + collectionID + "/delete"
	resp, err := c.doJSON(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete documents from collection %s: %w", collectionID, err)
	}
	_ = resp // {"ids": [...]} or empty on success
	return nil
}

// DeleteByFilter deletes documents matching a filter.
func (c *Client) DeleteByFilter(collectionID string, where map[string]interface{}) error {
	body := map[string]interface{}{
		"where": where,
	}

	url := c.collectionsBase() + "/" + collectionID + "/delete"
	resp, err := c.doJSON(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete documents from collection %s: %w", collectionID, err)
	}
	_ = resp // {"ids": [...]} or empty on success
	return nil
}

// doJSON performs an HTTP request and returns the response body as JSON bytes.
func (c *Client) doJSON(method, url string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return bodyBytes, nil
}
