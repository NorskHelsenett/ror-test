package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type Result struct {
	Name       string   `json:"name"`
	Status     int      `json:"status"`
	Digest     string   `json:"digest"`
	Failures   []string `json:"failures,omitempty"`
	DurationMS int64    `json:"durationMs"`
}

type Report struct {
	Schema   int      `json:"schema"`
	Suite    string   `json:"suite"`
	Expected int      `json:"expected"`
	Target   string   `json:"target"`
	Results  []Result `json:"results"`
}

type Difference struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func VerifyReport(suite Suite, report Report) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	if report.Schema != 1 || report.Suite != fingerprint(suite) || report.Expected != len(suite.Steps) || !report.Passed(len(suite.Steps)) {
		return fmt.Errorf("report is incomplete, failed, or belongs to a different suite")
	}
	for index, step := range suite.Steps {
		result := report.Results[index]
		digest, err := hex.DecodeString(result.Digest)
		if result.Name != step.Name || result.Status != step.Status || err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("report result does not match suite step %q", step.Name)
		}
	}
	return nil
}

func Compare(baseline, candidate Report) []Difference {
	var differences []Difference
	if baseline.Schema != 1 || candidate.Schema != 1 || baseline.Suite != candidate.Suite {
		return []Difference{{Reason: "report schema or suite fingerprint mismatch"}}
	}
	if baseline.Expected <= 0 || candidate.Expected != baseline.Expected || len(baseline.Results) != baseline.Expected || len(candidate.Results) != candidate.Expected {
		return []Difference{{Reason: "incomplete report or expected step count mismatch"}}
	}
	indexed := make(map[string]Result)
	for _, result := range baseline.Results {
		if _, exists := indexed[result.Name]; exists {
			differences = append(differences, Difference{result.Name, "duplicate baseline result"})
		}
		indexed[result.Name] = result
		if len(result.Failures) > 0 {
			differences = append(differences, Difference{result.Name, "baseline assertion failed"})
		}
	}
	seen := make(map[string]bool)
	for _, result := range candidate.Results {
		if seen[result.Name] {
			differences = append(differences, Difference{result.Name, "duplicate candidate result"})
		}
		seen[result.Name] = true
		if len(result.Failures) > 0 {
			differences = append(differences, Difference{result.Name, "candidate assertion failed"})
		}
		previous, exists := indexed[result.Name]
		if !exists {
			differences = append(differences, Difference{result.Name, "missing baseline result"})
			continue
		}
		if previous.Status != result.Status || previous.Digest != result.Digest {
			differences = append(differences, Difference{result.Name, "response differs"})
		}
	}
	for _, result := range baseline.Results {
		if !seen[result.Name] {
			differences = append(differences, Difference{result.Name, "missing candidate result"})
		}
	}
	return differences
}

func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON value")
	}
	return value, nil
}

func pointer(value any, path string) (any, bool) {
	if path == "" {
		return value, true
	}
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	for _, part := range strings.Split(path[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = node[part]
			if !exists {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			value = node[index]
		default:
			return nil, false
		}
	}
	return value, true
}

func fingerprint(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func normalize(value any, path string, ignore, unordered []string, aliases map[string]string) any {
	for _, pattern := range ignore {
		if matchesPath(pattern, path) {
			return "<ignored>"
		}
	}
	switch node := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(node))
		for key, child := range node {
			escaped := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			result[key] = normalize(child, path+"/"+escaped, ignore, unordered, aliases)
		}
		return result
	case []any:
		result := make([]any, len(node))
		for index, child := range node {
			result[index] = normalize(child, path+"/"+strconv.Itoa(index), ignore, unordered, aliases)
		}
		for _, pattern := range unordered {
			if matchesPath(pattern, path) {
				sort.SliceStable(result, func(left, right int) bool {
					return fingerprint(result[left]) < fingerprint(result[right])
				})
			}
		}
		return result
	case string:
		if alias, exists := aliases[node]; exists {
			return "${" + alias + "}"
		}
	}
	return value
}

func matchesPath(pattern, path string) bool {
	wanted, actual := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(wanted) != len(actual) {
		return false
	}
	for index := range wanted {
		if wanted[index] != "*" && wanted[index] != actual[index] {
			return false
		}
	}
	return true
}

func equalJSON(left, right any) bool {
	return reflect.DeepEqual(left, right)
}
