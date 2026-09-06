package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/safefile"
)

const maxArtifactValidationBytes = 64 << 20

type ValidateArtifactTool struct{ Guard *PathGuard }

type validateArtifactArgs struct {
	Path         string   `json:"path"`
	Format       string   `json:"format"`
	MinBytes     int64    `json:"min_bytes"`
	RequiredText []string `json:"required_text"`
}

func (t ValidateArtifactTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        "validate_artifact",
		Description: "Validate a completed file deliverable after its final write. Confirms a bounded regular file exists, records its SHA-256 digest, parses supported text/Markdown/JSON/CSV/XLSX/DOCX/PPTX/PDF structure, and can require exact text. HTML, CSS, JavaScript and other known source files are checked as UTF-8 text, not parsed or executed; use an appropriate parser or browser to test functionality. This evidence does not prove factual correctness, source quality, or visual polish.",
		InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or permitted absolute artifact path"},"format":{"type":"string","enum":["auto","text","html","markdown","json","csv","xlsx","docx","pptx","pdf","binary"],"description":"auto infers from the extension; html is an alias for UTF-8 text checks, not HTML parsing or browser testing"},"min_bytes":{"type":"integer","minimum":1,"maximum":67108864,"description":"Optional minimum file size"},"required_text":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":512},"description":"Exact text each of which must occur in inspectable artifact content"}},"required":["path"],"additionalProperties":false}`),
	}
}

func (t ValidateArtifactTool) Assess(raw json.RawMessage) (Action, error) {
	args, err := parseValidateArtifactArgs(raw)
	if err != nil {
		return Action{}, err
	}
	path, outside, err := t.Guard.ResolveRead(args.Path)
	return Action{Risk: RiskRead, Summary: "validate artifact " + path, Outside: outside, Paths: []string{path}}, err
}

func (t ValidateArtifactTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	result, err := t.ExecuteResultStream(ctx, raw, nil)
	return result.Content, err
}

func (t ValidateArtifactTool) ExecuteResultStream(ctx context.Context, raw json.RawMessage, _ func(string)) (Result, error) {
	args, err := parseValidateArtifactArgs(raw)
	if err != nil {
		return Result{}, err
	}
	path, _, err := t.Guard.ResolveRead(args.Path)
	if err != nil {
		return Result{}, err
	}
	target, err := safefile.Open(filepath.Dir(path), path)
	if err != nil {
		return Result{}, err
	}
	defer target.Close()
	rootID, err := target.RootIdentity()
	if err != nil {
		return Result{}, err
	}
	info, err := target.Lstat()
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("artifact is not a regular file")
	}
	if info.Size() <= 0 {
		return Result{}, errors.New("artifact is empty")
	}
	if args.MinBytes > 0 && info.Size() < args.MinBytes {
		return Result{}, fmt.Errorf("artifact is %d bytes; expected at least %d", info.Size(), args.MinBytes)
	}
	if info.Size() > maxArtifactValidationBytes {
		return Result{}, fmt.Errorf("artifact is %d bytes; structural validation is bounded at %d bytes", info.Size(), maxArtifactValidationBytes)
	}
	file, err := target.OpenFile()
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(artifactContextReader{ctx, file}, maxArtifactValidationBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > maxArtifactValidationBytes {
		return Result{}, errors.New("artifact grew beyond the validation size limit")
	}
	if len(data) == 0 || int64(len(data)) < args.MinBytes {
		return Result{}, errors.New("artifact became empty or smaller than min_bytes during validation")
	}
	format := artifactFormat(args.Format, path)
	inspection, searchable, err := inspectArtifact(format, data)
	if err != nil {
		return Result{}, fmt.Errorf("validate %s structure: %w", format, err)
	}
	if len(args.RequiredText) > 0 && searchable == "" {
		return Result{}, fmt.Errorf("required_text is not supported for %s artifacts; validate their structure here and inspect/render them with an appropriate external tool", format)
	}
	for _, required := range args.RequiredText {
		if !strings.Contains(searchable, required) {
			return Result{}, fmt.Errorf("required text %q was not found", required)
		}
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	display := path
	if rel, relErr := filepath.Rel(t.Guard.Workspace, path); relErr == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		display = filepath.ToSlash(rel)
	}
	detail := fmt.Sprintf("%s structure parsed; %d bytes", format, len(data))
	if inspection != "" {
		detail += "; " + inspection
	}
	if len(args.RequiredText) > 0 {
		detail += fmt.Sprintf("; %d required text value(s) present", len(args.RequiredText))
	}
	content := fmt.Sprintf("Artifact validation passed: %s\nformat: %s\nsize: %d bytes\nsha256: %s", display, format, len(data), digest)
	if inspection != "" {
		content += "\nstructure: " + inspection
	}
	if len(args.RequiredText) > 0 {
		content += fmt.Sprintf("\nrequired text: %d/%d present", len(args.RequiredText), len(args.RequiredText))
	}
	content += "\nscope: structural and requested-text validation only; factual correctness, source quality, and visual polish were not established"
	if format == "text" {
		content += "\nText checks do not parse or execute source code, HTML, or scripts; syntax and functionality were not tested."
	}
	checks := map[string]string{"structure": "passed", "calculations": "not_assessed", "content": "not_assessed", "sources": "not_assessed", "visual": "not_assessed"}
	if format == "binary" {
		checks["structure"] = "not_assessed"
	}
	if len(args.RequiredText) > 0 {
		checks["content"] = "passed"
	}
	return Result{Content: content, Evidence: &Evidence{Kind: "artifact_validated", Subject: display, Digest: "sha256:" + digest, Detail: detail, Checks: checks, ArtifactRoot: rootID}}, nil
}

func parseValidateArtifactArgs(raw json.RawMessage) (validateArtifactArgs, error) {
	var args validateArtifactArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, err
	}
	if strings.TrimSpace(args.Path) == "" {
		return args, errors.New("path is required")
	}
	if args.MinBytes < 0 || args.MinBytes > maxArtifactValidationBytes {
		return args, fmt.Errorf("min_bytes must be between 1 and %d when set", maxArtifactValidationBytes)
	}
	if len(args.RequiredText) > 32 {
		return args, errors.New("required_text accepts at most 32 values")
	}
	for _, required := range args.RequiredText {
		if strings.TrimSpace(required) == "" {
			return args, errors.New("required_text values must not be empty")
		}
		if len(required) > 512 {
			return args, errors.New("required_text values must be at most 512 bytes")
		}
	}
	switch format := strings.ToLower(strings.TrimSpace(args.Format)); format {
	case "", "auto", "text", "html", "markdown", "json", "csv", "xlsx", "docx", "pptx", "pdf", "binary":
	default:
		return args, fmt.Errorf("unsupported format %q", args.Format)
	}
	return args, nil
}

func artifactFormat(requested, path string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "html" {
		return "text"
	}
	if requested != "" && requested != "auto" {
		return requested
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".log", ".rst", ".html", ".htm", ".css", ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".py", ".go", ".rs", ".sh", ".bash", ".zsh", ".sql", ".yaml", ".yml", ".toml", ".xml", ".svg":
		return "text"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv", ".tsv":
		return "csv"
	case ".xlsx":
		return "xlsx"
	case ".docx":
		return "docx"
	case ".pptx":
		return "pptx"
	case ".pdf":
		return "pdf"
	default:
		return "binary"
	}
}

func inspectArtifact(format string, data []byte) (inspection, searchable string, err error) {
	switch format {
	case "text", "markdown":
		if !utf8.Valid(data) {
			return "", "", errors.New("content is not valid UTF-8")
		}
		text := string(data)
		lines := 1 + strings.Count(text, "\n")
		if format == "markdown" {
			headings := 0
			for _, line := range strings.Split(text, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "#### ") || strings.HasPrefix(trimmed, "##### ") || strings.HasPrefix(trimmed, "###### ") {
					headings++
				}
			}
			return fmt.Sprintf("valid UTF-8 Markdown; %d line(s), %d heading(s)", lines, headings), text, nil
		}
		return fmt.Sprintf("valid UTF-8 text; %d line(s)", lines), text, nil
	case "json":
		if !utf8.Valid(data) {
			return "", "", errors.New("content is not valid UTF-8")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", "", err
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values are present")
			}
			return "", "", err
		}
		return "valid single JSON value", string(data), nil
	case "csv":
		if !utf8.Valid(data) {
			return "", "", errors.New("content is not valid UTF-8")
		}
		reader := csv.NewReader(bytes.NewReader(data))
		if bytes.Contains(data, []byte{'\t'}) && !bytes.Contains(data, []byte{','}) {
			reader.Comma = '\t'
		}
		records, err := reader.ReadAll()
		if err != nil {
			return "", "", err
		}
		if len(records) == 0 {
			return "", "", errors.New("no CSV records")
		}
		return fmt.Sprintf("valid delimited data; %d row(s), %d column(s)", len(records), len(records[0])), string(data), nil
	case "xlsx", "docx":
		return inspectOpenXML(data, format)
	case "pptx":
		return inspectOpenXML(data, "pptx")
	case "pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", "", errors.New("missing PDF header")
		}
		if !bytes.Contains(data[max(0, len(data)-2048):], []byte("%%EOF")) {
			return "", "", errors.New("missing PDF end marker")
		}
		pages := bytes.Count(data, []byte("/Type /Page")) - bytes.Count(data, []byte("/Type /Pages"))
		if pages < 0 {
			pages = 0
		}
		return fmt.Sprintf("PDF header and end marker present; %d directly declared page object(s)", pages), "", nil
	case "binary":
		return "bounded regular binary file; no format-specific parser selected", "", nil
	default:
		return "", "", fmt.Errorf("unsupported format %q", format)
	}
}

func inspectOpenXML(data []byte, format string) (inspection, searchable string, err error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", err
	}
	if len(archive.File) > 4096 {
		return "", "", errors.New("package exceeds 4096 parts")
	}
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		if files[file.Name] != nil {
			return "", "", errors.New("duplicate package part")
		}
		files[file.Name] = file
	}
	required := []string{"[Content_Types].xml"}
	if format == "docx" {
		required = append(required, "word/document.xml")
	} else if format == "xlsx" {
		required = append(required, "xl/workbook.xml", "xl/_rels/workbook.xml.rels")
	} else {
		required = append(required, "ppt/presentation.xml")
	}
	for _, name := range required {
		if files[name] == nil {
			return "", "", fmt.Errorf("missing required package part %s", name)
		}
	}
	if format == "xlsx" {
		if err := checkWorkbookRelationships(files); err != nil {
			return "", "", err
		}
	}
	var targets []string
	if format == "docx" {
		targets = []string{"[Content_Types].xml", "word/document.xml"}
	} else if format == "xlsx" {
		targets = []string{"[Content_Types].xml", "xl/workbook.xml", "xl/_rels/workbook.xml.rels"}
		sheets := 0
		for name := range files {
			if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") && !strings.Contains(strings.TrimPrefix(name, "xl/worksheets/"), "/") {
				targets = append(targets, name)
				sheets++
			}
		}
		if sheets == 0 {
			return "", "", errors.New("workbook contains no worksheet XML parts")
		}
		if files["xl/sharedStrings.xml"] != nil {
			targets = append(targets, "xl/sharedStrings.xml")
		}
		sort.Strings(targets[3:])
	} else {
		targets = []string{"[Content_Types].xml", "ppt/presentation.xml"}
		for name := range files {
			if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
				targets = append(targets, name)
			}
		}
		sort.Strings(targets[2:])
		if len(targets) == 2 {
			return "", "", errors.New("presentation contains no slide XML parts")
		}
	}
	var text strings.Builder
	paragraphs := 0
	var totalUncompressed uint64
	for _, name := range targets {
		totalUncompressed += files[name].UncompressedSize64
		if totalUncompressed > maxArtifactValidationBytes {
			return "", "", errors.New("selected package parts exceed validation bound")
		}
	}
	for _, name := range targets {
		part, err := readZipPart(files[name])
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", name, err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(part))
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", "", fmt.Errorf("parse %s: %w", name, err)
			}
			switch value := token.(type) {
			case xml.StartElement:
				if value.Name.Local == "p" {
					paragraphs++
				}
				if value.Name.Local == "t" {
					var fragment string
					if err := decoder.DecodeElement(&fragment, &value); err != nil {
						return "", "", fmt.Errorf("parse text in %s: %w", name, err)
					}
					text.WriteString(fragment)
					text.WriteByte('\n')
				}
			}
		}
	}
	if format == "docx" {
		return fmt.Sprintf("valid DOCX package; document XML parsed, %d paragraph element(s)", paragraphs), text.String(), nil
	}
	if format == "xlsx" {
		return "XLSX package parts and worksheet XML parsed; formulas and cached values are not recalculated or reconciled", text.String(), nil
	}
	return fmt.Sprintf("valid PPTX package; %d slide(s), %d paragraph element(s)", len(targets)-2, paragraphs), text.String(), nil
}

// Check that the declared sheets actually exist; an unrelated worksheet XML
// file must not make a workbook with dangling sheet references validate.
func checkWorkbookRelationships(files map[string]*zip.File) error {
	workbook, err := readZipPart(files["xl/workbook.xml"])
	if err != nil {
		return err
	}
	var book struct {
		XMLName xml.Name `xml:"workbook"`
		Sheets  []struct {
			ID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(workbook, &book); err != nil {
		return fmt.Errorf("parse workbook: %w", err)
	}
	if len(book.Sheets) == 0 {
		return errors.New("workbook declares no sheets")
	}
	data, err := readZipPart(files["xl/_rels/workbook.xml.rels"])
	if err != nil {
		return err
	}
	var relationships struct {
		XMLName xml.Name `xml:"Relationships"`
		Items   []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Type   string `xml:"Type,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(data, &relationships); err != nil {
		return err
	}
	targets := map[string]string{}
	seen := map[string]bool{}
	for _, rel := range relationships.Items {
		if rel.ID == "" || seen[rel.ID] {
			return errors.New("missing or duplicate workbook relationship ID")
		}
		seen[rel.ID] = true
		if rel.Mode == "External" || !strings.HasSuffix(rel.Type, "/worksheet") {
			continue
		}
		target := path.Clean(path.Join("xl", rel.Target))
		if strings.HasPrefix(rel.Target, "/") {
			target = strings.TrimPrefix(path.Clean(rel.Target), "/")
		}
		if !strings.HasPrefix(target, "xl/worksheets/") || files[target] == nil {
			return errors.New("worksheet relationship target missing or unsupported")
		}
		targets[rel.ID] = target
	}
	for _, sheet := range book.Sheets {
		if targets[sheet.ID] == "" {
			return errors.New("declared sheet has no local worksheet relationship")
		}
	}
	return nil
}

func readZipPart(file *zip.File) ([]byte, error) {
	if file == nil {
		return nil, errors.New("package part is missing")
	}
	if file.UncompressedSize64 > maxArtifactValidationBytes {
		return nil, errors.New("package part exceeds validation bound")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxArtifactValidationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArtifactValidationBytes {
		return nil, errors.New("package part exceeds validation bound")
	}
	return data, nil
}
