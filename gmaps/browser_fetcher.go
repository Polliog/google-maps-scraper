package gmaps

import (
	"context"
	"fmt"
	"time"

	"github.com/gosom/scrapemate"
)

const browserFetchTimeout = 15 * time.Second

// pageBrowserFetcher adapts a scrapemate.BrowserPage into a BrowserFetcher
// so the email pipeline can use an already-allocated browser page for
// Level 3 rendering.
type pageBrowserFetcher struct {
	page scrapemate.BrowserPage
}

// browserResult carries the result of a browser fetch from a goroutine.
type browserResult struct {
	html string
	err  error
}

func (f *pageBrowserFetcher) FetchWithBrowser(ctx context.Context, url string) (string, error) {
	if f.page == nil {
		return "", fmt.Errorf("browser page is nil, cannot fetch %s", url)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Run browser navigation in a goroutine so we can:
	// 1. Enforce a timeout (page.Goto with WaitUntilNetworkIdle can hang)
	// 2. Recover from panics when the underlying playwright page has been
	//    closed or recycled by scrapemate's browser pool between Fetch and Process.
	ch := make(chan browserResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- browserResult{"", fmt.Errorf("browser page panic (page likely recycled): %v", r)}
			}
		}()

		_, err := f.page.Goto(url, scrapemate.WaitUntilNetworkIdle)
		if err != nil {
			ch <- browserResult{"", err}

			return
		}

		f.page.WaitForTimeout(500 * time.Millisecond)

		html, err := f.page.Content()
		ch <- browserResult{html, err}
	}()

	// Use the shorter of the context deadline and browserFetchTimeout.
	timer := time.NewTimer(browserFetchTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("browser fetch timeout for %s", url)
	case r := <-ch:
		return r.html, r.err
	}
}
