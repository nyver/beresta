package main

import (
	"testing"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
)

// TestImportResultDTOCountsWarningsByKind covers task 6.7's import result
// summary: the frontend renders simplified/skipped counts directly from the
// DTO rather than re-deriving them from Warnings, so this must count each
// account.ImportWarningKind correctly and leave the per-note Warnings list
// intact for the detailed report.
func TestImportResultDTOCountsWarningsByKind(t *testing.T) {
	result := account.ImportResult{
		NewNoteIDs: []model.ID{model.Nil, model.Nil},
		Warnings: []account.ImportWarning{
			{NoteTitle: "Note A", Kind: account.ImportWarningSimplifiedFormatting, Message: "formatting simplified"},
			{NoteTitle: "Note A", Kind: account.ImportWarningSkippedFile, Message: "attachment skipped"},
			{NoteTitle: "Note B", Kind: account.ImportWarningSimplifiedFormatting, Message: "formatting simplified"},
		},
	}

	dto := importResultDTO(result)

	if len(dto.NewNoteIDs) != 2 {
		t.Fatalf("NewNoteIDs = %v, want 2 entries", dto.NewNoteIDs)
	}
	if dto.SimplifiedCount != 2 {
		t.Fatalf("SimplifiedCount = %d, want 2", dto.SimplifiedCount)
	}
	if dto.SkippedCount != 1 {
		t.Fatalf("SkippedCount = %d, want 1", dto.SkippedCount)
	}
	if len(dto.Warnings) != 3 {
		t.Fatalf("Warnings = %v, want 3 entries", dto.Warnings)
	}
	if dto.Warnings[1].Kind != string(account.ImportWarningSkippedFile) {
		t.Fatalf("Warnings[1].Kind = %q, want %q", dto.Warnings[1].Kind, account.ImportWarningSkippedFile)
	}
}

// TestImportResultDTOWithNoWarningsHasZeroCounts covers a clean import: no
// simplification or skip happened, so both counts must be zero rather than
// left uninitialized in some other way that could render as "undefined" in
// the frontend summary.
func TestImportResultDTOWithNoWarningsHasZeroCounts(t *testing.T) {
	dto := importResultDTO(account.ImportResult{NewNoteIDs: []model.ID{model.Nil}})

	if dto.SimplifiedCount != 0 || dto.SkippedCount != 0 {
		t.Fatalf("counts = (%d, %d), want (0, 0)", dto.SimplifiedCount, dto.SkippedCount)
	}
	if len(dto.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want empty", dto.Warnings)
	}
}
