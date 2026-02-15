package gmaps

import (
	"context"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailExtractJob struct {
	scrapemate.Job

	Entry       *Entry
	ExitMonitor exiter.Exiter

	browserPage scrapemate.BrowserPage
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
			URL:        entry.WebSite,
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

// BrowserActions captures the browser page reference for Level 3 email
// extraction and returns an empty response (no navigation needed here;
// the email pipeline navigates on its own).
func (j *EmailExtractJob) BrowserActions(_ context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	j.browserPage = page

	return scrapemate.Response{}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	defer func() {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
	}()

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing email pipeline", "url", j.URL)

	var fetcher BrowserFetcher
	if j.browserPage != nil {
		fetcher = &pageBrowserFetcher{page: j.browserPage}
	}

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

	return j.Entry, nil, nil
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}
