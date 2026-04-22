package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher downloads chart images and caches Last-Modified to avoid redundant downloads.
type Fetcher struct {
	client       *http.Client
	lastModified map[string]string
}

func New() *Fetcher {
	return &Fetcher{
		client:       &http.Client{Timeout: 30 * time.Second},
		lastModified: make(map[string]string),
	}
}

// Fetch downloads the image at url. Returns the image bytes, whether the image
// changed since the last call (always true on first call), and any error.
func (f *Fetcher) Fetch(url string) ([]byte, bool, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("building request for %s: %w", url, err)
	}

	if lm, ok := f.lastModified[url]; ok {
		req.Header.Set("If-Modified-Since", lm)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("reading body from %s: %w", url, err)
	}

	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		f.lastModified[url] = lm
	}

	return data, true, nil
}
