package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
)

func TestExtractDocumentTextPlainText(t *testing.T) {
	got, err := ExtractDocumentText(context.Background(), "faq.txt", "text/plain", []byte("  First answer.\n\nSecond answer.  "))
	if err != nil {
		t.Fatalf("ExtractDocumentText returned error: %v", err)
	}
	if got != "First answer.\nSecond answer." {
		t.Fatalf("unexpected extracted text: %q", got)
	}
}

func TestExtractDocumentTextDOCX(t *testing.T) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	file, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Business hours are 9am to 5pm.</w:t></w:r></w:p><w:p><w:r><w:t>We deliver nationwide.</w:t></w:r></w:p></w:body></w:document>`))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractDocumentText(context.Background(), "business.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", buf.Bytes())
	if err != nil {
		t.Fatalf("ExtractDocumentText returned error: %v", err)
	}
	if got != "Business hours are 9am to 5pm.\nWe deliver nationwide." {
		t.Fatalf("unexpected extracted text: %q", got)
	}
}

func TestExtractDocumentTextRejectsUnsupportedAndOversizedFiles(t *testing.T) {
	if _, err := ExtractDocumentText(context.Background(), "business.exe", "application/octet-stream", []byte("nope")); err == nil {
		t.Fatal("expected unsupported file type error")
	}
	if _, err := ExtractDocumentText(context.Background(), "business.txt", "text/plain", bytes.Repeat([]byte("x"), int(MaxKnowledgeDocumentBytes+1))); err == nil {
		t.Fatal("expected file size error")
	}
}

type testOCRProvider struct {
	called bool
}

func (p *testOCRProvider) Extract(_ context.Context, _, _ string, _ []byte) (string, error) {
	p.called = true
	return "Scanned business information", nil
}

func TestExtractDocumentTextUsesOCRForImages(t *testing.T) {
	ocr := &testOCRProvider{}
	got, err := ExtractDocumentTextWithOCR(context.Background(), "scan.png", "image/png", []byte("image-bytes"), ocr)
	if err != nil {
		t.Fatalf("ExtractDocumentTextWithOCR returned error: %v", err)
	}
	if !ocr.called {
		t.Fatal("expected OCR provider to be called for an image")
	}
	if got != "Scanned business information" {
		t.Fatalf("unexpected OCR text: %q", got)
	}
}
