package ranalysisproject

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed README.md .gitignore renv.lock R/* config/* notebooks/* skills/r-survival-data-cleaning/*
var templateFS embed.FS

type File struct {
	Path    string
	Content string
}

func Files() ([]File, error) {
	paths := []string{"README.md", ".gitignore", "renv.lock"}
	for _, directory := range []string{"R", "config", "notebooks", "skills/r-survival-data-cleaning"} {
		entries, err := fs.ReadDir(templateFS, directory)
		if err != nil {
			return nil, fmt.Errorf("read R analysis template directory %s: %w", directory, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				paths = append(paths, directory+"/"+entry.Name())
			}
		}
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, path := range paths {
		content, err := templateFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read R analysis template file %s: %w", path, err)
		}
		files = append(files, File{Path: path, Content: strings.ReplaceAll(string(content), "\r\n", "\n")})
	}
	return files, nil
}
