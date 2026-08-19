// Package locales embeds the English and Russian UI string catalogs so
// platform clients can serve them without a runtime file-system dependency.
// The catalogs' key/value content is validated at build time by
// cmd/localecheck (see AGENTS.md).
package locales

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed en.json ru.json
var catalogFiles embed.FS

// English is the default "en" locale identifier.
const English = "en"

// Russian is the "ru" locale identifier.
const Russian = "ru"

// Catalog returns the parsed key/value string catalog for locale ("en" or
// "ru"). Every other value reports an error rather than silently falling
// back, so a caller-supplied locale from settings or a URL is never
// mistaken for a supported one.
func Catalog(locale string) (map[string]string, error) {
	var file string
	switch locale {
	case English:
		file = "en.json"
	case Russian:
		file = "ru.json"
	default:
		return nil, fmt.Errorf("locales: unsupported locale %q", locale)
	}

	data, err := catalogFiles.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("locales: read %s: %w", file, err)
	}
	var catalog map[string]string
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("locales: decode %s: %w", file, err)
	}
	return catalog, nil
}

// Supported lists every locale identifier Catalog accepts, in display
// order.
func Supported() []string {
	return []string{English, Russian}
}
