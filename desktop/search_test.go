package main

import "testing"

func TestSearchByTagFindsOnlyTaggedNotes(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	tag, err := a.CreateTag("urgent")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	tagged, err := a.CreateNote("", "Tagged note")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := a.CreateNote("", "Untagged note"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := a.SetNoteTag(tagged.ID, tag.ID, true); err != nil {
		t.Fatalf("SetNoteTag: %v", err)
	}

	results, err := a.SearchByTag(tag.ID)
	if err != nil {
		t.Fatalf("SearchByTag: %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != tagged.ID {
		t.Fatalf("SearchByTag = %+v, want only %+v", results, tagged)
	}
}

func TestSearchByTagRejectsUnknownTagID(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if _, err := a.SearchByTag("not-a-valid-id"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("SearchByTag(invalid id) error = %v, want %s", err, ErrCodeInvalidInput)
	}
}

func TestSearchByTagRequiresUnlockedAccount(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.SearchByTag("00000000-0000-7000-8000-000000000000"); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("SearchByTag on locked app error = %v, want %s", err, ErrCodeLocked)
	}
}
