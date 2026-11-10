package spec

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Stdin is the path that reads documents from the stdin reader passed to Load.
const Stdin = "-"

var specExtensions = []string{".yaml", ".yml", ".json"}

// Load parses every path: a file, a directory (walked recursively for .yaml, .yml
// and .json files; hidden entries are skipped) or Stdin. Files are read in lexical
// order so that results are stable.
func Load(paths []string, stdin io.Reader) ([]*Document, Diagnostics) {
	var (
		docs  []*Document
		diags Diagnostics
	)
	add := func(source string, data []byte) {
		ds, d := Parse(source, data)
		docs = append(docs, ds...)
		diags = append(diags, d...)
	}
	for _, p := range paths {
		if p == Stdin {
			data, err := io.ReadAll(io.LimitReader(stdin, MaxDocumentBytes+1))
			if err != nil {
				diags = append(diags, loadError(p, err))
				continue
			}
			add(p, data)
			continue
		}
		files, err := expand(p)
		if err != nil {
			diags = append(diags, loadError(p, err))
			continue
		}
		for _, f := range files {
			data, err := readLimited(f)
			if err != nil {
				diags = append(diags, loadError(f, err))
				continue
			}
			add(f, data)
		}
	}
	return docs, diags
}

func loadError(source string, err error) Diagnostic {
	return Diagnostic{Severity: SeverityError, Source: source, Message: err.Error()}
}

// expand returns the spec files under p.
func expand(p string) ([]string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{p}, nil
	}
	var files []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != p && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && slices.Contains(specExtensions, strings.ToLower(filepath.Ext(path))) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s files in directory", strings.Join(specExtensions, ", "))
	}
	slices.Sort(files)
	return files, nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // reading user-specified spec files is the purpose of this function.
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
}
