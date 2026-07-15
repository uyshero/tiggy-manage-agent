package skills

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestAssetBundleRoundTripAndLookup(t *testing.T) {
	raw, err := EncodeAssetBundle(AssetBundle{
		Files:    []AssetFile{{Path: "docs/REFERENCE.md", Content: "Reference text.", Revision: "blob1"}},
		Warnings: []string{" skipped binary ", "skipped binary"},
	})
	if err != nil {
		t.Fatalf("encode assets: %v", err)
	}
	bundle, err := DecodeAssetBundle(raw)
	if err != nil {
		t.Fatalf("decode assets: %v", err)
	}
	file, ok := FindAsset(bundle, "docs/REFERENCE.md")
	if !ok || file.Size != len(file.Content) || bundle.TotalBytes != len(file.Content) || len(bundle.Warnings) != 1 {
		t.Fatalf("unexpected bundle: %#v", bundle)
	}
}

func TestBinaryAssetBundleRequiresInlineOrObjectReference(t *testing.T) {
	raw, err := EncodeAssetBundle(AssetBundle{Files: []AssetFile{{
		Path: "assets/template.png", Binary: true, ContentBase64: base64.StdEncoding.EncodeToString([]byte("png-bytes")), ContentType: "image/png",
	}}})
	if err != nil {
		t.Fatalf("encode transient binary asset: %v", err)
	}
	bundle, err := DecodeAssetBundle(raw)
	if err != nil || len(bundle.Files) != 1 || bundle.Files[0].ChecksumSHA256 == "" || bundle.Files[0].Size != len("png-bytes") {
		t.Fatalf("unexpected transient binary bundle: bundle=%#v err=%v", bundle, err)
	}
	persisted := bundle.Files[0]
	persisted.ContentBase64 = ""
	persisted.ObjectRefID = "obj_000001"
	persisted.ScanStatus = "passed"
	if _, err := EncodeAssetBundle(AssetBundle{Files: []AssetFile{persisted}}); err != nil {
		t.Fatalf("encode persisted binary asset: %v", err)
	}
	persisted.ScanStatus = ""
	if _, err := EncodeAssetBundle(AssetBundle{Files: []AssetFile{persisted}}); err == nil {
		t.Fatal("expected unscanned object reference to be rejected")
	}
}

func TestRenderVersionListsAssetsWithoutInliningContent(t *testing.T) {
	assets, err := EncodeAssetBundle(AssetBundle{Files: []AssetFile{
		{Path: "REFERENCE.md", Content: "private reference body"},
		{Path: "scripts/check.py", Content: "print('check')", Executable: true},
	}})
	if err != nil {
		t.Fatalf("encode assets: %v", err)
	}
	rendered, err := RenderVersion(Skill{Identifier: "review"}, Version{Version: 1, ContentText: "Review carefully.", Assets: assets}, ModeFull, nil)
	if err != nil {
		t.Fatalf("render version: %v", err)
	}
	if !strings.Contains(rendered, "skills.read_asset") || !strings.Contains(rendered, "REFERENCE.md") || !strings.Contains(rendered, "not auto-executable") {
		t.Fatalf("expected asset index, got %s", rendered)
	}
	if strings.Contains(rendered, "private reference body") || strings.Contains(rendered, "print('check')") {
		t.Fatalf("expected asset content to remain out of rendered context: %s", rendered)
	}
}

func TestAssetBundleRejectsUnsafePaths(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"files":[{"path":"../secret.txt","content":"x"}]}`),
		json.RawMessage(`{"files":[{"path":"/secret.txt","content":"x"}]}`),
		json.RawMessage(`{"files":[{"path":"a.txt","content":"x"},{"path":"a.txt","content":"y"}]}`),
	} {
		if _, err := DecodeAssetBundle(raw); err == nil {
			t.Fatalf("expected assets to be rejected: %s", raw)
		}
	}
}
