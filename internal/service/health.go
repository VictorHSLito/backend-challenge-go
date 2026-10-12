package service

import (
	"context"
	"sort"
	"sync"
	"time"
)

type HealthChecker interface {
	Name() string
	Check(ctx context.Context) error
}

type HealthStatus string

const (
	HealthUp   HealthStatus = "UP"
	HealthDown HealthStatus = "DOWN"
)

type HealthReport struct {
	Status HealthStatus            `json:"status"`
	Checks map[string]HealthResult `json:"checks"`
}

type HealthResult struct {
	Status HealthStatus `json:"status"`
	Error  string       `json:"error,omitempty"`
}

type HealthService struct {
	checkers []HealthChecker
	timeout  time.Duration
}

func NewHealthService(checkers []HealthChecker) *HealthService {
	sort.Slice(checkers, func(i, j int) bool { return checkers[i].Name() < checkers[j].Name() })
	return &HealthService{checkers: checkers, timeout: 2 * time.Second}
}

func (s *HealthService) Ready(ctx context.Context) HealthReport {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	report := HealthReport{Status: HealthUp, Checks: make(map[string]HealthResult, len(s.checkers))}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, c := range s.checkers {
		wg.Go(func() {
			res := HealthResult{Status: HealthUp}
			if err := c.Check(ctx); err != nil {
				res = HealthResult{Status: HealthDown, Error: err.Error()}
			}
			mu.Lock()
			defer mu.Unlock()
			report.Checks[c.Name()] = res
			if res.Status == HealthDown {
				report.Status = HealthDown
			}
		})
	}
	wg.Wait()
	return report
}
