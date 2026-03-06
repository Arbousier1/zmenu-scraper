package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTargetURL = "https://docs.groupez.dev/zmenu/getting-started"
	defaultMaxPages  = 80
	fallbackPrefix   = "https://r.jina.ai/http://"
	browserUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var (
	absoluteURLPattern  = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
	markdownLinkPattern = regexp.MustCompile(`\]\(([^)]+)\)`)
)

type pageResult struct {
	URL     string
	Source  string
	Content string
}

func main() {
	startURL := strings.TrimSpace(os.Getenv("TARGET_URL"))
	if startURL == "" {
		startURL = defaultTargetURL
	}

	parsedStart, err := url.Parse(startURL)
	if err != nil || parsedStart.Scheme == "" || parsedStart.Host == "" {
		log.Fatalf("invalid TARGET_URL: %q", startURL)
	}

	crawlPrefix := normalizePrefix(os.Getenv("CRAWL_PREFIX"))
	if crawlPrefix == "" {
		crawlPrefix = derivePrefixFromPath(parsedStart.Path)
	}

	maxPages := parsePositiveIntEnv("MAX_PAGES", defaultMaxPages)
	log.Printf("crawl start: %s, prefix: %s, max pages: %d", startURL, crawlPrefix, maxPages)

	pages, err := crawlAllPages(startURL, crawlPrefix, maxPages)
	if err != nil {
		log.Fatal(err)
	}
	if len(pages) == 0 {
		log.Fatalf("no pages collected from %s", startURL)
	}

	finalText := buildOutput(startURL, crawlPrefix, pages)
	if err := os.WriteFile("output.txt", []byte(finalText), 0o644); err != nil {
		log.Fatal("write output.txt failed:", err)
	}

	log.Printf("scrape finished. pages: %d", len(pages))
}

func crawlAllPages(startURL, crawlPrefix string, maxPages int) ([]pageResult, error) {
	startParsed, _ := url.Parse(startURL)
	allowedHost := strings.ToLower(startParsed.Hostname())
	startCanonical, ok := normalizeDocURL(startURL, allowedHost, crawlPrefix)
	if !ok {
		return nil, fmt.Errorf("start URL does not match host/prefix: %s", startURL)
	}

	queue := []string{startCanonical}
	inQueue := map[string]bool{startCanonical: true}
	prefixRoot := "https://" + startParsed.Host + strings.TrimSuffix(crawlPrefix, "/")
	if rootCanonical, rootOK := normalizeDocURL(prefixRoot, allowedHost, crawlPrefix); rootOK && rootCanonical != startCanonical {
		queue = append(queue, rootCanonical)
		inQueue[rootCanonical] = true
	}
	visited := make(map[string]bool)
	pages := make([]pageResult, 0, maxPages)
	failed := make([]string, 0)

	for len(queue) > 0 && len(pages) < maxPages {
		currentURL := queue[0]
		queue = queue[1:]
		delete(inQueue, currentURL)
		if visited[currentURL] {
			continue
		}
		visited[currentURL] = true

		content, source, err := fetchPageContent(currentURL)
		if err != nil {
			log.Printf("skip %s: %v", currentURL, err)
			failed = append(failed, currentURL)
			continue
		}
		pages = append(pages, pageResult{
			URL:     currentURL,
			Source:  source,
			Content: content,
		})
		log.Printf("collected (%d): %s", len(pages), currentURL)

		links := extractDocLinks(content, currentURL, allowedHost, crawlPrefix)
		for _, link := range links {
			if visited[link] || inQueue[link] {
				continue
			}
			queue = append(queue, link)
			inQueue[link] = true
		}
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("all pages failed. tried: %d", len(visited))
	}
	if len(failed) > 0 {
		log.Printf("failed pages: %d", len(failed))
	}
	return pages, nil
}

func fetchPageContent(pageURL string) (string, string, error) {
	fallbackURL := toFallbackURL(pageURL)
	body, err := fetchText(fallbackURL)
	if err != nil {
		return "", "", fmt.Errorf("fallback fetch failed: %w", err)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", fmt.Errorf("empty body from fallback")
	}
	return body, fallbackURL, nil
}

func extractDocLinks(content, baseURL, allowedHost, crawlPrefix string) []string {
	found := make(map[string]struct{})
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	addLink := func(raw string) {
		raw = cleanURLToken(raw)
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "mailto:") {
			return
		}
		u, err := base.Parse(raw)
		if err != nil {
			return
		}
		normalized, ok := normalizeDocURL(u.String(), allowedHost, crawlPrefix)
		if !ok || normalized == baseURL {
			return
		}
		found[normalized] = struct{}{}
	}

	for _, hit := range absoluteURLPattern.FindAllString(content, -1) {
		addLink(hit)
	}

	for _, match := range markdownLinkPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		addLink(match[1])
	}

	links := make([]string, 0, len(found))
	for link := range found {
		links = append(links, link)
	}
	sort.Strings(links)
	return links
}

func buildOutput(startURL, crawlPrefix string, pages []pageResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Start URL: %s\n", startURL))
	sb.WriteString(fmt.Sprintf("Crawl Prefix: %s\n", crawlPrefix))
	sb.WriteString(fmt.Sprintf("Pages: %d\n", len(pages)))
	sb.WriteString(fmt.Sprintf("Generated At: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	for i, page := range pages {
		sb.WriteString("================================================================================\n")
		sb.WriteString(fmt.Sprintf("Page %d/%d\n", i+1, len(pages)))
		sb.WriteString(fmt.Sprintf("URL: %s\n", page.URL))
		sb.WriteString(fmt.Sprintf("Source: %s\n", page.Source))
		sb.WriteString("================================================================================\n")
		sb.WriteString(strings.TrimSpace(page.Content))
		sb.WriteString("\n\n")
	}

	return strings.TrimSpace(sb.String()) + "\n"
}

func fetchText(rawURL string) (string, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/plain, text/html;q=0.9, */*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func normalizeDocURL(rawURL, allowedHost, crawlPrefix string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(u.Hostname(), allowedHost) {
		return "", false
	}

	path := strings.TrimSpace(u.EscapedPath())
	if path == "" {
		path = "/"
	}

	prefixNoSlash := strings.TrimSuffix(crawlPrefix, "/")
	if path != prefixNoSlash && !strings.HasPrefix(path, crawlPrefix) {
		return "", false
	}

	clean := &url.URL{
		Scheme: "https",
		Host:   strings.ToLower(u.Host),
		Path:   path,
	}
	if clean.Path != "/" {
		clean.Path = strings.TrimRight(clean.Path, "/")
		if clean.Path == "" {
			clean.Path = "/"
		}
	}
	return clean.String(), true
}

func derivePrefixFromPath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return "/"
	}
	first := strings.Split(path, "/")[0]
	if first == "" {
		return "/"
	}
	return "/" + first + "/"
}

func normalizePrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if raw != "/" && !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	return raw
}

func parsePositiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Printf("invalid %s=%q, using %d", name, raw, fallback)
		return fallback
	}
	return value
}

func toFallbackURL(targetURL string) string {
	targetURL = strings.TrimPrefix(targetURL, "https://")
	targetURL = strings.TrimPrefix(targetURL, "http://")
	return fallbackPrefix + targetURL
}

func cleanURLToken(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "'\"")
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimSuffix(raw, ",")
	return raw
}
