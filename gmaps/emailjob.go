package gmaps

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/exiter"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailExtractJob struct {
	scrapemate.Job

	Entry                   *Entry
	ExitMonitor             exiter.Exiter
	WriterManagedCompletion bool

	pipelineRan bool
}

func NewEmailJob(parentID string, entry *Entry, opts ...EmailExtractJobOptions) *EmailExtractJob {
	const (
		defaultPrio       = scrapemate.PriorityHigh
		defaultMaxRetries = 0
	)

	job := EmailExtractJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        normalizeGoogleURL(entry.WebSite),
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.Entry = entry

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithEmailJobExitMonitor(exitMonitor exiter.Exiter) EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.ExitMonitor = exitMonitor
	}
}

func WithEmailJobWriterManagedCompletion() EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.WriterManagedCompletion = true
	}
}

// BrowserActions runs the email pipeline while the browser page is owned
// exclusively. scrapemate recycles the page back into its pool the moment this
// returns, so Level 3 navigation MUST happen here, not in Process. Running it
// in Process drives a page that another worker may already be using, which
// intermittently deadlocks the Playwright driver and stalls every worker.
func (j *EmailExtractJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	var fetcher BrowserFetcher
	if page != nil {
		fetcher = &pageBrowserFetcher{page: page}
	}

	j.runPipeline(ctx, fetcher)

	return scrapemate.Response{StatusCode: 200}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	defer func() {
		if j.ExitMonitor != nil && !j.WriterManagedCompletion {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
	}()

	// In non-JS mode BrowserActions is never invoked, so run the HTTP-only
	// pipeline here. In JS mode it already ran during BrowserActions and this
	// is a no-op.
	j.runPipeline(ctx, nil)

	return j.Entry, nil, nil
}

// runPipeline executes the email pipeline exactly once. fetcher is non-nil only
// when a browser page is available (JS mode), enabling Level 3 rendering.
func (j *EmailExtractJob) runPipeline(ctx context.Context, fetcher BrowserFetcher) {
	if j.pipelineRan {
		return
	}

	j.pipelineRan = true

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing email pipeline", "url", j.URL)

	pipeline := NewEmailPipeline(j.Entry, fetcher)

	if err := pipeline.Run(ctx); err != nil {
		log.Warn("Email pipeline failed", "url", j.URL, "error", err)
		j.Entry.Emails = []string{}
		j.Entry.EmailStatus = "website_error"
	}

	log.Info("Email pipeline completed",
		"url", j.URL,
		"emails_found", len(j.Entry.Emails),
		"status", j.Entry.EmailStatus,
		"source", j.Entry.EmailSource,
	)
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}

// normalizeGoogleURL extracts the actual target URL from Google redirect URLs.
// Google Maps sometimes returns URLs like "/url?q=http://example.com/&opi=..."
// for external website links.
func normalizeGoogleURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	if strings.HasPrefix(rawURL, "/url?q=") {
		fullURL := "https://www.google.com" + rawURL

		parsed, err := url.Parse(fullURL)
		if err != nil {
			return rawURL
		}

		if target := parsed.Query().Get("q"); target != "" {
			return target
		}
	}

	if strings.HasPrefix(rawURL, "/") {
		return "https://www.google.com" + rawURL
	}

	return rawURL
}
