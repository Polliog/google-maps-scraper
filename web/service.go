package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosom/google-maps-scraper/gmaps"
)

type Service struct {
	repo       JobRepository
	dataFolder string
}

func NewService(repo JobRepository, dataFolder string) *Service {
	return &Service{
		repo:       repo,
		dataFolder: dataFolder,
	}
}

func (s *Service) Create(ctx context.Context, job *Job) error {
	return s.repo.Create(ctx, job)
}

func (s *Service) All(ctx context.Context) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{})
}

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid file name")
	}

	// Elimina sia il file CSV che JSON
	csvPath := filepath.Join(s.dataFolder, id+".csv")
	jsonPath := filepath.Join(s.dataFolder, id+".json")

	// Rimuovi il file CSV se esiste
	if _, err := os.Stat(csvPath); err == nil {
		if err := os.Remove(csvPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Rimuovi il file JSON se esiste
	if _, err := os.Stat(jsonPath); err == nil {
		if err := os.Remove(jsonPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return s.repo.Delete(ctx, id)
}

func (s *Service) Update(ctx context.Context, job *Job) error {
	return s.repo.Update(ctx, job)
}

func (s *Service) SelectPending(ctx context.Context) ([]Job, error) {
	return s.repo.Select(ctx, SelectParams{Status: StatusPending, Limit: 1})
}

// GetCSV restituisce il percorso del file CSV per un job
func (s *Service) GetCSV(_ context.Context, id string) (string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid file name")
	}

	datapath := filepath.Join(s.dataFolder, id+".csv")

	if _, err := os.Stat(datapath); os.IsNotExist(err) {
		return "", fmt.Errorf("csv file not found for job %s", id)
	}

	return datapath, nil
}

func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	repo, ok := s.repo.(SettingsRepository)
	if !ok {
		settings := Settings{}
		settings.ApplyDefaults()

		return settings, nil
	}

	settings, err := repo.GetSettings(ctx)
	if err != nil {
		settings.ApplyDefaults()

		return settings, nil
	}

	settings.ApplyDefaults()

	return settings, nil
}

func (s *Service) SaveSettings(ctx context.Context, settings *Settings) error {
	repo, ok := s.repo.(SettingsRepository)
	if !ok {
		return fmt.Errorf("settings not supported by repository")
	}

	if err := settings.Validate(); err != nil {
		return err
	}

	settings.ApplyDefaults()

	return repo.UpsertSettings(ctx, settings)
}

// GetJSON restituisce il percorso del file JSON per un job
func (s *Service) GetJSON(_ context.Context, id string) (string, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid file name")
	}

	datapath := filepath.Join(s.dataFolder, id+".json")

	if _, err := os.Stat(datapath); os.IsNotExist(err) {
		return "", fmt.Errorf("json file not found for job %s", id)
	}

	return datapath, nil
}

func (s *Service) loadEntries(id string) ([]gmaps.Entry, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid file name")
	}

	datapath := filepath.Join(s.dataFolder, id+".json")

	data, err := os.ReadFile(datapath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("json file not found for job %s", id)
		}

		return nil, err
	}

	var entries []gmaps.Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse json file: %w", err)
	}

	return entries, nil
}

func (s *Service) saveEntries(id string, entries []gmaps.Entry) error {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid file name")
	}

	datapath := filepath.Join(s.dataFolder, id+".json")

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	return os.WriteFile(datapath, data, 0o644)
}

type IndexedEntry struct {
	Entry gmaps.Entry
	Index int // 0-based index in the original array
}

func (s *Service) GetRecords(_ context.Context, jobID string, page, pageSize int, search string) ([]IndexedEntry, int, error) {
	entries, err := s.loadEntries(jobID)
	if err != nil {
		return nil, 0, err
	}

	indexed := make([]IndexedEntry, 0, len(entries))

	if search != "" {
		search = strings.ToLower(search)

		for i, e := range entries {
			if strings.Contains(strings.ToLower(e.Title), search) ||
				strings.Contains(strings.ToLower(e.Address), search) ||
				strings.Contains(strings.ToLower(e.Phone), search) ||
				strings.Contains(strings.ToLower(strings.Join(e.Emails, " ")), search) {
				indexed = append(indexed, IndexedEntry{Entry: e, Index: i})
			}
		}
	} else {
		for i, e := range entries {
			indexed = append(indexed, IndexedEntry{Entry: e, Index: i})
		}
	}

	total := len(indexed)

	start := (page - 1) * pageSize
	if start >= total {
		return []IndexedEntry{}, total, nil
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	return indexed[start:end], total, nil
}

func (s *Service) UpdateRecord(_ context.Context, jobID string, recordID int, updates map[string]interface{}) (gmaps.Entry, error) {
	entries, err := s.loadEntries(jobID)
	if err != nil {
		return gmaps.Entry{}, err
	}

	idx := recordID - 1
	if idx < 0 || idx >= len(entries) {
		return gmaps.Entry{}, ErrNotFound
	}

	entry := &entries[idx]

	for key, val := range updates {
		switch key {
		case "title":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'title' must be a string")
			}

			entry.Title = v
		case "address":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'address' must be a string")
			}

			entry.Address = v
		case "phone":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'phone' must be a string")
			}

			entry.Phone = v
		case "website":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'website' must be a string")
			}

			entry.WebSite = v
		case "email":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'email' must be a string")
			}

			parts := strings.Split(v, ",")
			emails := make([]string, 0, len(parts))

			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					emails = append(emails, p)
				}
			}

			entry.Emails = emails
		case "category":
			v, ok := val.(string)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'category' must be a string")
			}

			entry.Category = v
		case "rating":
			v, ok := val.(float64)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'rating' must be a number")
			}

			entry.ReviewRating = v
		case "reviews_count":
			v, ok := val.(float64)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'reviews_count' must be a number")
			}

			entry.ReviewCount = int(v)
		case "latitude":
			v, ok := val.(float64)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'latitude' must be a number")
			}

			entry.Latitude = v
		case "longitude":
			v, ok := val.(float64)
			if !ok {
				return gmaps.Entry{}, fmt.Errorf("field 'longitude' must be a number")
			}

			entry.Longtitude = v
		}
	}

	if err := s.saveEntries(jobID, entries); err != nil {
		return gmaps.Entry{}, err
	}

	return entries[idx], nil
}

func (s *Service) DeleteRecord(_ context.Context, jobID string, recordID int) error {
	entries, err := s.loadEntries(jobID)
	if err != nil {
		return err
	}

	idx := recordID - 1
	if idx < 0 || idx >= len(entries) {
		return ErrNotFound
	}

	entries = append(entries[:idx], entries[idx+1:]...)

	return s.saveEntries(jobID, entries)
}
