// Package localization validates the source localization catalogs used by all
// Beresta clients.
package localization

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// ValidateCatalogs checks that both catalogs contain the same unique keys and
// that every value is non-empty and translated.
func ValidateCatalogs(englishPath, russianPath string) error {
	english, err := readCatalog(englishPath)
	if err != nil {
		return err
	}
	russian, err := readCatalog(russianPath)
	if err != nil {
		return err
	}

	for _, key := range sortedKeys(english) {
		englishValue := strings.TrimSpace(english[key])
		if englishValue == "" {
			return fmt.Errorf("localization key %q has an empty English value", key)
		}

		russianValue, ok := russian[key]
		if !ok {
			return fmt.Errorf("Russian catalog is missing localization key %q", key)
		}
		russianValue = strings.TrimSpace(russianValue)
		if russianValue == "" {
			return fmt.Errorf("localization key %q has an empty Russian value", key)
		}
		if englishValue == russianValue {
			return fmt.Errorf("localization key %q is untranslated", key)
		}
	}

	for _, key := range sortedKeys(russian) {
		if _, ok := english[key]; !ok {
			return fmt.Errorf("English catalog is missing localization key %q", key)
		}
	}

	return nil
}

func readCatalog(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open localization catalog %q: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	openingToken, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode localization catalog %q: %w", path, err)
	}
	if openingDelimiter, ok := openingToken.(json.Delim); !ok || openingDelimiter != '{' {
		return nil, fmt.Errorf("localization catalog %q must be a JSON object", path)
	}

	catalog := make(map[string]string)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return nil, fmt.Errorf("decode localization key in %q: %w", path, tokenErr)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("localization catalog %q contains a non-string key", path)
		}
		if _, duplicate := catalog[key]; duplicate {
			return nil, fmt.Errorf("localization catalog %q contains duplicate key %q", path, key)
		}

		var value string
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, fmt.Errorf("localization key %q in %q must have a string value: %w", key, path, decodeErr)
		}
		catalog[key] = value
	}

	closingToken, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode localization catalog %q: %w", path, err)
	}
	if closingDelimiter, ok := closingToken.(json.Delim); !ok || closingDelimiter != '}' {
		return nil, fmt.Errorf("localization catalog %q must end with a JSON object delimiter", path)
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("localization catalog %q contains data after the JSON object", path)
		}
		return nil, fmt.Errorf("decode localization catalog %q: %w", path, err)
	}

	return catalog, nil
}

func sortedKeys(catalog map[string]string) []string {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
