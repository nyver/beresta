// Command localecheck validates that the English and Russian localization
// catalogs are complete, unique, and translated.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/beresta-app/beresta/internal/localization"
)

func main() {
	englishPath := flag.String("en", "locales/en.json", "path to the English localization catalog")
	russianPath := flag.String("ru", "locales/ru.json", "path to the Russian localization catalog")
	flag.Parse()

	if err := localization.ValidateCatalogs(*englishPath, *russianPath); err != nil {
		fmt.Fprintf(os.Stderr, "localization validation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Localization catalogs are valid: %s, %s\n", *englishPath, *russianPath)
}
