package main

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NorskHelsenett/ror-test/internal/e2e"
)

func TestVerifyReportCommand(t *testing.T) {
	directory := t.TempDir()
	suite := e2e.Suite{Version: 1, Name: "test", Steps: []e2e.Step{{Name: "denied", Method: "GET", Path: "/", Status: 403}}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(403) }))
	defer server.Close()
	report, err := e2e.Run(context.Background(), suite, server.URL, "candidate", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	suitePath, reportPath, output := filepath.Join(directory, "suite.json"), filepath.Join(directory, "report.json"), filepath.Join(directory, "verified.json")
	if err := writeJSON(suitePath, suite); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(reportPath, report); err != nil {
		t.Fatal(err)
	}
	args := []string{"-suite", suitePath, "-report", reportPath, "-out", output}
	if err := verifyReport(args); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	report.Results[0].Status = 200
	if err := writeJSON(reportPath, report); err != nil {
		t.Fatal(err)
	}
	if verifyReport(args) == nil {
		t.Fatal("incorrect status accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed verification emitted success evidence")
	}
}

func TestCompareSameFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "report.json")
	report := e2e.Report{Schema: 1, Suite: "fixture-v1", Expected: 1, Results: []e2e.Result{{Name: "deny", Status: 403, Digest: "same"}}}
	if err := writeJSON(path, report); err != nil {
		t.Fatal(err)
	}
	if err := compare([]string{"-baseline", path, "-candidate", path, "-out", filepath.Join(directory, "diff.json")}); err != nil {
		t.Fatal(err)
	}
	report.Expected = 2
	if err := writeJSON(path, report); err != nil {
		t.Fatal(err)
	}
	if err := compare([]string{"-baseline", path, "-candidate", path, "-out", filepath.Join(directory, "diff.json")}); err == nil {
		t.Fatal("truncated reports accepted")
	}
}

func TestJUnitAndArtifactPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.xml")
	report := e2e.Report{Results: []e2e.Result{{Name: "deny", Failures: []string{"expected 403"}}}}
	if err := writeJUnit(path, "contract", report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(data, &suite); err != nil || suite.Tests != 1 || suite.Failures != 1 {
		t.Fatalf("incorrect JUnit: %v %+v", err, suite)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("artifact is not private: %v", err)
	}
}
