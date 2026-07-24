package capability

import "testing"

func TestPortableFileReferencePath(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "fileref://workspace/uploads/demo.png", want: "/workspace/uploads/demo.png"},
		{value: "fileref://tmp/crop.png", want: "/tmp/crop.png"},
		{value: "fileref://data/cache/result.json", want: "/mnt/data/cache/result.json"},
		{value: "file:///workspace/report.md", want: "/workspace/report.md"},
	}
	for _, test := range tests {
		got, recognized, err := PortableFileReferencePath(test.value)
		if err != nil || !recognized || got != test.want {
			t.Fatalf("PortableFileReferencePath(%q) = %q, %t, %v; want %q", test.value, got, recognized, err, test.want)
		}
	}
}

func TestParseFileReferenceRejectsUnsupportedOrAmbiguousValues(t *testing.T) {
	for _, value := range []string{
		"fileref://unknown/file.txt",
		"fileref://workspace/file.txt?revision=1",
		"file://remote-host/workspace/file.txt",
		"fileref://artifact/",
		"https://example.com/file.txt",
	} {
		if _, recognized, err := ParseFileReference(value); !recognized || err == nil {
			t.Fatalf("ParseFileReference(%q) recognized=%t err=%v, want recognized error", value, recognized, err)
		}
	}
}
