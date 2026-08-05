package tools

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"strings"
	"time"
)

// ReadURLContent fetches a web URL and converts HTML to clean readable text
func ReadURLContent(targetURL string) (string, error) {
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	client := &http.Client{
		Timeout: 30 * time.Second, // 30s timeout to prevent context deadline exceeded
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") {
			return "", fmt.Errorf("request to '%s' timed out (network slow)", targetURL)
		}
		return "", fmt.Errorf("failed to fetch URL '%s': %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP request returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	htmlContent := string(bodyBytes)
	cleanText := extractCleanTextFromHTML(htmlContent)

	if len(cleanText) > 8000 {
		cleanText = cleanText[:8000] + "\n\n...[Content Truncated at 8000 characters]"
	}

	return cleanText, nil
}

// WebSearch performs web search via DuckDuckGo HTML endpoint
func WebSearch(query string) (string, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	client := &http.Client{
		Timeout: 30 * time.Second, // 30s timeout
	}

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create search request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout") {
			return "", fmt.Errorf("web search timed out for query '%s'", query)
		}
		return "", fmt.Errorf("web search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read search response: %w", err)
	}

	results := parseDuckDuckGoResults(string(bodyBytes))
	if len(results) == 0 {
		return fmt.Sprintf("No search results found for query: '%s'", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 Web Search Results for '%s':\n\n", query))
	for i, res := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n    URL: %s\n    Snippet: %s\n\n", i+1, res.Title, res.URL, res.Snippet))
		if i >= 4 { // Top 5 results
			break
		}
	}

	return sb.String(), nil
}

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

func parseDuckDuckGoResults(html string) []SearchResult {
	var results []SearchResult

	titleRegex := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetRegex := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

	titles := titleRegex.FindAllStringSubmatch(html, -1)
	snippets := snippetRegex.FindAllStringSubmatch(html, -1)

	for i := 0; i < len(titles); i++ {
		link := titles[i][1]
		if strings.Contains(link, "uddg=") {
			if u, err := url.Parse(link); err == nil {
				if actual := u.Query().Get("uddg"); actual != "" {
					link = actual
				}
			}
		}

		titleText := stripHTMLTags(titles[i][2])
		snippetText := ""
		if i < len(snippets) {
			snippetText = stripHTMLTags(snippets[i][1])
		}

		if titleText != "" && link != "" {
			results = append(results, SearchResult{
				Title:   titleText,
				URL:     link,
				Snippet: snippetText,
			})
		}
	}

	return results
}

func extractCleanTextFromHTML(html string) string {
	reScript := regexp.MustCompile(`(?s)<script.*?>.*?</script>`)
	reStyle := regexp.MustCompile(`(?s)<style.*?>.*?</style>`)
	html = reScript.ReplaceAllString(html, "")
	html = reStyle.ReplaceAllString(html, "")

	text := stripHTMLTags(html)

	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	return strings.Join(cleanLines, "\n")
}

func stripHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}
