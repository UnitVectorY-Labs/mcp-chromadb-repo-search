package content

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultSearchLimit = 5
	maximumSearchLimit = 20
)

type SearchParams struct {
	Query        string
	Limit        int
	Organization string
	Repository   string
	Branch       string
	Path         string
}

func pathDocumentWhere(pattern string) (any, error) {
	pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "/"))
	if pattern == "" {
		return nil, nil
	}
	if strings.ContainsAny(pattern, "\r\n") {
		return nil, fmt.Errorf("path must not contain line breaks")
	}
	var expression strings.Builder
	expression.WriteString(`^Source: [^\n]+:`)
	runes := []rune(pattern)
	for index := 0; index < len(runes); {
		switch runes[index] {
		case '*':
			if index+2 < len(runes) && runes[index+1] == '*' && runes[index+2] == '/' {
				expression.WriteString(`(?:.*/)?`)
				index += 3
				continue
			}
			for index < len(runes) && runes[index] == '*' {
				index++
			}
			expression.WriteString(`.*`)
		case '?':
			expression.WriteByte('.')
			index++
		default:
			expression.WriteString(regexp.QuoteMeta(string(runes[index])))
			index++
		}
	}
	expression.WriteString(`(?:\n|$)`)
	return map[string]any{"$regex": expression.String()}, nil
}

type SearchMatch struct {
	Document string
	Metadata map[string]any
	Distance *float64
}

func parseSource(value string) (organization, repository, branch string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", "", nil
	}
	repositoryPart := value
	if before, after, found := strings.Cut(value, "@"); found {
		repositoryPart = before
		branch = strings.TrimSpace(after)
		if branch == "" {
			return "", "", "", fmt.Errorf("source branch must be non-empty after @")
		}
	}
	parts := strings.Split(repositoryPart, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", "", fmt.Errorf("source must use owner/repository or owner/repository@branch")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), branch, nil
}

func metadataWhere(params SearchParams) any {
	filters := make([]any, 0, 3)
	for _, item := range []struct{ key, value string }{
		{"organization", params.Organization},
		{"repository", params.Repository},
		{"branch", params.Branch},
	} {
		if item.value != "" {
			filters = append(filters, map[string]any{item.key: map[string]any{"$eq": item.value}})
		}
	}
	if len(filters) == 0 {
		return nil
	}
	if len(filters) == 1 {
		return filters[0]
	}
	return map[string]any{"$and": filters}
}

// selectMatches preserves relevance order while preferring results from different
// files. Deferred same-file matches fill any remaining result slots.
func selectMatches(matches []SearchMatch, limit int) []SearchMatch {
	selected := make([]SearchMatch, 0, limit)
	deferred := make([]SearchMatch, 0, len(matches))
	perFile := map[string]int{}
	seenContent := map[string]bool{}
	for _, match := range matches {
		content := cleanDocument(match.Document)
		if content == "" || seenContent[content] {
			continue
		}
		seenContent[content] = true
		key := metadataString(match.Metadata, "organization") + "/" + metadataString(match.Metadata, "repository") + "@" + metadataString(match.Metadata, "branch") + ":" + metadataString(match.Metadata, "path")
		if perFile[key] >= 2 {
			deferred = append(deferred, match)
			continue
		}
		selected = append(selected, match)
		perFile[key]++
		if len(selected) == limit {
			return selected
		}
	}
	for _, match := range deferred {
		selected = append(selected, match)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func renderSearchResults(matches []SearchMatch, source string) string {
	if len(matches) == 0 {
		message := "No relevant repository content was found. Try a broader or more specific query"
		if source != "" {
			message += ", or omit `source` to search all indexed repositories"
		}
		return message + "."
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Found %d relevant repository excerpt", len(matches))
	if len(matches) != 1 {
		output.WriteString("s")
	}
	output.WriteString(", ranked by semantic relevance.\n")

	for index, match := range matches {
		metadata := match.Metadata
		organization := metadataString(metadata, "organization")
		repository := metadataString(metadata, "repository")
		branch := metadataString(metadata, "branch")
		path := metadataString(metadata, "path")
		startLine := metadataInt(metadata, "start_line")
		endLine := metadataInt(metadata, "end_line")

		output.WriteString("\n---\n\n")
		fmt.Fprintf(&output, "### %d. `%s/%s@%s:%s", index+1, organization, repository, branch, path)
		if startLine > 0 {
			fmt.Fprintf(&output, "#L%d", startLine)
			if endLine > startLine {
				fmt.Fprintf(&output, "-L%d", endLine)
			}
		}
		output.WriteString("`\n\n")
		if sourceURL := githubSourceURL(metadata); sourceURL != "" {
			fmt.Fprintf(&output, "Source: [%s](%s)\n", path, sourceURL)
		}
		context := resultContext(metadata)
		if len(context) > 0 {
			output.WriteString("Context: " + strings.Join(context, " · ") + "\n")
		}
		output.WriteString("\n")
		content := cleanDocument(match.Document)
		fence := markdownFence(content)
		language := metadataString(metadata, "language")
		if language == "" && metadataString(metadata, "file_type") == "markdown" {
			language = "markdown"
		}
		output.WriteString(fence + language + "\n" + content + "\n" + fence + "\n")
	}
	return output.String()
}

func cleanDocument(document string) string {
	document = strings.TrimSpace(document)
	if strings.HasPrefix(document, "Source: ") {
		if _, content, found := strings.Cut(document, "\n\n"); found {
			return strings.TrimSpace(content)
		}
	}
	return document
}

func resultContext(metadata map[string]any) []string {
	items := make([]string, 0, 3)
	for _, field := range []string{"section", "symbol"} {
		if value := metadataString(metadata, field); value != "" {
			items = append(items, field+" `"+strings.ReplaceAll(value, "`", "\\`")+"`")
		}
	}
	return items
}

func githubSourceURL(metadata map[string]any) string {
	organization := metadataString(metadata, "organization")
	repository := metadataString(metadata, "repository")
	ref := metadataString(metadata, "indexed_commit_sha")
	if ref == "" {
		ref = metadataString(metadata, "branch")
	}
	path := metadataString(metadata, "path")
	if organization == "" || repository == "" || ref == "" || path == "" {
		return ""
	}
	segments := strings.Split(path, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	result := "https://github.com/" + url.PathEscape(organization) + "/" + url.PathEscape(repository) + "/blob/" + url.PathEscape(ref) + "/" + strings.Join(segments, "/")
	startLine := metadataInt(metadata, "start_line")
	endLine := metadataInt(metadata, "end_line")
	if startLine > 0 {
		result += "#L" + strconv.Itoa(startLine)
		if endLine > startLine {
			result += "-L" + strconv.Itoa(endLine)
		}
	}
	return result
}

func markdownFence(content string) string {
	longest := 2
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		count := 0
		for count < len(trimmed) && trimmed[count] == '`' {
			count++
		}
		if count > longest {
			longest = count
		}
	}
	return strings.Repeat("`", longest+1)
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func metadataInt(metadata map[string]any, key string) int {
	switch value := metadata[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := strconv.Atoi(string(value))
		return parsed
	}
	return 0
}
