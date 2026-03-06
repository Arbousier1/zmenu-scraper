package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gocolly/colly"
)

const (
	defaultTargetURL = "https://docs.groupez.dev/zmenu/getting-started"
	fallbackPrefix   = "https://r.jina.ai/http://"
	browserUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

func main() {
	targetURL := strings.TrimSpace(os.Getenv("TARGET_URL"))
	if targetURL == "" {
		targetURL = defaultTargetURL
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		log.Fatalf("invalid TARGET_URL: %q", targetURL)
	}

	c := colly.NewCollector(
		colly.AllowedDomains(parsedURL.Hostname()),
		colly.UserAgent(browserUA),
	)

	var docContent []string
	fallbackUsed := false

	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en-US,en;q=0.9")
		r.Headers.Set("Cache-Control", "no-cache")
		r.Headers.Set("Pragma", "no-cache")
		log.Printf("Visiting: %s", r.URL.String())
	})

	c.OnHTML("title", func(e *colly.HTMLElement) {
		title := strings.TrimSpace(e.Text)
		if title != "" {
			docContent = append(docContent, fmt.Sprintf("Title: %s", title))
		}
	})

	c.OnHTML("main", func(e *colly.HTMLElement) {
		text := strings.TrimSpace(e.Text)
		if text != "" {
			docContent = append(docContent, text)
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		if r != nil && r.StatusCode == http.StatusForbidden {
			content, fetchErr := fetchWithFallback(targetURL)
			if fetchErr != nil {
				if r.Request != nil {
					log.Printf("Request failed: %s, err: %v, fallback err: %v", r.Request.URL, err, fetchErr)
				} else {
					log.Printf("Request failed with 403, err: %v, fallback err: %v", err, fetchErr)
				}
				return
			}
			fallbackUsed = true
			docContent = append(docContent, content)
			log.Printf("Target returned 403. Fallback fetch succeeded.")
			return
		}

		if r != nil && r.Request != nil {
			log.Printf("Request failed: %s, err: %v", r.Request.URL, err)
			return
		}
		log.Printf("Request failed: %v", err)
	})

	if err := c.Visit(targetURL); err != nil {
		log.Fatal(err)
	}

	if len(docContent) == 0 && !fallbackUsed {
		content, fetchErr := fetchWithFallback(targetURL)
		if fetchErr != nil {
			log.Printf("No content collected and fallback failed: %v", fetchErr)
		} else {
			fallbackUsed = true
			docContent = append(docContent, content)
			log.Printf("No direct content collected. Fallback fetch succeeded.")
		}
	}

	finalText := strings.TrimSpace(strings.Join(docContent, "\n\n"))
	if finalText == "" {
		finalText = fmt.Sprintf("No content collected from %s", targetURL)
	}

	if err := os.WriteFile("output.txt", []byte(finalText), 0o644); err != nil {
		log.Fatal("write output.txt failed:", err)
	}

	log.Printf("Scrape finished: %s", targetURL)
}

func fetchWithFallback(targetURL string) (string, error) {
	path := strings.TrimPrefix(targetURL, "https://")
	path = strings.TrimPrefix(path, "http://")
	fallbackURL := fallbackPrefix + path

	body, err := fetchText(fallbackURL)
	if err != nil {
		return "", fmt.Errorf("fallback request failed: %w", err)
	}
	return fmt.Sprintf("Source: %s\n\n%s", fallbackURL, body), nil
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

	return strings.TrimSpace(string(body)), nil
}
