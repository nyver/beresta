// Command normalizezip rewrites ZIP/JAR/AAR metadata deterministically.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var stableTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: normalizezip <archive>")
		os.Exit(2)
	}
	path := flag.Arg(0)
	contents, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	normalized, err := normalize(contents)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, normalized, 0o644); err != nil {
		fatal(err)
	}
}

func normalize(contents []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, err
	}
	files := append([]*zip.File(nil), reader.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range files {
		body, err := readEntry(file)
		if err != nil {
			writer.Close()
			return nil, err
		}
		if strings.EqualFold(filepath.Ext(file.Name), ".jar") {
			if nested, err := normalize(body); err == nil {
				body = nested
			}
		}
		header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate}
		header.SetMode(file.Mode())
		header.SetModTime(stableTime)
		if file.FileInfo().IsDir() {
			header.Method = zip.Store
		}
		destination, err := writer.CreateHeader(header)
		if err != nil {
			writer.Close()
			return nil, err
		}
		if _, err := destination.Write(body); err != nil {
			writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func readEntry(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
