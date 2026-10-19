package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	ledongthucpdf "github.com/ledongthuc/pdf"
)

const MaxKnowledgeDocumentBytes int64 = 10 << 20

const (
	defaultOCRTimeout  = 2 * time.Minute
	defaultOCRDPI      = "200"
	defaultOCRLanguage = "eng"
	maxOCRPages        = 20
)

// OCRProvider extracts text from image-based documents. Implementations should
// avoid persisting the source document after the request completes.
type OCRProvider interface {
	Extract(ctx context.Context, filename, contentType string, data []byte) (string, error)
}

// ExtractDocumentText extracts text from supported business knowledge files.
// The caller owns the input bytes; this service never persists the original file.
func ExtractDocumentText(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	return ExtractDocumentTextWithOCR(ctx, filename, contentType, data, NewTesseractOCRProvider())
}

// ExtractDocumentTextWithOCR extracts selectable text first and only invokes
// OCR when a PDF has no selectable text or an image is uploaded.
func ExtractDocumentTextWithOCR(ctx context.Context, filename, contentType string, data []byte, ocr OCRProvider) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if int64(len(data)) > MaxKnowledgeDocumentBytes {
		return "", fmt.Errorf("document exceeds the %d MB limit", MaxKnowledgeDocumentBytes/(1<<20))
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", ".csv":
		return normalizeExtractedText(string(data)), nil
	case ".pdf":
		return extractPDFText(ctx, data, filename, contentType, ocr)
	case ".docx":
		return extractDOCXText(data)
	case ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp":
		if ocr == nil {
			return "", fmt.Errorf("image OCR is not configured; upload a text document instead")
		}
		return ocr.Extract(ctx, filename, contentType, data)
	default:
		if strings.Contains(strings.ToLower(contentType), "text/plain") {
			return normalizeExtractedText(string(data)), nil
		}
		return "", fmt.Errorf("unsupported document type %q; upload PDF, DOCX, TXT, MD, CSV, PNG, or JPG", ext)
	}
}

func extractPDFText(ctx context.Context, data []byte, filename, contentType string, ocr OCRProvider) (string, error) {
	reader, err := ledongthucpdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("could not read PDF: %w", err)
	}
	textReader, err := reader.GetPlainText()
	if err != nil {
		if ocr != nil {
			return ocr.Extract(ctx, filename, contentType, data)
		}
		return "", fmt.Errorf("could not extract PDF text: %w", err)
	}
	textBytes, err := io.ReadAll(textReader)
	if err != nil {
		if ocr != nil {
			return ocr.Extract(ctx, filename, contentType, data)
		}
		return "", fmt.Errorf("could not read extracted PDF text: %w", err)
	}
	text := normalizeExtractedText(string(textBytes))
	if text == "" {
		if ocr == nil {
			return "", fmt.Errorf("the PDF contains no selectable text; scanned PDFs require OCR before ingestion")
		}
		return ocr.Extract(ctx, filename, contentType, data)
	}
	return text, nil
}

// TesseractOCRProvider runs the locally installed Tesseract engine. PDFs are
// rendered to temporary PNG pages with Poppler before OCR. The temporary
// directory is removed before this method returns.
type TesseractOCRProvider struct {
	tesseractBinary string
	pdftoppmBinary  string
	timeout         time.Duration
	dpi             string
	language        string
	maxPages        int
}

func NewTesseractOCRProvider() OCRProvider {
	return &TesseractOCRProvider{
		tesseractBinary: envOrDefault("TESSERACT_BINARY", "tesseract"),
		pdftoppmBinary:  envOrDefault("PDFTOPPM_BINARY", "pdftoppm"),
		timeout:         envDurationOrDefault("OCR_TIMEOUT_SECONDS", defaultOCRTimeout),
		dpi:             envOrDefault("OCR_DPI", defaultOCRDPI),
		language:        envOrDefault("OCR_LANGUAGE", defaultOCRLanguage),
		maxPages:        maxOCRPages,
	}
}

func (p *TesseractOCRProvider) Extract(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	if p == nil {
		return "", fmt.Errorf("local OCR is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ocrCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	tempDir, err := os.MkdirTemp("", "omnipulse-ocr-")
	if err != nil {
		return "", fmt.Errorf("could not prepare the document for OCR")
	}
	defer os.RemoveAll(tempDir)

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".pdf" {
		return p.extractPDF(ocrCtx, tempDir, data)
	}

	if !isOCRImageExtension(ext) {
		return "", fmt.Errorf("OCR is supported for PDF, PNG, JPG, TIFF, and BMP files")
	}
	inputPath := filepath.Join(tempDir, "input"+ext)
	if err := os.WriteFile(inputPath, data, 0600); err != nil {
		return "", fmt.Errorf("could not prepare the image for OCR")
	}
	return p.runTesseract(ocrCtx, inputPath)
}

func (p *TesseractOCRProvider) extractPDF(ctx context.Context, tempDir string, data []byte) (string, error) {
	reader, err := ledongthucpdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("the scanned PDF could not be read")
	}
	pageCount := reader.NumPage()
	if pageCount == 0 {
		return "", fmt.Errorf("the PDF does not contain any pages")
	}
	if pageCount > p.maxPages {
		return "", fmt.Errorf("this scanned PDF has %d pages; the maximum supported is %d pages", pageCount, p.maxPages)
	}

	inputPath := filepath.Join(tempDir, "input.pdf")
	if err := os.WriteFile(inputPath, data, 0600); err != nil {
		return "", fmt.Errorf("could not prepare the PDF for OCR")
	}
	outputPrefix := filepath.Join(tempDir, "page")
	cmd := exec.CommandContext(ctx, p.pdftoppmBinary, "-png", "-r", p.dpi, inputPath, outputPrefix)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("the PDF could not be rendered for OCR: %s", cleanCommandOutput(output, err))
	}

	pages, err := filepath.Glob(outputPrefix + "-*.png")
	if err != nil || len(pages) == 0 {
		return "", fmt.Errorf("the PDF did not produce any OCR pages")
	}
	sort.Slice(pages, func(i, j int) bool { return pageNumber(pages[i]) < pageNumber(pages[j]) })

	var extracted strings.Builder
	for _, page := range pages {
		text, err := p.runTesseract(ctx, page)
		if err != nil {
			return "", err
		}
		if text = normalizeExtractedText(text); text != "" {
			if extracted.Len() > 0 {
				extracted.WriteString("\n\n")
			}
			extracted.WriteString(text)
		}
	}
	if extracted.Len() == 0 {
		return "", fmt.Errorf("no readable text was found in the scanned document")
	}
	return extracted.String(), nil
}

func (p *TesseractOCRProvider) runTesseract(ctx context.Context, inputPath string) (string, error) {
	cmd := exec.CommandContext(ctx, p.tesseractBinary, inputPath, "stdout", "-l", p.language, "--psm", "3")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("local OCR is unavailable or failed; please try again or upload a text-based document")
	}
	return string(output), nil
}

func cleanCommandOutput(output []byte, commandErr error) string {
	if message := strings.TrimSpace(string(output)); message != "" {
		return message
	}
	return commandErr.Error()
}

func isOCRImageExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp":
		return true
	default:
		return false
	}
}

func pageNumber(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	idx := strings.LastIndex(base, "-")
	if idx < 0 {
		return 0
	}
	n, _ := strconv.Atoi(base[idx+1:])
	return n
}

func envOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	seconds, err := strconv.Atoi(envOrDefault(key, strconv.Itoa(int(fallback/time.Second))))
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func extractDOCXText(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("could not read DOCX archive: %w", err)
	}

	var document *zip.File
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", fmt.Errorf("DOCX document.xml is missing")
	}

	file, err := document.Open()
	if err != nil {
		return "", fmt.Errorf("could not open DOCX content: %w", err)
	}
	defer file.Close()

	decoder := xml.NewDecoder(file)
	var out strings.Builder
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return "", fmt.Errorf("could not parse DOCX content: %w", tokenErr)
		}
		switch value := token.(type) {
		case xml.CharData:
			out.Write([]byte(value))
		case xml.StartElement:
			switch value.Name.Local {
			case "tab":
				out.WriteByte('\t')
			case "br", "cr":
				out.WriteByte('\n')
			}
		case xml.EndElement:
			if value.Name.Local == "p" {
				out.WriteByte('\n')
			}
		}
	}

	text := normalizeExtractedText(out.String())
	if text == "" {
		return "", fmt.Errorf("the DOCX document contains no readable text")
	}
	return text, nil
}

func normalizeExtractedText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
