package skills

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	MaxAssetFiles           = 32
	MaxAssetFileBytes       = 100000
	MaxBinaryAssetFileBytes = 512 << 10
	MaxAssetTotalBytes      = 4 << 20
)

type AssetBundle struct {
	Files      []AssetFile `json:"files"`
	TotalBytes int         `json:"total_bytes"`
	Warnings   []string    `json:"warnings,omitempty"`
	SBOM       AssetSBOM   `json:"sbom,omitempty"`
}

type AssetFile struct {
	Path           string `json:"path"`
	Content        string `json:"content,omitempty"`
	ContentBase64  string `json:"content_base64,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	ScanProvider   string `json:"scan_provider,omitempty"`
	ScanVersion    string `json:"scan_version,omitempty"`
	Size           int    `json:"size"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
}

type AssetSBOM struct {
	Format              string               `json:"format"`
	PackageDigestSHA256 string               `json:"package_digest_sha256"`
	Components          []AssetSBOMComponent `json:"components"`
}

type AssetSBOMComponent struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type,omitempty"`
	Size           int    `json:"size"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
}

func EncodeAssetBundle(bundle AssetBundle) (json.RawMessage, error) {
	normalized, err := normalizeAssetBundle(bundle)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func DecodeAssetBundle(raw json.RawMessage) (AssetBundle, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "[]" {
		return AssetBundle{Files: []AssetFile{}}, nil
	}
	var bundle AssetBundle
	if err := json.Unmarshal(raw, &bundle); err == nil && bundle.Files != nil {
		return normalizeAssetBundle(bundle)
	}
	var files []AssetFile
	if err := json.Unmarshal(raw, &files); err != nil {
		return AssetBundle{}, fmt.Errorf("decode skill assets: %w", err)
	}
	return normalizeAssetBundle(AssetBundle{Files: files})
}

func FindAsset(bundle AssetBundle, assetPath string) (AssetFile, bool) {
	cleanPath, err := normalizeAssetPath(assetPath)
	if err != nil {
		return AssetFile{}, false
	}
	for _, file := range bundle.Files {
		if file.Path == cleanPath {
			return file, true
		}
	}
	return AssetFile{}, false
}

func normalizeAssetBundle(bundle AssetBundle) (AssetBundle, error) {
	if len(bundle.Files) > MaxAssetFiles {
		return AssetBundle{}, fmt.Errorf("skill assets exceed %d files", MaxAssetFiles)
	}
	normalized := AssetBundle{Files: make([]AssetFile, 0, len(bundle.Files)), Warnings: cleanAssetWarnings(bundle.Warnings), SBOM: bundle.SBOM}
	seen := map[string]bool{}
	for _, file := range bundle.Files {
		cleanPath, err := normalizeAssetPath(file.Path)
		if err != nil {
			return AssetBundle{}, err
		}
		if seen[cleanPath] {
			return AssetBundle{}, fmt.Errorf("duplicate skill asset path %q", cleanPath)
		}
		seen[cleanPath] = true
		file.Path = cleanPath
		if file.Binary {
			if err := normalizeBinaryAsset(&file); err != nil {
				return AssetBundle{}, err
			}
		} else {
			file.Size = len([]byte(file.Content))
			if file.Size == 0 || file.Size > MaxAssetFileBytes {
				return AssetBundle{}, fmt.Errorf("skill asset %q must contain 1 to %d bytes", cleanPath, MaxAssetFileBytes)
			}
			checksum := sha256.Sum256([]byte(file.Content))
			calculated := hex.EncodeToString(checksum[:])
			if file.ChecksumSHA256 != "" && !strings.EqualFold(file.ChecksumSHA256, calculated) {
				return AssetBundle{}, fmt.Errorf("skill asset %q checksum does not match content", cleanPath)
			}
			file.ChecksumSHA256 = calculated
		}
		normalized.TotalBytes += file.Size
		if normalized.TotalBytes > MaxAssetTotalBytes {
			return AssetBundle{}, fmt.Errorf("skill assets exceed %d total bytes", MaxAssetTotalBytes)
		}
		normalized.Files = append(normalized.Files, file)
	}
	return normalized, nil
}

func normalizeBinaryAsset(file *AssetFile) error {
	if file.Executable {
		return fmt.Errorf("binary skill asset %q cannot be executable", file.Path)
	}
	if strings.TrimSpace(file.Content) != "" {
		return fmt.Errorf("binary skill asset %q cannot contain inline text", file.Path)
	}
	hasInline := strings.TrimSpace(file.ContentBase64) != ""
	hasObject := strings.TrimSpace(file.ObjectRefID) != ""
	if hasInline == hasObject {
		return fmt.Errorf("binary skill asset %q must contain exactly one of content_base64 or object_ref_id", file.Path)
	}
	if strings.TrimSpace(file.ContentType) == "" {
		return fmt.Errorf("binary skill asset %q requires content_type", file.Path)
	}
	if hasInline {
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return fmt.Errorf("binary skill asset %q has invalid Base64 content", file.Path)
		}
		file.Size = len(content)
		checksum := sha256.Sum256(content)
		calculated := hex.EncodeToString(checksum[:])
		if file.ChecksumSHA256 != "" && !strings.EqualFold(file.ChecksumSHA256, calculated) {
			return fmt.Errorf("binary skill asset %q checksum does not match content", file.Path)
		}
		file.ChecksumSHA256 = calculated
	} else {
		if file.Size <= 0 || len(strings.TrimSpace(file.ChecksumSHA256)) != sha256.Size*2 {
			return fmt.Errorf("binary skill asset %q object reference requires size and SHA-256", file.Path)
		}
		if file.ScanStatus != "passed" {
			return fmt.Errorf("binary skill asset %q object reference requires passed scan status", file.Path)
		}
	}
	if file.Size == 0 || file.Size > MaxBinaryAssetFileBytes {
		return fmt.Errorf("binary skill asset %q must contain 1 to %d bytes", file.Path, MaxBinaryAssetFileBytes)
	}
	return nil
}

func normalizeAssetPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return "", errors.New("skill asset path must be relative and slash-separated")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("skill asset path cannot escape the package")
	}
	return cleaned, nil
}

func cleanAssetWarnings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
