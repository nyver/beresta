package mobileapi

import (
	"time"

	"github.com/beresta-app/beresta/core/datacheck"
	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
)

// dataCheckReportDTO is the gomobile-safe JSON projection of
// presentation.DataCheckReport, matching the desktop bridge's
// DataCheckReportDTO (task 7.10).
type dataCheckReportDTO struct {
	Issue           presentation.DataCheckIssue `json:"issue"`
	Healthy         bool                        `json:"healthy"`
	CheckedAtUnixMS int64                       `json:"checked_at_unix_ms"`
}

func newDataCheckReportDTO(report presentation.DataCheckReport) dataCheckReportDTO {
	return dataCheckReportDTO{
		Issue:           report.Issue,
		Healthy:         report.Issue.Healthy(),
		CheckedAtUnixMS: unixMS(report.CheckedAt),
	}
}

// RunDataCheck runs the Advanced "Check my data" action's single,
// consolidated, safe verification pass over local database integrity,
// search index consistency, and backup health, per specs/product-
// experience's "Routine maintenance and user data check" requirement, and
// returns it as a strict JSON string matching desktop's DataCheckReportDTO.
func (s *Service) RunDataCheck() (string, error) {
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	now := time.Now()

	result, err := value.RunDataCheck(s.root, now)
	if err != nil {
		return "", err
	}
	daily, err := value.ListBackups(s.root, store.BackupKindDaily)
	if err != nil {
		return "", err
	}
	manual, err := value.ListBackups(s.root, store.BackupKindManual)
	if err != nil {
		return "", err
	}

	report := datacheck.Summarize(result.IntegrityOK, result.SearchIndexRepaired, daily, manual, now)
	return marshal(newDataCheckReportDTO(report))
}
