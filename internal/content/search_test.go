package content

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		value                         string
		organization, repository, ref string
		wantError                     bool
	}{
		{"", "", "", "", false},
		{"Acme/widgets", "Acme", "widgets", "", false},
		{"Acme/widgets@release/v2", "Acme", "widgets", "release/v2", false},
		{"widgets", "", "", "", true},
		{"Acme/widgets@", "", "", "", true},
	}
	for _, test := range tests {
		organization, repository, ref, err := parseSource(test.value)
		if (err != nil) != test.wantError {
			t.Fatalf("parseSource(%q) error = %v", test.value, err)
		}
		if organization != test.organization || repository != test.repository || ref != test.ref {
			t.Fatalf("parseSource(%q) = %q, %q, %q", test.value, organization, repository, ref)
		}
	}
}

func TestMetadataWhere(t *testing.T) {
	if got := metadataWhere(SearchParams{}); got != nil {
		t.Fatalf("empty filter = %#v", got)
	}
	want := map[string]any{"$and": []any{
		map[string]any{"organization": map[string]any{"$eq": "Acme"}},
		map[string]any{"repository": map[string]any{"$eq": "widgets"}},
	}}
	if got := metadataWhere(SearchParams{Organization: "Acme", Repository: "widgets"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("source filter = %#v", got)
	}
}

func TestPathDocumentWhere(t *testing.T) {
	tests := []struct {
		pattern string
		want    any
	}{
		{"", nil},
		{"docs/*", map[string]any{"$regex": `^Source: [^\n]+:docs/.*(?:\n|$)`}},
		{"**/*.go", map[string]any{"$regex": `^Source: [^\n]+:(?:.*/)?.*\.go(?:\n|$)`}},
		{"README?.md", map[string]any{"$regex": `^Source: [^\n]+:README.\.md(?:\n|$)`}},
		{"docs/café.md", map[string]any{"$regex": `^Source: [^\n]+:docs/café\.md(?:\n|$)`}},
	}
	for _, test := range tests {
		got, err := pathDocumentWhere(test.pattern)
		if err != nil {
			t.Fatalf("pathDocumentWhere(%q): %v", test.pattern, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("pathDocumentWhere(%q) = %#v, want %#v", test.pattern, got, test.want)
		}
	}
	if _, err := pathDocumentWhere("docs/\nbad"); err == nil {
		t.Fatal("expected line break to fail")
	}
	filter, _ := pathDocumentWhere("docs/*")
	expression := filter.(map[string]any)["$regex"].(string)
	for _, document := range []string{
		"Source: Acme/widgets@main:docs/setup.md\nType: markdown\n\nsetup",
		"Source: Acme/widgets@main:docs/guides/setup.md\nType: markdown\n\nsetup",
	} {
		if !regexp.MustCompile(expression).MatchString(document) {
			t.Fatalf("docs/* did not match %q", document)
		}
	}
}

func TestSelectMatchesPrefersSourceDiversity(t *testing.T) {
	match := func(path, document string) SearchMatch {
		return SearchMatch{Document: document, Metadata: map[string]any{"organization": "Acme", "repository": "widgets", "branch": "main", "path": path}}
	}
	matches := []SearchMatch{
		match("a.go", "first"), match("a.go", "second"), match("a.go", "third"), match("b.go", "fourth"), match("c.go", "first"),
	}
	selected := selectMatches(matches, 4)
	if len(selected) != 4 {
		t.Fatalf("selected %d matches", len(selected))
	}
	paths := []string{metadataString(selected[0].Metadata, "path"), metadataString(selected[1].Metadata, "path"), metadataString(selected[2].Metadata, "path"), metadataString(selected[3].Metadata, "path")}
	if !reflect.DeepEqual(paths, []string{"a.go", "a.go", "b.go", "a.go"}) {
		t.Fatalf("selected paths = %#v", paths)
	}
}

func TestRenderSearchResultsProducesConciseAttributedMarkdown(t *testing.T) {
	matches := []SearchMatch{{
		Document: "Source: Acme/widgets@main:internal/config.go\nType: code\nSymbol: Load\n\nfunc Load() {}",
		Metadata: map[string]any{
			"organization": "Acme", "repository": "widgets", "branch": "main", "path": "internal/config.go",
			"start_line": float64(10), "end_line": float64(12), "language": "go", "symbol": "Load",
			"indexed_commit_sha": "abc123",
		},
	}}
	output := renderSearchResults(matches, "Acme/widgets@main")
	for _, expected := range []string{
		"Found 1 relevant repository excerpt", "**Repo:** `Acme/widgets@main`", "**File:** `internal/config.go#L10-L12`",
		"https://github.com/Acme/widgets/blob/abc123/internal/config.go#L10-L12", "Context: symbol `Load`",
		"```go\nfunc Load() {}\n```",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
	for _, unwanted := range []string{"repochunk:", "chunk_hash", "distance", "Source: Acme/widgets@main:"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output contains %q:\n%s", unwanted, output)
		}
	}
}

func TestRenderSearchResultsEmpty(t *testing.T) {
	output := renderSearchResults(nil, "Acme/widgets")
	if !strings.Contains(output, "omit `source`") {
		t.Fatalf("unexpected empty response: %s", output)
	}
}
