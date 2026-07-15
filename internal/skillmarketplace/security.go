package skillmarketplace

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	AttestationVersion = "tma.skill.attestation.v1"
	AttestationPath    = "SKILL.attestation.json"

	AttestationMissing   = "missing"
	AttestationVerified  = "verified"
	AttestationInvalid   = "invalid"
	AttestationUntrusted = "untrusted"

	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"

	PolicyCheckAttestation = "attestation"
	PolicyCheckStaticScan  = "static_scan"
	PolicyCheckBinaryScan  = "binary_scan"
	maxSecurityFindings    = 50
)

type PackageAttestation struct {
	Version      string `json:"version"`
	Algorithm    string `json:"algorithm"`
	KeyID        string `json:"key_id"`
	DigestSHA256 string `json:"digest_sha256"`
	Signature    string `json:"signature"`
}

type AttestationResult struct {
	Status       string `json:"status"`
	Path         string `json:"path,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	Algorithm    string `json:"algorithm,omitempty"`
	DigestSHA256 string `json:"digest_sha256"`
	Message      string `json:"message"`
}

type SecurityFinding struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

type PackageSecurityReport struct {
	DigestSHA256    string             `json:"digest_sha256"`
	Attestation     AttestationResult  `json:"attestation"`
	Findings        []SecurityFinding  `json:"findings"`
	HighestSeverity string             `json:"highest_severity,omitempty"`
	ScannedFiles    int                `json:"scanned_files"`
	FindingsLimited bool               `json:"findings_limited,omitempty"`
	BinaryFiles     []BinaryScanResult `json:"binary_files"`
	SBOM            PackageSBOM        `json:"sbom"`
}

type securityRule struct {
	ID       string
	Severity string
	Message  string
	Pattern  *regexp.Regexp
}

var packageSecurityRules = []securityRule{
	{ID: "prompt_override", Severity: SeverityCritical, Message: "Attempts to override higher-priority instructions.", Pattern: regexp.MustCompile(`(?i)\bignore (?:all |any )?(?:previous|prior|system|developer) instructions?\b`)},
	{ID: "credential_exfiltration", Severity: SeverityCritical, Message: "Requests transmission of secrets or credentials.", Pattern: regexp.MustCompile(`(?i)\b(?:send|upload|post|exfiltrat\w*)\b.{0,80}\b(?:secret|token|credential|password|\.env)\b`)},
	{ID: "remote_shell_pipe", Severity: SeverityCritical, Message: "Pipes a remote download directly into a shell.", Pattern: regexp.MustCompile(`(?i)\bcurl\b[^|]{0,200}\|\s*(?:ba)?sh\b`)},
	{ID: "destructive_root_delete", Severity: SeverityCritical, Message: "Contains a destructive root filesystem delete command.", Pattern: regexp.MustCompile(`(?i)\brm\s+-rf\s+/(?:\s|$)`)},
	{ID: "approval_bypass", Severity: SeverityHigh, Message: "Attempts to bypass approval, security, or policy controls.", Pattern: regexp.MustCompile(`(?i)\b(?:bypass|disable|skip)\b.{0,60}\b(?:approval|security|safety|policy|permission)\b`)},
	{ID: "system_prompt_disclosure", Severity: SeverityHigh, Message: "Requests disclosure of system or developer prompts.", Pattern: regexp.MustCompile(`(?i)\b(?:reveal|print|extract|show)\b.{0,60}\b(?:system|developer) prompt\b`)},
	{ID: "sensitive_file_access", Severity: SeverityHigh, Message: "References a sensitive credential or account file.", Pattern: regexp.MustCompile(`(?i)(?:~/?\.ssh|\.aws/credentials|/etc/shadow)`)},
	{ID: "unapproved_execution", Severity: SeverityMedium, Message: "Requests execution without approval or confirmation.", Pattern: regexp.MustCompile(`(?i)\b(?:execute|run)\b.{0,60}\bwithout (?:asking|approval|confirmation)\b`)},
}

func PackageDigest(pkg Package) string {
	type packageDigestFile struct {
		path    string
		content []byte
	}
	files := []packageDigestFile{{path: "SKILL.md", content: []byte(pkg.Content)}}
	for _, file := range pkg.Files {
		if strings.EqualFold(file.Path, AttestationPath) {
			continue
		}
		content, err := packageFileBytes(file)
		if err != nil {
			content = []byte(file.ContentBase64)
		}
		files = append(files, packageDigestFile{path: file.Path, content: content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	digest := sha256.New()
	for _, file := range files {
		_ = binary.Write(digest, binary.BigEndian, uint64(len([]byte(file.path))))
		_, _ = digest.Write([]byte(file.path))
		_ = binary.Write(digest, binary.BigEndian, uint64(len(file.content)))
		_, _ = digest.Write(file.content)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func AttestationMessage(digest string) []byte {
	return []byte("tma.skill.package.v1:" + strings.ToLower(strings.TrimSpace(digest)))
}

func InspectPackageSecurity(pkg Package, policy Policy) PackageSecurityReport {
	report := PackageSecurityReport{
		DigestSHA256: PackageDigest(pkg), Findings: []SecurityFinding{}, BinaryFiles: []BinaryScanResult{},
	}
	report.SBOM = BuildPackageSBOM(pkg, report.DigestSHA256)
	report.Attestation = verifyPackageAttestation(pkg, report.DigestSHA256, policy.TrustedAttestationKeys)
	files := []PackageFile{{Path: "SKILL.md", Content: pkg.Content}}
	for _, file := range pkg.Files {
		if !strings.EqualFold(file.Path, AttestationPath) && !file.Binary {
			files = append(files, file)
		}
	}
	report.BinaryFiles = InspectBinaryAssets(pkg)
	report.ScannedFiles = len(files) + len(report.BinaryFiles)
	for _, binaryFile := range report.BinaryFiles {
		for _, finding := range binaryFile.Findings {
			report.Findings = append(report.Findings, finding)
			report.HighestSeverity = SeverityCritical
		}
	}
	for _, file := range files {
		for lineIndex, line := range strings.Split(strings.ReplaceAll(file.Content, "\r\n", "\n"), "\n") {
			for _, rule := range packageSecurityRules {
				if !rule.Pattern.MatchString(line) {
					continue
				}
				report.Findings = append(report.Findings, SecurityFinding{
					RuleID: rule.ID, Severity: rule.Severity, Path: file.Path, Line: lineIndex + 1, Message: rule.Message,
				})
				if severityRank(rule.Severity) > severityRank(report.HighestSeverity) {
					report.HighestSeverity = rule.Severity
				}
				if len(report.Findings) == maxSecurityFindings {
					report.FindingsLimited = true
					return report
				}
			}
		}
	}
	return report
}

func (p Policy) EvaluatePackageSecurity(source Source, license string, pkg Package) (PolicyDecision, PackageSecurityReport) {
	decision := p.EvaluatePackage(source, license)
	report := InspectPackageSecurity(pkg, p)
	attestationEnforced := p.RequireAttestation || report.Attestation.Status == AttestationInvalid
	attestationPassed := report.Attestation.Status == AttestationVerified || (!p.RequireAttestation && report.Attestation.Status != AttestationInvalid)
	decision.Checks = append(decision.Checks, PolicyCheck{
		Name: PolicyCheckAttestation, Enforced: attestationEnforced, Passed: attestationPassed, Message: report.Attestation.Message,
	})
	threshold := strings.ToLower(strings.TrimSpace(p.StaticScanBlockSeverity))
	staticScanEnforced := threshold != ""
	staticScanPassed := !staticScanEnforced || severityRank(report.HighestSeverity) < severityRank(threshold)
	staticMessage := fmt.Sprintf("static scan completed with %d finding(s)", len(report.Findings))
	if staticScanEnforced && !staticScanPassed {
		staticMessage = fmt.Sprintf("static scan found %s severity content at or above %s threshold", report.HighestSeverity, threshold)
	}
	decision.Checks = append(decision.Checks, PolicyCheck{
		Name: PolicyCheckStaticScan, Enforced: staticScanEnforced, Passed: staticScanPassed, Message: staticMessage,
	})
	binaryPassed := true
	for _, file := range report.BinaryFiles {
		if file.Status != BinaryScanPassed {
			binaryPassed = false
			break
		}
	}
	binaryMessage := "package contains no binary assets"
	if len(report.BinaryFiles) > 0 {
		binaryMessage = fmt.Sprintf("binary scan passed for %d asset(s)", len(report.BinaryFiles))
		if !binaryPassed {
			binaryMessage = "binary scan blocked one or more assets"
		}
	}
	decision.Checks = append(decision.Checks, PolicyCheck{
		Name: PolicyCheckBinaryScan, Enforced: len(report.BinaryFiles) > 0, Passed: binaryPassed, Message: binaryMessage,
	})
	return newPolicyDecision(decision.Checks), report
}

func verifyPackageAttestation(pkg Package, digest string, trustedKeys map[string]string) AttestationResult {
	result := AttestationResult{Status: AttestationMissing, DigestSHA256: digest, Message: "package attestation is not present"}
	var files []PackageFile
	for _, file := range pkg.Files {
		if strings.EqualFold(file.Path, AttestationPath) {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return result
	}
	result.Path = files[0].Path
	if len(files) != 1 {
		result.Status = AttestationInvalid
		result.Message = "package contains multiple attestation files"
		return result
	}
	var attestation PackageAttestation
	if err := json.Unmarshal([]byte(files[0].Content), &attestation); err != nil {
		result.Status = AttestationInvalid
		result.Message = "package attestation is not valid JSON"
		return result
	}
	result.KeyID = strings.TrimSpace(attestation.KeyID)
	result.Algorithm = strings.ToLower(strings.TrimSpace(attestation.Algorithm))
	if attestation.Version != AttestationVersion || result.Algorithm != "ed25519" || result.KeyID == "" {
		result.Status = AttestationInvalid
		result.Message = "package attestation metadata is invalid"
		return result
	}
	if !strings.EqualFold(strings.TrimSpace(attestation.DigestSHA256), digest) {
		result.Status = AttestationInvalid
		result.Message = "package attestation digest does not match package content"
		return result
	}
	encodedKey, ok := trustedKeys[result.KeyID]
	if !ok {
		result.Status = AttestationUntrusted
		result.Message = fmt.Sprintf("package attestation key %q is not trusted", result.KeyID)
		return result
	}
	publicKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		result.Status = AttestationInvalid
		result.Message = fmt.Sprintf("trusted attestation key %q is invalid", result.KeyID)
		return result
	}
	signature, err := base64.StdEncoding.DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), AttestationMessage(digest), signature) {
		result.Status = AttestationInvalid
		result.Message = "package attestation signature verification failed"
		return result
	}
	result.Status = AttestationVerified
	result.Message = fmt.Sprintf("package attestation verified with key %q", result.KeyID)
	return result
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case SeverityMedium:
		return 1
	case SeverityHigh:
		return 2
	case SeverityCritical:
		return 3
	default:
		return 0
	}
}

func normalizeTrustedAttestationKeys(values map[string]string) (map[string]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("trusted attestation keys cannot exceed 100 entries")
	}
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for keyID, encodedKey := range values {
		keyID = strings.TrimSpace(keyID)
		encodedKey = strings.TrimSpace(encodedKey)
		if !githubOwnerPattern.MatchString(keyID) {
			return nil, fmt.Errorf("invalid attestation key id %q", keyID)
		}
		publicKey, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("attestation key %q must be a base64 Ed25519 public key", keyID)
		}
		result[keyID] = base64.StdEncoding.EncodeToString(publicKey)
	}
	return result, nil
}
