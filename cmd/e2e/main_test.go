package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"

	"github.com/NorskHelsenett/ror-test/internal/e2e"
)

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
