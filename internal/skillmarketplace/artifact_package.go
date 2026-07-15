package skillmarketplace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ArtifactProvider               = "artifact"
	MaxArtifactPackageArchiveBytes = 8 << 20
)

// ParseArtifactPackage validates and expands a user-uploaded ZIP without
// executing package content or accepting host filesystem paths.
func ParseArtifactPackage(archive []byte, artifactID string, archiveName string) (Package, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return Package{}, fmt.Errorf("artifact skill package requires artifact_id")
	}
	if len(archive) == 0 || len(archive) > MaxArtifactPackageArchiveBytes {
		return Package{}, fmt.Errorf("artifact skill package ZIP must contain 1 to %d bytes", MaxArtifactPackageArchiveBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Package{}, fmt.Errorf("open artifact skill package ZIP: %w", err)
	}
	entries, root, err := normalizedArtifactZIPEntries(reader.File)
	if err != nil {
		return Package{}, err
	}
	files := make([]PackageFile, 0, len(entries)-1)
	var skillContent string
	totalAssets := 0
	for _, entry := range entries {
		relative := strings.TrimPrefix(strings.TrimPrefix(entry.Name, root), "/")
		content, err := readArtifactZIPFile(entry.File)
		if err != nil {
			return Package{}, err
		}
		if relative == "SKILL.md" {
			if len(content) == 0 || len(content) > maxRemoteSkillBytes || !utf8.Valid(content) {
				return Package{}, fmt.Errorf("artifact SKILL.md must contain 1 to %d UTF-8 bytes", maxRemoteSkillBytes)
			}
			skillContent = string(content)
			continue
		}
		if len(strings.Split(relative, "/")) > maxRemoteAssetDepth {
			return Package{}, fmt.Errorf("artifact package asset %q exceeds %d path levels", relative, maxRemoteAssetDepth)
		}
		extension := strings.ToLower(path.Ext(relative))
		binary := isAllowedBinaryAssetExtension(extension)
		if !binary && !isAllowedTextAssetExtension(extension) {
			return Package{}, fmt.Errorf("artifact package contains unsupported file %q", relative)
		}
		limit := maxRemoteSkillBytes
		if binary {
			limit = maxRemoteBinaryFileBytes
		}
		if len(content) == 0 || len(content) > limit {
			return Package{}, fmt.Errorf("artifact package file %q must contain 1 to %d bytes", relative, limit)
		}
		if !binary && !utf8.Valid(content) {
			return Package{}, fmt.Errorf("artifact text package file %q must be UTF-8", relative)
		}
		totalAssets += len(content)
		if totalAssets > maxRemoteAssetBytes {
			return Package{}, fmt.Errorf("artifact package assets exceed %d bytes", maxRemoteAssetBytes)
		}
		digest := sha256.Sum256(content)
		contentType := mime.TypeByExtension(extension)
		if contentType == "" {
			contentType = http.DetectContentType(content)
		}
		file := PackageFile{
			Path: relative, ContentType: contentType, ChecksumSHA256: hex.EncodeToString(digest[:]),
			Size: len(content), Revision: hex.EncodeToString(digest[:]), Executable: isScriptExtension(extension), Binary: binary,
		}
		if binary {
			file.ContentBase64 = base64.StdEncoding.EncodeToString(content)
		} else {
			file.Content = string(content)
		}
		files = append(files, file)
	}
	name, description, license := parseFrontMatter([]byte(skillContent))
	manifest, err := parsePackageManifest([]byte(skillContent))
	if err != nil {
		return Package{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimSpace(archiveName)), path.Ext(strings.TrimSpace(archiveName)))
	}
	revision := sha256.Sum256(archive)
	return Package{
		Source: Source{Provider: ArtifactProvider, ArtifactID: artifactID, Path: "SKILL.md"},
		Name:   name, Description: description, License: license, Content: skillContent, Manifest: manifest,
		Revision: hex.EncodeToString(revision[:]), Files: files, TotalAssetBytes: totalAssets,
	}, nil
}

type artifactZIPEntry struct {
	Name string
	File *zip.File
}

func normalizedArtifactZIPEntries(files []*zip.File) ([]artifactZIPEntry, string, error) {
	entries := make([]artifactZIPEntry, 0, len(files))
	skillPaths := make([]string, 0, 1)
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		name, err := normalizeArtifactZIPPath(file.Name)
		if err != nil {
			return nil, "", err
		}
		if name == "" || file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() {
			return nil, "", fmt.Errorf("artifact package file %q must be a regular file", name)
		}
		if seen[name] {
			return nil, "", fmt.Errorf("artifact package contains duplicate file %q", name)
		}
		seen[name] = true
		if path.Base(name) == "SKILL.md" {
			skillPaths = append(skillPaths, name)
		}
		entries = append(entries, artifactZIPEntry{Name: name, File: file})
	}
	if len(skillPaths) != 1 {
		return nil, "", fmt.Errorf("artifact package must contain exactly one SKILL.md")
	}
	root := strings.TrimSuffix(skillPaths[0], "/SKILL.md")
	if root == "SKILL.md" {
		root = ""
	}
	if strings.Contains(root, "/") {
		return nil, "", fmt.Errorf("artifact package SKILL.md may use at most one wrapper directory")
	}
	for _, entry := range entries {
		if root != "" && !strings.HasPrefix(entry.Name, root+"/") {
			return nil, "", fmt.Errorf("artifact package file %q is outside the SKILL.md root", entry.Name)
		}
	}
	if len(entries)-1 > maxRemoteAssetFiles {
		return nil, "", fmt.Errorf("artifact skill package exceeds %d asset files", maxRemoteAssetFiles)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, root, nil
}

func normalizeArtifactZIPPath(value string) (string, error) {
	if strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("artifact package contains unsafe path %q", value)
	}
	cleaned := path.Clean(strings.TrimSpace(value))
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("artifact package contains unsafe path %q", value)
	}
	return cleaned, nil
}

func readArtifactZIPFile(entry *zip.File) ([]byte, error) {
	limit := int64(maxRemoteBinaryFileBytes + 1)
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("artifact package file %q exceeds per-file size limit", entry.Name)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("open artifact package file %q: %w", entry.Name, err)
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, fmt.Errorf("read artifact package file %q: %w", entry.Name, err)
	}
	if int64(len(content)) >= limit {
		return nil, fmt.Errorf("artifact package file %q exceeds per-file size limit", entry.Name)
	}
	return content, nil
}
