package locales

import "testing"

func TestCatalogReturnsMatchingKeySets(t *testing.T) {
	en, err := Catalog(English)
	if err != nil {
		t.Fatalf("Catalog(en): %v", err)
	}
	ru, err := Catalog(Russian)
	if err != nil {
		t.Fatalf("Catalog(ru): %v", err)
	}
	if len(en) == 0 {
		t.Fatal("Catalog(en) is empty")
	}
	if len(en) != len(ru) {
		t.Fatalf("catalog key counts differ: en=%d ru=%d", len(en), len(ru))
	}
	for key := range en {
		if _, ok := ru[key]; !ok {
			t.Errorf("key %q missing from ru catalog", key)
		}
	}
}

func TestCatalogRejectsUnknownLocale(t *testing.T) {
	if _, err := Catalog("fr"); err == nil {
		t.Fatal("Catalog(fr) error = nil, want error")
	}
}

func TestSupportedListsEnglishAndRussian(t *testing.T) {
	supported := Supported()
	if len(supported) != 2 || supported[0] != English || supported[1] != Russian {
		t.Fatalf("Supported() = %v", supported)
	}
}
