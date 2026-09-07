package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateArtifactMarkdownProducesBoundedEvidence(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.md")
	if err := os.WriteFile(path, []byte("# Findings\n\nEvidence-backed result.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	result, err := tool.ExecuteResultStream(context.Background(), json.RawMessage(`{"path":"report.md","required_text":["Findings","Evidence-backed"]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence == nil || result.Evidence.Kind != "artifact_validated" || result.Evidence.Subject != "report.md" || !strings.HasPrefix(result.Evidence.Digest, "sha256:") {
		t.Fatalf("evidence=%+v", result.Evidence)
	}
	if result.Evidence.Checks["content"] != "passed" || result.Evidence.Checks["structure"] != "passed" || result.Evidence.Checks["calculations"] != "not_assessed" || result.Evidence.Checks["sources"] != "not_assessed" || result.Evidence.Checks["visual"] != "not_assessed" {
		t.Fatalf("incorrect assessment scopes: %+v", result.Evidence.Checks)
	}
	for _, want := range []string{"valid UTF-8 Markdown", "1 heading", "2/2 present", "visual polish were not established"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("result missing %q:\n%s", want, result.Content)
		}
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"report.md","required_text":["Missing section"]}`)); err == nil {
		t.Fatal("missing required text was accepted")
	}
}

func TestValidateXLSXRequiresLinkedWorksheetsAndDoesNotClaimCalculation(t *testing.T) {
	parts := map[string]string{
		"[Content_Types].xml":        `<Types/>`,
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Data" r:id="r1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="r1" Target="worksheets/sheet1.xml" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c t="inlineStr"><is><t>Hours</t></is></c><c><f>1/0</f></c></row></sheetData></worksheet>`,
	}
	dir := t.TempDir()
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	write := func() {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "book.xlsx"), openXMLFixture(t, parts), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	result, err := tool.ExecuteResultStream(t.Context(), json.RawMessage(`{"path":"book.xlsx","required_text":["Hours"]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence.Checks["calculations"] != "not_assessed" || !strings.Contains(result.Content, "not recalculated") {
		t.Fatalf("overclaimed XLSX scope: %+v", result)
	}
	parts["xl/_rels/workbook.xml.rels"] = `<Relationships><Relationship Id="r1" Target="worksheets/missing.xml" Type="a/worksheet"/></Relationships>`
	write()
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"path":"book.xlsx"}`)); err == nil {
		t.Fatal("dangling worksheet accepted")
	}
}

// Opt-in interoperability check against a real bundle from the locked kit;
// ordinary Go tests do not require a Python/LibreOffice installation.
func TestWorkKitArtifactInterop(t *testing.T) {
	dir := os.Getenv("COLLO_WORK_BUNDLE")
	if dir == "" {
		t.Skip("set COLLO_WORK_BUNDLE to a generated Work bundle")
	}
	guard, err := NewPathGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	for _, name := range []string{"report.xlsx", "memo.docx", "memo.pdf"} {
		raw, _ := json.Marshal(map[string]any{"path": name})
		result, err := tool.ExecuteResultStream(t.Context(), raw, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.Evidence == nil || result.Evidence.Checks["structure"] != "passed" || result.Evidence.Checks["visual"] != "not_assessed" {
			t.Fatalf("%s: %+v", name, result)
		}
	}
}

func TestValidateArtifactRejectsInvalidStructuredFormats(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "bad.json"), []byte(`{"open":`), 0o644); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"bad.json"}`)); err == nil || !strings.Contains(err.Error(), "json") {
		t.Fatalf("invalid JSON error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "bad.pdf"), []byte("not a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"bad.pdf"}`)); err == nil || !strings.Contains(err.Error(), "PDF header") {
		t.Fatalf("invalid PDF error=%v", err)
	}
}

func TestValidateArtifactParsesOpenXMLPackages(t *testing.T) {
	workspace := t.TempDir()
	docx := openXMLFixture(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="urn:types"/>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="urn:word"><w:body><w:p><w:r><w:t>Quarterly status</w:t></w:r></w:p></w:body></w:document>`,
	})
	pptx := openXMLFixture(t, map[string]string{
		"[Content_Types].xml":   `<?xml version="1.0"?><Types xmlns="urn:types"/>`,
		"ppt/presentation.xml":  `<?xml version="1.0"?><p:presentation xmlns:p="urn:ppt"/>`,
		"ppt/slides/slide1.xml": `<?xml version="1.0"?><p:sld xmlns:p="urn:ppt" xmlns:a="urn:drawing"><a:p><a:r><a:t>Executive summary</a:t></a:r></a:p></p:sld>`,
	})
	if err := os.WriteFile(filepath.Join(workspace, "status.docx"), docx, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "brief.pptx"), pptx, 0o644); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	for _, test := range []struct {
		args, want string
	}{
		{`{"path":"status.docx","required_text":["Quarterly status"]}`, "valid DOCX package"},
		{`{"path":"brief.pptx","required_text":["Executive summary"]}`, "1 slide(s)"},
	} {
		got, err := tool.Execute(context.Background(), json.RawMessage(test.args))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, test.want) {
			t.Errorf("result missing %q:\n%s", test.want, got)
		}
	}
}

func TestValidateArtifactCSVPDFFallbackAndMinimumSize(t *testing.T) {
	workspace := t.TempDir()
	fixtures := map[string][]byte{
		"table.csv":   []byte("name,value\nalpha,42\n"),
		"summary.pdf": []byte("%PDF-1.4\n1 0 obj <</Type /Page>> endobj\n%%EOF\n"),
		"archive.bin": {0x00, 0x01, 0x02, 0x03},
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(workspace, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := NewPathGuard(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	tool := ValidateArtifactTool{Guard: guard}
	for _, test := range []struct {
		args, want string
	}{
		{`{"path":"table.csv","required_text":["alpha"]}`, "2 row(s), 2 column(s)"},
		{`{"path":"summary.pdf"}`, "1 directly declared page object(s)"},
		{`{"path":"archive.bin","min_bytes":4}`, "bounded regular binary file"},
	} {
		got, err := tool.Execute(context.Background(), json.RawMessage(test.args))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, test.want) {
			t.Errorf("result missing %q:\n%s", test.want, got)
		}
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"archive.bin","min_bytes":5}`)); err == nil || !strings.Contains(err.Error(), "expected at least 5") {
		t.Fatalf("minimum-size error=%v", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"archive.bin","required_text":["anything"]}`)); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("binary required-text error=%v", err)
	}
}

func openXMLFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
