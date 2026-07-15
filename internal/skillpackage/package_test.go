package skillpackage

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"sort"
	"testing"

	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skills"
)

func TestRepositoryStoresStandardDeterministicPackage(t *testing.T) {
	client, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "skill-packages",
	})
	if err != nil {
		t.Fatalf("new local object store: %v", err)
	}
	repository, err := NewRepository(client, "skill-packages")
	if err != nil {
		t.Fatalf("new package repository: %v", err)
	}
	input := BuildInput{
		WorkspaceID: "wksp_test", Identifier: "code-review", Version: 2,
		SkillMarkdown: "# Code Review\n\nReview carefully.\n",
		Assets: skills.AssetBundle{Files: []skills.AssetFile{
			{Path: "scripts/check.sh", Content: "#!/bin/sh\nexit 0\n", ContentType: "text/x-shellscript", Executable: true},
			{Path: "references/checklist.md", Content: "# Checklist\n", ContentType: "text/markdown"},
		}},
	}
	first, err := repository.Store(context.Background(), input)
	if err != nil {
		t.Fatalf("store first package: %v", err)
	}
	second, err := repository.Store(context.Background(), input)
	if err != nil {
		t.Fatalf("store second package: %v", err)
	}
	if first.Root != "skills/wksp_test/code-review/versions/2" || first.PackageChecksum != second.PackageChecksum {
		t.Fatalf("package root/checksum is not deterministic: first=%#v second=%#v", first, second)
	}
	firstArchive := packageObject(t, first, ArchivePath)
	secondArchive := packageObject(t, second, ArchivePath)
	if firstArchive.File.ChecksumSHA256 != secondArchive.File.ChecksumSHA256 {
		t.Fatalf("archive checksum is not deterministic: %s != %s", firstArchive.File.ChecksumSHA256, secondArchive.File.ChecksumSHA256)
	}
	archiveBytes := readStoredObject(t, client, firstArchive)
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatalf("open package archive: %v", err)
	}
	paths := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		paths = append(paths, file.Name)
	}
	sort.Strings(paths)
	want := []string{"SKILL.md", "references/checklist.md", "scripts/check.sh"}
	if len(paths) != len(want) {
		t.Fatalf("unexpected archive paths: %v", paths)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("unexpected archive paths: %v", paths)
		}
	}
	skillMD := packageObject(t, first, SkillMarkdownPath)
	if content := string(readStoredObject(t, client, skillMD)); content != input.SkillMarkdown {
		t.Fatalf("unexpected stored SKILL.md: %q", content)
	}
}

func TestRepositoryRejectsReservedAssetPath(t *testing.T) {
	client, err := objectstore.NewLocalFSClient(objectstore.Config{Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new local object store: %v", err)
	}
	repository, err := NewRepository(client, "skills")
	if err != nil {
		t.Fatalf("new package repository: %v", err)
	}
	_, err = repository.Store(context.Background(), BuildInput{
		WorkspaceID: "wksp", Identifier: "reserved", Version: 1, SkillMarkdown: "# Root",
		Assets: skills.AssetBundle{Files: []skills.AssetFile{{Path: "SKILL.md", Content: "duplicate"}}},
	})
	if err == nil {
		t.Fatal("expected reserved SKILL.md path rejection")
	}
}

func packageObject(t *testing.T, stored StoredPackage, filePath string) StoredObject {
	t.Helper()
	for _, item := range stored.Files {
		if item.File.Path == filePath {
			return item
		}
	}
	t.Fatalf("package object %q not found", filePath)
	return StoredObject{}
}

func readStoredObject(t *testing.T, client objectstore.Client, stored StoredObject) []byte {
	t.Helper()
	result, err := client.GetObject(context.Background(), objectstore.GetObjectInput{
		Bucket: stored.Bucket, Key: stored.File.ObjectKey, Version: stored.Version,
	})
	if err != nil {
		t.Fatalf("read stored object %q: %v", stored.File.Path, err)
	}
	defer result.Body.Close()
	content, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read stored object body %q: %v", stored.File.Path, err)
	}
	return content
}
