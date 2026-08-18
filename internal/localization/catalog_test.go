package localization

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCatalogs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		english   string
		russian   string
		wantError string
	}{
		{
			name:    "valid catalogs",
			english: `{"app.tagline":"Encrypted notes, available offline."}`,
			russian: `{"app.tagline":"Зашифрованные заметки, доступные офлайн."}`,
		},
		{
			name:      "missing key",
			english:   `{"app.tagline":"Encrypted notes, available offline.","action.save":"Save"}`,
			russian:   `{"app.tagline":"Зашифрованные заметки, доступные офлайн."}`,
			wantError: `Russian catalog is missing localization key "action.save"`,
		},
		{
			name:      "duplicate key",
			english:   `{"action.save":"Save","action.save":"Store"}`,
			russian:   `{"action.save":"Сохранить"}`,
			wantError: `contains duplicate key "action.save"`,
		},
		{
			name:      "untranslated key",
			english:   `{"action.save":"Save"}`,
			russian:   `{"action.save":"Save"}`,
			wantError: `localization key "action.save" is untranslated`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			englishPath := writeCatalog(t, directory, "en.json", test.english)
			russianPath := writeCatalog(t, directory, "ru.json", test.russian)
			err := ValidateCatalogs(englishPath, russianPath)

			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateCatalogs() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateCatalogs() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func writeCatalog(t *testing.T, directory, name, contents string) string {
	t.Helper()

	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}
