package skillmarketplace

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
)

const (
	BinaryScanVersion = "tma.skill.binary-scan.v1"
	BinaryScanPassed  = "passed"
	BinaryScanBlocked = "blocked"
	SBOMFormat        = "tma.skill.sbom.v1"
)

type BinaryScanResult struct {
	Path           string                    `json:"path"`
	Status         string                    `json:"status"`
	Scanner        string                    `json:"scanner"`
	ExternalScan   *ExternalBinaryScanResult `json:"external_scan,omitempty"`
	ContentType    string                    `json:"content_type"`
	Size           int                       `json:"size"`
	ChecksumSHA256 string                    `json:"checksum_sha256"`
	Findings       []SecurityFinding         `json:"findings"`
}

type PackageSBOM struct {
	Format              string          `json:"format"`
	PackageDigestSHA256 string          `json:"package_digest_sha256"`
	Components          []SBOMComponent `json:"components"`
}

type SBOMComponent struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type,omitempty"`
	Size           int    `json:"size"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
}

func InspectBinaryAssets(pkg Package) []BinaryScanResult {
	results := make([]BinaryScanResult, 0)
	for _, file := range pkg.Files {
		if !file.Binary && strings.TrimSpace(file.ContentBase64) == "" {
			continue
		}
		results = append(results, inspectBinaryAsset(file))
	}
	return results
}

func inspectBinaryAsset(file PackageFile) BinaryScanResult {
	result := BinaryScanResult{
		Path: file.Path, Status: BinaryScanPassed, Scanner: BinaryScanVersion,
		ContentType: file.ContentType, Size: file.Size, ChecksumSHA256: file.ChecksumSHA256,
		Findings: []SecurityFinding{},
	}
	content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
	if err != nil {
		return blockBinary(result, "binary_encoding", "Binary asset is not valid Base64.")
	}
	result.Size = len(content)
	checksum := sha256.Sum256(content)
	result.ChecksumSHA256 = hex.EncodeToString(checksum[:])
	if file.Size != 0 && file.Size != len(content) {
		result = blockBinary(result, "binary_size_mismatch", "Binary asset size does not match package metadata.")
	}
	if file.ChecksumSHA256 != "" && !strings.EqualFold(file.ChecksumSHA256, result.ChecksumSHA256) {
		result = blockBinary(result, "binary_digest_mismatch", "Binary asset digest does not match package metadata.")
	}
	detectedType := http.DetectContentType(content)
	result.ContentType = detectedType
	if !binaryContentTypeMatches(path.Ext(file.Path), detectedType, content) {
		result = blockBinary(result, "binary_type_mismatch", "Binary asset content does not match its allowed file extension.")
	}
	if containsExecutableMagic(content) {
		result = blockBinary(result, "binary_executable", "Binary asset contains an executable file signature.")
	}
	if bytes.Contains(bytes.ToUpper(content), []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE")) {
		result = blockBinary(result, "binary_eicar", "Binary asset matches the EICAR antivirus test signature.")
	}
	lowerContent := asciiLower(content)
	if strings.EqualFold(path.Ext(file.Path), ".pdf") && pdfContainsActiveContent(content) {
		result = blockBinary(result, "pdf_active_content", "PDF asset contains active or embedded content markers.")
	}
	if isOfficeOpenXML(path.Ext(file.Path)) && containsAny(lowerContent, []byte("vbaproject.bin"), []byte("macrosheets"), []byte("_xmlsignatures")) {
		result = blockBinary(result, "office_active_content", "Office asset contains macro or active-content markers.")
	}
	return result
}

func blockBinary(result BinaryScanResult, ruleID string, message string) BinaryScanResult {
	result.Status = BinaryScanBlocked
	result.Findings = append(result.Findings, SecurityFinding{
		RuleID: ruleID, Severity: SeverityCritical, Path: result.Path, Message: message,
	})
	return result
}

func binaryContentTypeMatches(extension string, detectedType string, content []byte) bool {
	extension = strings.ToLower(extension)
	detectedType = strings.ToLower(strings.TrimSpace(strings.Split(detectedType, ";")[0]))
	switch extension {
	case ".png":
		return detectedType == "image/png"
	case ".jpg", ".jpeg":
		return detectedType == "image/jpeg"
	case ".gif":
		return detectedType == "image/gif"
	case ".webp":
		return detectedType == "image/webp"
	case ".pdf":
		return detectedType == "application/pdf"
	case ".docx", ".xlsx", ".pptx":
		return (detectedType == "application/zip" || detectedType == "application/octet-stream") && bytes.HasPrefix(content, []byte("PK"))
	default:
		return false
	}
}

func containsExecutableMagic(content []byte) bool {
	if len(content) < 4 {
		return false
	}
	if bytes.HasPrefix(content, []byte("MZ")) || bytes.HasPrefix(content, []byte{0x7f, 'E', 'L', 'F'}) {
		return true
	}
	magic := content[:4]
	return bytes.Equal(magic, []byte{0xfe, 0xed, 0xfa, 0xce}) ||
		bytes.Equal(magic, []byte{0xfe, 0xed, 0xfa, 0xcf}) ||
		bytes.Equal(magic, []byte{0xcf, 0xfa, 0xed, 0xfe}) ||
		bytes.Equal(magic, []byte{0xca, 0xfe, 0xba, 0xbe})
}

func containsAny(content []byte, markers ...[]byte) bool {
	for _, marker := range markers {
		if bytes.Contains(content, marker) {
			return true
		}
	}
	return false
}

func pdfContainsActiveContent(content []byte) bool {
	content = asciiLower(pdfContentOutsideStreams(content))
	for _, name := range []string{"/javascript", "/js", "/launch", "/embeddedfile"} {
		if containsPDFName(content, []byte(name)) {
			return true
		}
	}
	return false
}

func pdfContentOutsideStreams(content []byte) []byte {
	lower := asciiLower(content)
	var result bytes.Buffer
	cursor := 0
	for cursor < len(content) {
		relativeStart := bytes.Index(lower[cursor:], []byte("stream"))
		if relativeStart < 0 {
			result.Write(content[cursor:])
			break
		}
		start := cursor + relativeStart
		afterKeyword := start + len("stream")
		if (start > 0 && !isPDFDelimiter(lower[start-1])) || afterKeyword >= len(lower) || (lower[afterKeyword] != '\r' && lower[afterKeyword] != '\n') {
			result.Write(content[cursor:afterKeyword])
			cursor = afterKeyword
			continue
		}
		result.Write(content[cursor:afterKeyword])
		dataStart := afterKeyword
		if lower[dataStart] == '\r' {
			dataStart++
		}
		if dataStart < len(lower) && lower[dataStart] == '\n' {
			dataStart++
		}
		endOffset := bytes.Index(lower[dataStart:], []byte("endstream"))
		if endOffset < 0 {
			result.Write(content[afterKeyword:])
			break
		}
		end := dataStart + endOffset
		endKeywordEnd := end + len("endstream")
		if end < dataStart || endKeywordEnd < end || endKeywordEnd > len(content) {
			result.Write(content[afterKeyword:])
			break
		}
		result.Write(content[end:endKeywordEnd])
		cursor = endKeywordEnd
	}
	return result.Bytes()
}

func asciiLower(content []byte) []byte {
	result := append([]byte(nil), content...)
	for index, value := range result {
		if value >= 'A' && value <= 'Z' {
			result[index] = value + ('a' - 'A')
		}
	}
	return result
}

func containsPDFName(content []byte, name []byte) bool {
	cursor := 0
	for cursor < len(content) {
		index := bytes.Index(content[cursor:], name)
		if index < 0 {
			return false
		}
		after := cursor + index + len(name)
		if after == len(content) || isPDFDelimiter(content[after]) {
			return true
		}
		cursor = after
	}
	return false
}

func isPDFDelimiter(value byte) bool {
	switch value {
	case 0, '\t', '\n', '\f', '\r', ' ', '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}

func isOfficeOpenXML(extension string) bool {
	switch strings.ToLower(extension) {
	case ".docx", ".xlsx", ".pptx":
		return true
	default:
		return false
	}
}

func BuildPackageSBOM(pkg Package, packageDigest string) PackageSBOM {
	components := make([]SBOMComponent, 0, len(pkg.Files)+1)
	rootChecksum := sha256.Sum256([]byte(pkg.Content))
	components = append(components, SBOMComponent{
		Path: "SKILL.md", Kind: "text", ContentType: "text/markdown", Size: len([]byte(pkg.Content)),
		ChecksumSHA256: hex.EncodeToString(rootChecksum[:]), Revision: pkg.Revision, SourceURL: pkg.HTMLURL,
	})
	for _, file := range pkg.Files {
		content, err := packageFileBytes(file)
		if err != nil {
			content = []byte(file.ContentBase64)
		}
		checksum := sha256.Sum256(content)
		kind := "text"
		if file.Binary {
			kind = "binary"
		}
		components = append(components, SBOMComponent{
			Path: file.Path, Kind: kind, ContentType: file.ContentType, Size: len(content),
			ChecksumSHA256: hex.EncodeToString(checksum[:]), Revision: file.Revision, SourceURL: file.SourceURL,
		})
	}
	return PackageSBOM{Format: SBOMFormat, PackageDigestSHA256: packageDigest, Components: components}
}

func packageFileBytes(file PackageFile) ([]byte, error) {
	if file.Binary || strings.TrimSpace(file.ContentBase64) != "" {
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("decode binary package file %q: %w", file.Path, err)
		}
		return content, nil
	}
	return []byte(file.Content), nil
}
