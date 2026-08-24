package main

// honcho_sync: auto-discover new Honcho features vs Vectorizer parity.
// Run: go run scripts/honcho_sync.go
// Intended for CI cron (weekly) or manual: compares src/routers/*.py endpoints with Vectorizer routes in main.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

func fetch(url string) string {
	resp, _ := http.Get(url)
	if resp == nil { return "" }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func main() {
	// Fetch Honcho routers list
	api := "https://api.github.com/repos/plastic-labs/honcho/contents/src/routers"
	body := fetch(api)
	var items []struct{Name string `json:"name"`}
	_ = json.Unmarshal([]byte(body), &items)
	fmt.Println("Honcho routers:")
	for _, it := range items { fmt.Printf(" - %s\n", it.Name) }

	// Fetch Vectorizer routes
	data, _ := os.ReadFile("main.go")
	re := regexp.MustCompile(`api\.(Get|Post|Put|Delete)\("([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	fmt.Println("\nVectorizer routes:")
	for _, m := range matches { fmt.Printf(" %s %s\n", strings.ToUpper(m[1]), m[2]) }

	fmt.Println("\nGap check: compare above. Missing = candidate for next batch.")
	fmt.Println("Next auto-batch: derive from honcho changelog or new files in src/dialectic, src/dreamer, src/vector_store.")
}
