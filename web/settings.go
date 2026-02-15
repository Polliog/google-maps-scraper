package web

import (
	"context"
	"errors"
	"time"
)

type Settings struct {
	Language string   `json:"language"`
	Depth    int      `json:"depth"`
	Email    bool     `json:"email"`
	MaxTime  string   `json:"max_time"`
	Proxies  []string `json:"proxies"`
}

func (s *Settings) Validate() error {
	if s.Language != "" && len(s.Language) != 2 {
		return errors.New("language must be a 2-letter ISO code")
	}

	if s.Depth < 0 {
		return errors.New("depth cannot be negative")
	}

	if s.MaxTime != "" {
		if _, err := time.ParseDuration(s.MaxTime); err != nil {
			return errors.New("invalid max time format (use Go duration like 10m, 1h30m)")
		}
	}

	return nil
}

func (s *Settings) ApplyDefaults() {
	if s.Language == "" {
		s.Language = "en"
	}

	if s.Depth == 0 {
		s.Depth = 10
	}

	if s.MaxTime == "" {
		s.MaxTime = "10m"
	}

	if s.Proxies == nil {
		s.Proxies = []string{}
	}
}

type SettingsRepository interface {
	GetSettings(context.Context) (Settings, error)
	UpsertSettings(context.Context, *Settings) error
}
