package tools

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxDOCXDocumentXMLBytes = 32 << 20

func readableFileContent(path string, content []byte) (string, bool, error) {
	if strings.EqualFold(filepath.Ext(path), ".docx") {
		text, err := extractDOCXText(content)
		if err != nil {
			return "", false, fmt.Errorf("extract docx text from %q: %w", path, err)
		}
		return text, true, nil
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return "", false, nil
	}
	return string(content), true, nil
}

func extractDOCXText(content []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open package: %w", err)
	}

	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		if file.UncompressedSize64 > maxDOCXDocumentXMLBytes {
			return "", fmt.Errorf("document.xml exceeds %d bytes", maxDOCXDocumentXMLBytes)
		}
		stream, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("open document.xml: %w", err)
		}
		defer stream.Close()
		return extractWordprocessingMLText(io.LimitReader(stream, maxDOCXDocumentXMLBytes+1))
	}
	return "", fmt.Errorf("word/document.xml is missing")
}

func extractWordprocessingMLText(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	var text strings.Builder
	textDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode document.xml: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "t":
				textDepth++
			case "tab":
				text.WriteByte('\t')
			case "br", "cr":
				text.WriteByte('\n')
			}
		case xml.CharData:
			if textDepth > 0 {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				textDepth--
			case "p", "tr":
				writeTextSeparator(&text, '\n')
			case "tc":
				writeTextSeparator(&text, '\t')
			}
		}
	}
	return strings.TrimSpace(text.String()), nil
}

func writeTextSeparator(text *strings.Builder, separator byte) {
	value := text.String()
	if value == "" || value[len(value)-1] == separator {
		return
	}
	text.WriteByte(separator)
}
