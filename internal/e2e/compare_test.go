package e2e

import "testing"

func TestVerifyReport(t *testing.T) {
	suite := Suite{Version: 1, Name: "gate", Steps: []Step{{Name: "denied", Method: "HEAD", Path: "/", Status: 403}}}
	valid := func() Report {
		return Report{Schema: 1, Suite: fingerprint(suite), Expected: 1, Results: []Result{{Name: "denied", Status: 403, Digest: fingerprint("body")}}}
	}
	if err := VerifyReport(suite, valid()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Report){
		"empty":     func(report *Report) { report.Results = nil },
		"suite":     func(report *Report) { report.Suite = "other" },
		"schema":    func(report *Report) { report.Schema = 2 },
		"count":     func(report *Report) { report.Expected = 2 },
		"status":    func(report *Report) { report.Results[0].Status = 200 },
		"name":      func(report *Report) { report.Results[0].Name = "other" },
		"digest":    func(report *Report) { report.Results[0].Digest = "" },
		"failure":   func(report *Report) { report.Results[0].Failures = []string{"failed"} },
		"duplicate": func(report *Report) { report.Results = append(report.Results, report.Results[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			report := valid()
			change(&report)
			if VerifyReport(suite, report) == nil {
				t.Fatal("invalid release evidence accepted")
			}
		})
	}
}

func TestCompare(t *testing.T) {
	good := Report{Schema: 1, Suite: "fixture-v1", Expected: 1, Results: []Result{{Name: "denied", Status: 403, Digest: "same"}}}
	if differences := Compare(good, good); len(differences) != 0 {
		t.Fatalf("identical passing reports differ: %v", differences)
	}
	for _, test := range []struct {
		name string
		bad  Report
	}{
		{"matching failures", Report{Schema: 1, Suite: "fixture-v1", Results: []Result{{Name: "denied", Status: 200, Digest: "same", Failures: []string{"expected 403"}}}}},
		{"empty", Report{Schema: 1, Suite: "fixture-v1"}},
		{"truncated", Report{Schema: 1, Suite: "fixture-v1", Expected: 2, Results: good.Results}},
		{"suite mismatch", Report{Schema: 1, Suite: "other", Results: good.Results}},
		{"schema mismatch", Report{Schema: 2, Suite: "fixture-v1", Results: good.Results}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(Compare(test.bad, test.bad)) == 0 && test.name != "suite mismatch" {
				t.Fatal("invalid reports accepted")
			}
			if len(Compare(good, test.bad)) == 0 {
				t.Fatal("regression accepted")
			}
		})
	}
	for _, changed := range []Result{
		{Name: "denied", Status: 200, Digest: "same"},
		{Name: "denied", Status: 403, Digest: "changed"},
		{Name: "missing", Status: 403, Digest: "same"},
	} {
		candidate := Report{Schema: 1, Suite: good.Suite, Expected: 1, Results: []Result{changed}}
		if len(Compare(good, candidate)) == 0 {
			t.Fatalf("missed regression: %+v", changed)
		}
	}
}

func TestNormalizationIsExplicit(t *testing.T) {
	left, _ := decodeJSON([]byte(`{"items":[{"id":"generated-a","time":1},{"id":"stable","time":2}],"access":["read","write"]}`))
	right, _ := decodeJSON([]byte(`{"access":["read","write"],"items":[{"time":9,"id":"stable"},{"time":8,"id":"generated-b"}]}`))
	ignore := []string{"/items/*/time"}
	unordered := []string{"/items"}
	leftNormalized := normalize(left, "", ignore, unordered, map[string]string{"generated-a": "created"})
	rightNormalized := normalize(right, "", ignore, unordered, map[string]string{"generated-b": "created"})
	if !equalJSON(leftNormalized, rightNormalized) {
		t.Fatal("explicit normalization did not match")
	}
	if equalJSON(normalize(left, "", ignore, nil, nil), normalize(right, "", ignore, nil, nil)) {
		t.Fatal("array ordering or IDs silently ignored")
	}
	if _, err := decodeJSON([]byte(`{} {}`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	value, exists := pointer(map[string]any{"a/b": []any{"ok"}}, "/a~1b/0")
	if !exists || value != "ok" {
		t.Fatal("JSON pointer decoding failed")
	}
}
