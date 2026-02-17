package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
)

type GmapJobOptions func(*GmapJob)

type GmapJob struct {
	scrapemate.Job

	MaxDepth     int
	LangCode     string
	ExtractEmail bool

	Deduper             deduper.Deduper
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
}

func NewGmapJob(
	id, langCode, query string,
	maxDepth int,
	extractEmail bool,
	geoCoordinates string,
	zoom int,
	opts ...GmapJobOptions,
) *GmapJob {
	query = url.QueryEscape(query)

	const (
		maxRetries = 3
		prio       = scrapemate.PriorityLow
	)

	if id == "" {
		id = uuid.New().String()
	}

	mapURL := ""
	if geoCoordinates != "" && zoom > 0 {
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s/@%s,%dz", query, strings.ReplaceAll(geoCoordinates, " ", ""), zoom)
	} else {
		// Warning: geo and zoom MUST be both set or not
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s", query)
	}

	job := GmapJob{
		Job: scrapemate.Job{
			ID:         id,
			Method:     http.MethodGet,
			URL:        mapURL,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: maxRetries,
			Priority:   prio,
		},
		MaxDepth:     maxDepth,
		LangCode:     langCode,
		ExtractEmail: extractEmail,
	}

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithDeduper(d deduper.Deduper) GmapJobOptions {
	return func(j *GmapJob) {
		j.Deduper = d
	}
}

func WithExitMonitor(e exiter.Exiter) GmapJobOptions {
	return func(j *GmapJob) {
		j.ExitMonitor = e
	}
}

func WithExtraReviews() GmapJobOptions {
	return func(j *GmapJob) {
		j.ExtractExtraReviews = true
	}
}

func (j *GmapJob) UseInResults() bool {
	return false
}

func (j *GmapJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	log := scrapemate.GetLoggerFromContext(ctx)

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}

	var next []scrapemate.IJob

	if strings.Contains(resp.URL, "/maps/place/") {
		jopts := []PlaceJobOptions{}
		if j.ExitMonitor != nil {
			jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
		}

		placeJob := NewPlaceJob(j.ID, j.LangCode, resp.URL, j.ExtractEmail, j.ExtractExtraReviews, jopts...)

		next = append(next, placeJob)
	} else {
		doc.Find(`div[role=feed] div[jsaction]>a`).Each(func(_ int, s *goquery.Selection) {
			if href := s.AttrOr("href", ""); href != "" {
				jopts := []PlaceJobOptions{}
				if j.ExitMonitor != nil {
					jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
				}

				nextJob := NewPlaceJob(j.ID, j.LangCode, href, j.ExtractEmail, j.ExtractExtraReviews, jopts...)

				if j.Deduper == nil || j.Deduper.AddIfNotExists(ctx, href) {
					next = append(next, nextJob)
				}
			}
		})
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(next))
		j.ExitMonitor.IncrSeedCompleted(1)
	}

	log.Info(fmt.Sprintf("%d places found", len(next)))

	return nil, next, nil
}

func (j *GmapJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetFullURL(), scrapemate.WaitUntilDOMContentLoaded)
	if err != nil {
		resp.Error = err

		return resp
	}

	clickRejectCookiesIfRequired(page)

	const defaultTimeout = 5 * time.Second

	err = page.WaitForURL(page.URL(), defaultTimeout)
	if err != nil {
		resp.Error = err

		return resp
	}

	resp.URL = pageResponse.URL
	resp.StatusCode = pageResponse.StatusCode
	resp.Headers = pageResponse.Headers

	// When Google Maps finds only 1 place, it slowly redirects to that place's URL
	// check element scroll
	sel := `div[role='feed']`

	err = page.WaitForSelector(sel, 700*time.Millisecond)

	var singlePlace bool

	if err != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, time.Second*5)
		defer waitCancel()

		singlePlace = waitUntilURLContains(waitCtx, page, "/maps/place/")

		waitCancel()
	}

	if singlePlace {
		resp.URL = page.URL()

		var body string

		body, err = page.Content()
		if err != nil {
			resp.Error = err
			return resp
		}

		resp.Body = []byte(body)

		return resp
	}

	scrollSelector := `div[role='feed']`

	_, err = scroll(ctx, page, j.MaxDepth, scrollSelector)
	if err != nil {
		resp.Error = err

		return resp
	}

	body, err := page.Content()
	if err != nil {
		resp.Error = err
		return resp
	}

	resp.Body = []byte(body)

	return resp
}

func waitUntilURLContains(ctx context.Context, page scrapemate.BrowserPage, s string) bool {
	ticker := time.NewTicker(time.Millisecond * 150)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if strings.Contains(page.URL(), s) {
				return true
			}
		}
	}
}

func clickRejectCookiesIfRequired(page scrapemate.BrowserPage) {
	// Use JavaScript to find and click - faster than multiple locator calls
	_, _ = page.Eval(`() => {
		// Try consent form buttons first
		const consentForm = document.querySelector('form[action*="consent.google"]');
		if (consentForm) {
			const btn = consentForm.querySelector('button, input[type="submit"]');
			if (btn) {
				btn.click();
				return true;
			}
		}
		// Try reject/decline buttons
		const buttons = document.querySelectorAll('button, input[type="submit"]');
		for (const btn of buttons) {
			const text = (btn.textContent || btn.value || '').toLowerCase();
			if (text.includes('reject') || text.includes('decline') || text.includes('ablehnen')) {
				btn.click();
				return true;
			}
		}
		return false;
	}`)
}

func scroll(ctx context.Context,
	page scrapemate.BrowserPage,
	maxDepth int,
	scrollSelector string,
) (int, error) {
	scrollExpr := `async () => {
		const el = document.querySelector("` + scrollSelector + `");
		el.scrollTop = el.scrollHeight;

		return new Promise((resolve) => {
			setTimeout(() => {
				resolve(el.scrollHeight);
			}, %d);
		});
	}`

	endOfListExpr := `() => {
		const el = document.querySelector("` + scrollSelector + `");
		if (!el) return false;
		const lastChild = el.lastElementChild;
		if (!lastChild) return false;
		// Google Maps shows an end-of-list marker as the last child
		// that contains a span inside a p.fontBodyMedium but no clickable links
		if (lastChild.querySelector('a[href]')) return false;
		const endMarker = lastChild.querySelector('span span');
		return endMarker !== null;
	}`

	var currentScrollHeight int
	scrollCount := 0
	staleCount := 0

	const (
		maxStaleRetries  = 3    // retry up to 3 times when scroll height doesn't change
		baseJsWaitMs     = 1500 // base wait for JS scroll to load new content
		staleExtraWaitMs = 1000 // extra wait per stale retry
		maxJsWaitMs      = 5000 // max JS wait time
		betweenScrollMs  = 500  // pause between successful scrolls
	)

	for scrollCount < maxDepth {
		select {
		case <-ctx.Done():
			return scrollCount, nil
		default:
		}

		// Increase JS wait time when retrying stale scrolls
		jsWait := baseJsWaitMs + (staleCount * staleExtraWaitMs)
		if jsWait > maxJsWaitMs {
			jsWait = maxJsWaitMs
		}

		scrollHeight, err := page.Eval(fmt.Sprintf(scrollExpr, jsWait))
		if err != nil {
			return scrollCount, err
		}

		// Handle both int and float64 (go-rod returns float64 for numbers)
		var height int
		switch v := scrollHeight.(type) {
		case int:
			height = v
		case float64:
			height = int(v)
		default:
			return scrollCount, fmt.Errorf("scrollHeight is not a number, got %T", scrollHeight)
		}

		if height == currentScrollHeight {
			staleCount++
			if staleCount >= maxStaleRetries {
				break // no more content after multiple retries
			}

			// Wait before retrying
			page.WaitForTimeout(time.Duration(staleExtraWaitMs) * time.Millisecond)

			continue // don't count stale scrolls toward maxDepth
		}

		// New content loaded
		staleCount = 0
		currentScrollHeight = height
		scrollCount++

		// Check for end-of-list marker
		endResult, endErr := page.Eval(endOfListExpr)
		if endErr == nil {
			if isEnd, ok := endResult.(bool); ok && isEnd {
				break // reached the end of Google Maps results
			}
		}

		page.WaitForTimeout(time.Duration(betweenScrollMs) * time.Millisecond)
	}

	return scrollCount, nil
}
