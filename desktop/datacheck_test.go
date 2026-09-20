package main

import (
	"testing"

	"github.com/beresta-app/beresta/core/presentation"
)

// TestRunDataCheckReportsHealthyOnAFreshAccount covers task 7.10's Advanced
// "Check my data" action: a freshly created account has a healthy database
// and no backup yet, so RunDataCheck must report that a backup is needed -
// specs/product-experience's "Routine maintenance and user data check"
// requirement lists backup rotation among the maintenance domains this one
// action stands in for.
func TestRunDataCheckReportsHealthyOnAFreshAccount(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	report, err := a.RunDataCheck()
	if err != nil {
		t.Fatalf("RunDataCheck: %v", err)
	}
	if report.Issue != presentation.DataCheckIssueBackupNeedsAttention {
		t.Fatalf("Issue = %q, want %q before any backup exists", report.Issue, presentation.DataCheckIssueBackupNeedsAttention)
	}
	if report.Healthy {
		t.Fatal("Healthy = true, want false before any backup exists")
	}
	if report.CheckedAtUnixMS <= 0 {
		t.Fatal("CheckedAtUnixMS = 0, want > 0")
	}
}

// TestRunDataCheckReportsHealthyAfterAVerifiedBackup covers the fully
// healthy case: once a backup has completed and been verified, RunDataCheck
// must report no problems.
func TestRunDataCheckReportsHealthyAfterAVerifiedBackup(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := a.CreateManualBackup(t.TempDir()); err != nil {
		t.Fatalf("CreateManualBackup: %v", err)
	}

	report, err := a.RunDataCheck()
	if err != nil {
		t.Fatalf("RunDataCheck: %v", err)
	}
	if report.Issue != presentation.DataCheckIssueNone {
		t.Fatalf("Issue = %q, want %q", report.Issue, presentation.DataCheckIssueNone)
	}
	if !report.Healthy {
		t.Fatal("Healthy = false, want true")
	}
}
