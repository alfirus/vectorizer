// Package codeindex is the phase-1 prototype for codebase indexing mode:
// regex symbol extraction (Go + Python + TS) producing Vectorizer-native
// chunks + CALLS edges. Stdlib only. See README.md for the full spec.
package codeindex

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol is one top-level definition.
type Symbol struct {
	Name     string // bare name: "UpdateMessage"
	Kind     string // "func" | "type" | "class" | "interface" | "method"
	Language string
	Body     string // signature + full body
	Doc      string // leading comment block (may be "")
	Start    int    // line offset in file (0-based)
	End      int    // line offset past last body line
}

// FileSymbols is the parse result for one file.
type FileSymbols struct {
	Path     string
	Language string // "go" | "python" | "ts" | ""
	Imports  []string
	Symbols  []Symbol
	Overview string // package clause + import list + symbol index
}

// Chunk is one Vectorizer-ready unit with metadata.
type Chunk struct {
	Document string
	Metadata map[string]string // source_type, source_path, language, chunk_type, entities, parent_doc_id, importance
}

var langByExt = map[string]string{
	".go": "go", ".py": "python",
	".ts": "ts", ".tsx": "ts", ".js": "ts", ".jsx": "ts",
}

var (
	goFuncRe   = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	goTypeRe   = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:struct|interface|\(|=)`)
	pyDefRe    = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyClassRe  = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[\(:]`)
	tsFuncRe   = regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsClassRe  = regexp.MustCompile(`^(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsConstRe  = regexp.MustCompile(`^(?:export\s+)?const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_][A-Za-z0-9_]*)\s*=>`)
	goImportRe = regexp.MustCompile(`^\s*(?:import\s+(?:"([^"]+)"|\(\s*)|"([^"]+)")`)
	pyImportRe = regexp.MustCompile(`^\s*(?:import\s+([\w.]+)|from\s+([\w.]+)\s+import)`)
	tsImportRe = regexp.MustCompile(`^\s*import\s+(?:.*?\s+from\s+)?["']([^"']+)["']`)
)

// LanguageFor returns the indexed language for a path, or "" to skip.
func LanguageFor(path string) string {
	return langByExt[strings.ToLower(filepath.Ext(path))]
}

// ParseFile extracts imports + top-level symbols. Body capture is
// brace/dedent-bounded best-effort (prototype-grade, not a parser).
func ParseFile(path, src string) FileSymbols {
	lang := LanguageFor(path)
	fs := FileSymbols{Path: path, Language: lang}
	if lang == "" {
		return fs
	}
	lines := strings.Split(src, "\n")
	var imps []string
	for _, ln := range lines {
		var m []string
		switch lang {
		case "go":
			m = goImportRe.FindStringSubmatch(ln)
			if len(m) == 3 {
				if m[1] != "" {
					imps = append(imps, m[1])
				} else if m[2] != "" {
					imps = append(imps, m[2])
				}
			}
		case "python":
			m = pyImportRe.FindStringSubmatch(ln)
			if len(m) == 3 {
				if m[1] != "" {
					imps = append(imps, m[1])
				} else if m[2] != "" {
					imps = append(imps, m[2])
				}
			}
		case "ts":
			m = tsImportRe.FindStringSubmatch(ln)
			if len(m) == 2 && m[1] != "" {
				imps = append(imps, m[1])
			}
		}
	}
	fs.Imports = imps

	// Symbol scan with doc-comment capture.
	var pendingDoc []string
	pushDoc := func() string {
		d := strings.Join(pendingDoc, "\n")
		pendingDoc = nil
		return d
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		trim := strings.TrimSpace(ln)
		// collect doc comments
		if lang == "go" && strings.HasPrefix(trim, "//") {
			pendingDoc = append(pendingDoc, trim)
			continue
		}
		if (lang == "python" || lang == "ts") && strings.HasPrefix(trim, "#") {
			pendingDoc = append(pendingDoc, trim)
			continue
		}
		name, kind := "", ""
		switch lang {
		case "go":
			if m := goFuncRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "func"
			} else if m := goTypeRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "type"
			}
		case "python":
			if m := pyDefRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "func"
			} else if m := pyClassRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "class"
			}
		case "ts":
			if m := tsFuncRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "func"
			} else if m := tsClassRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "class"
			} else if m := tsConstRe.FindStringSubmatch(trim); m != nil {
				name, kind = m[1], "func"
			}
		}
		if name == "" {
			if trim != "" {
				pendingDoc = nil // code without symbol resets doc
			}
			continue
		}
		doc := pushDoc()
		end := bodyEnd(lines, i, lang)
		fs.Symbols = append(fs.Symbols, Symbol{
			Name: name, Kind: kind, Language: lang, Doc: doc,
			Body:   strings.Join(lines[i:end], "\n"),
			Start:  i, End: end,
		})
		i = end - 1 // continue after body
	}
	fs.Overview = buildOverview(fs)
	return fs
}

// bodyEnd finds the end of a symbol body: brace matching for go/ts,
// dedent for python.
func bodyEnd(lines []string, start int, lang string) int {
	if lang == "python" {
		base := indentOf(lines[start])
		end := start + 1
		for end < len(lines) {
			ln := lines[end]
			if strings.TrimSpace(ln) == "" {
				end++
				continue
			}
			if indentOf(ln) <= base && end > start+1 {
				break
			}
			end++
		}
		return end
	}
	// brace matching
	depth := 0
	seen := false
	for end := start; end < len(lines); end++ {
		for _, ch := range lines[end] {
			switch ch {
			case '{':
				depth++
				seen = true
			case '}':
				depth--
			}
		}
		if seen && depth <= 0 {
			return end + 1
		}
	}
	return len(lines)
}

func indentOf(s string) int {
	n := 0
	for _, ch := range s {
		if ch == ' ' {
			n++
		} else if ch == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

func buildOverview(fs FileSymbols) string {
	var b strings.Builder
	b.WriteString("File: " + fs.Path + " (" + fs.Language + ")\n")
	if len(fs.Imports) > 0 {
		b.WriteString("Imports: " + strings.Join(fs.Imports, ", ") + "\n")
	}
	b.WriteString("Symbols:\n")
	for _, s := range fs.Symbols {
		b.WriteString("- " + s.Kind + " " + s.Name + "\n")
	}
	return b.String()
}

// ChunkFile produces the overview chunk + one chunk per symbol (merged to
// minChars floor so tiny getters don't spam the collection).
func ChunkFile(path, src string, minChars int) []Chunk {
	if minChars <= 0 {
		minChars = 2000
	}
	fs := ParseFile(path, src)
	if fs.Language == "" {
		return nil
	}
	base := map[string]string{
		"source_type": "file", "source_path": path, "language": fs.Language,
		"parent_doc_id": path, "importance": "3",
	}
	var out []Chunk
	ov := map[string]string{}
	for k, v := range base {
		ov[k] = v
	}
	ov["chunk_type"] = "file_overview"
	ov["entities"] = symbolNames(fs.Symbols)
	out = append(out, Chunk{Document: fs.Overview, Metadata: ov})

	var buf strings.Builder
	var bufNames []string
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		m["chunk_type"] = "symbol"
		m["entities"] = strings.Join(bufNames, ",")
		out = append(out, Chunk{Document: buf.String(), Metadata: m})
		buf.Reset()
		bufNames = nil
	}
	for _, s := range fs.Symbols {
		text := s.Name + " (" + s.Kind + "):\n"
		if s.Doc != "" {
			text += s.Doc + "\n"
		}
		text += s.Body + "\n\n"
		if buf.Len() > 0 && buf.Len()+len(text) > minChars && buf.Len() >= minChars/2 {
			flush()
		}
		buf.WriteString(text)
		bufNames = append(bufNames, s.Name)
		if buf.Len() >= minChars {
			flush()
		}
	}
	flush()
	return out
}

func symbolNames(syms []Symbol) string {
	names := make([]string, 0, len(syms))
	for _, s := range syms {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

// CallEdge is a caller → callee pair (same-repo, name-matched).
type CallEdge struct {
	Caller string
	Callee string
	File   string // file containing the caller
}

// CallEdges finds CALLS pairs: any known symbol name mentioned in another
// symbol's body. Excludes self-calls. Prototype-grade: no scope resolution,
// stdlib-blind by construction (only known symbols match).
func CallEdges(files []FileSymbols) []CallEdge {
	known := map[string]bool{}
	for _, f := range files {
		for _, s := range f.Symbols {
			known[s.Name] = true
		}
	}
	seen := map[string]bool{}
	var out []CallEdge
	for _, f := range files {
		for _, s := range f.Symbols {
			for _, tok := range SplitTokens(s.Body) {
				if tok == s.Name || !known[tok] {
					continue
				}
				key := s.Name + "\x00" + tok
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, CallEdge{Caller: s.Name, Callee: tok, File: f.Path})
			}
		}
	}
	return out
}

// SplitTokens reuses the store tokenizer pattern locally (stdlib mirror:
// case/underscore/digit boundaries, lowercase) to avoid an import cycle
// with internal/store.
func SplitTokens(src string) []string {
	fields := strings.FieldsFunc(src, func(r rune) bool {
		return !(r == '_' || r == '$' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	set := map[string]bool{}
	var out []string
	for _, f := range fields {
		for _, sub := range splitIdentLocal(f) {
			if len(sub) >= 3 && !set[sub] {
				set[sub] = true
				out = append(out, sub)
			}
		}
	}
	return out
}

func splitIdentLocal(s string) []string {
	var parts []string
	start := 0
	runes := []rune(s)
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isLower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }
	for i := 1; i < len(runes); i++ {
		p, c := runes[i-1], runes[i]
		if (isLower(p) || isDigit(p)) && isUpper(c) {
			parts = append(parts, string(runes[start:i]))
			start = i
		} else if p == '_' || (isUpper(p) && isUpper(c)) {
			// underscore handled by FieldsFunc; acronym runs split below
			_ = c
		}
	}
	parts = append(parts, string(runes[start:]))
	var out []string
	for _, p := range parts {
		if l := strings.ToLower(p); l != "" {
			out = append(out, l)
		}
	}
	// also keep the full lowered identifier for exact match
	if full := strings.ToLower(strings.ReplaceAll(s, "_", "")); full != "" {
		out = append(out, full)
	}
	return out
}
