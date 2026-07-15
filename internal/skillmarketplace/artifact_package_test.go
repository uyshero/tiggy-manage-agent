package skillmarketplace

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestParseArtifactPackageSupportsStandardAndWrappedRoots(t *testing.T) {
	for _, root := range []string{"", "code-review/"} {
		t.Run(strings.TrimSuffix(root, "/"), func(t *testing.T) {
			archive := artifactPackageZIP(t, map[string]string{
				root + "SKILL.md":                "---\nname: Offline Review\ndescription: Local package\nlicense: MIT\ninputs_schema:\n  type: object\n  additionalProperties: false\n  properties:\n    strict:\n      type: boolean\n---\nReview carefully.",
				root + "references/checklist.md": "# Checklist\n",
				root + "scripts/check.sh":        "#!/bin/sh\nexit 0\n",
			})
			pkg, err := ParseArtifactPackage(archive, "art_123", "offline-review.zip")
			if err != nil {
				t.Fatalf("parse artifact package: %v", err)
			}
			if pkg.Source.Provider != ArtifactProvider || pkg.Source.ArtifactID != "art_123" || pkg.Name != "Offline Review" || pkg.License != "MIT" || len(pkg.Files) != 2 {
				t.Fatalf("unexpected artifact package: %#v", pkg)
			}
			if !strings.Contains(string(pkg.Manifest), `"inputs_schema"`) {
				t.Fatalf("expected artifact inputs_schema manifest, got %s", pkg.Manifest)
			}
			if pkg.Files[0].Path != "references/checklist.md" || pkg.Files[1].Path != "scripts/check.sh" || !pkg.Files[1].Executable || pkg.Revision == "" {
				t.Fatalf("unexpected artifact package files: %#v", pkg.Files)
			}
		})
	}
}

func TestParseArtifactPackageRejectsUnsafeOrAmbiguousArchives(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{name: "path traversal", files: map[string]string{"SKILL.md": "# Safe", "../secret.txt": "secret"}},
		{name: "multiple roots", files: map[string]string{"one/SKILL.md": "# One", "two/SKILL.md": "# Two"}},
		{name: "multiple wrapper directories", files: map[string]string{"outer/inner/SKILL.md": "# Nested"}},
		{name: "outside root", files: map[string]string{"pkg/SKILL.md": "# Root", "outside.txt": "outside"}},
		{name: "unsupported file", files: map[string]string{"SKILL.md": "# Root", "payload.exe": "MZ"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseArtifactPackage(artifactPackageZIP(t, test.files), "art_bad", "bad.zip"); err == nil {
				t.Fatal("expected unsafe artifact package rejection")
			}
		})
	}
}

func TestParseArtifactPackageRejectsSymlink(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	root, err := writer.Create("SKILL.md")
	if err != nil {
		t.Fatalf("create SKILL.md: %v", err)
	}
	_, _ = root.Write([]byte("# Root"))
	header := &zip.FileHeader{Name: "references/link.md"}
	header.SetMode(os.ModeSymlink | 0o777)
	link, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	_, _ = link.Write([]byte("../outside"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	if _, err := ParseArtifactPackage(buffer.Bytes(), "art_link", "link.zip"); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestArtifactPackageUsesBuiltinSecurityPolicy(t *testing.T) {
	archive := artifactPackageZIP(t, map[string]string{
		"SKILL.md":          "---\nname: Unsafe Offline\nlicense: MIT\n---\n[File](assets/manual.pdf)",
		"assets/manual.pdf": "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*",
	})
	pkg, err := ParseArtifactPackage(archive, "art_eicar", "unsafe.zip")
	if err != nil {
		t.Fatalf("parse unsafe package fixture: %v", err)
	}
	decision, report := (Policy{}).EvaluatePackageSecurity(pkg.Source, pkg.License, pkg)
	if decision.Allowed || len(report.BinaryFiles) != 1 || report.BinaryFiles[0].Status != BinaryScanBlocked {
		t.Fatalf("builtin policy did not block artifact binary: decision=%#v report=%#v", decision, report)
	}
}

func artifactPackageZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write ZIP entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	return buffer.Bytes()
}
