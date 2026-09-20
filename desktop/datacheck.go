package main

import (
	"time"

	"github.com/beresta-app/beresta/core/datacheck"
	"github.com/beresta-app/beresta/core/store"
)

// RunDataCheck runs the Advanced "Check my data" action's single,
// consolidated, safe verification pass over local database integrity,
// search index consistency, and backup health, per specs/product-
// experience's "Routine maintenance and user data check" requirement
// (task 7.10). It never itemizes internal maintenance jobs: the UI sees
// only a healthy result or one actionable summary.
func (a *App) RunDataCheck() (DataCheckReportDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return DataCheckReportDTO{}, mapError(err)
	}
	ctx := a.requestContext()
	now := time.Now()

	result, err := acc.RunDataCheck(ctx, now)
	if err != nil {
		return DataCheckReportDTO{}, mapError(err)
	}
	daily, err := acc.ListBackups(ctx, store.BackupKindDaily)
	if err != nil {
		return DataCheckReportDTO{}, mapError(err)
	}
	manual, err := acc.ListBackups(ctx, store.BackupKindManual)
	if err != nil {
		return DataCheckReportDTO{}, mapError(err)
	}

	report := datacheck.Summarize(result.IntegrityOK, result.SearchIndexRepaired, daily, manual, now)
	return newDataCheckReportDTO(report), nil
}
