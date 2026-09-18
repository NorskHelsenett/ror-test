package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type Suite struct {
	Version int               `json:"version"`
	Name    string            `json:"name"`
	Vars    map[string]string `json:"vars"`
	Steps   []Step            `json:"steps"`
}

type Step struct {
	Name            string                     `json:"name"`
	Method          string                     `json:"method"`
	Path            string                     `json:"path"`
	Headers         map[string]string          `json:"headers,omitempty"`
	Body            json.RawMessage            `json:"body,omitempty"`
	Status          int                        `json:"status"`
	Equals          map[string]json.RawMessage `json:"equals,omitempty"`
	Capture         map[string]string          `json:"capture,omitempty"`
	Ignore          []string                   `json:"ignore,omitempty"`
	Unordered       []string                   `json:"unordered,omitempty"`
	ResponseHeaders map[string]string          `json:"responseHeaders,omitempty"`
}

var variablePattern = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]*)\}`)

func LoadSuite(path string) (Suite, error) {
	var suite Suite
	data, err := os.ReadFile(path)
	if err != nil {
		return suite, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		return suite, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return suite, fmt.Errorf("trailing suite data")
	}
	return suite, suite.Validate()
}

func (suite Suite) Validate() error {
	if suite.Version != 1 || suite.Name == "" || len(suite.Steps) == 0 {
		return fmt.Errorf("suite requires version 1, name and steps")
	}
	seen := make(map[string]bool)
	for _, step := range suite.Steps {
		if step.Name == "" || seen[step.Name] {
			return fmt.Errorf("missing or duplicate step name")
		}
		seen[step.Name] = true
		if step.Status < 100 || step.Status > 599 {
			return fmt.Errorf("%s: expected status required", step.Name)
		}
		switch step.Method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("%s: invalid method", step.Name)
		}
		if !strings.HasPrefix(step.Path, "/") || strings.HasPrefix(step.Path, "//") {
			return fmt.Errorf("%s: path must be relative to target", step.Name)
		}
		for _, path := range append(append([]string{}, step.Ignore...), step.Unordered...) {
			if !strings.HasPrefix(path, "/") {
				return fmt.Errorf("%s: normalization must target explicit JSON paths", step.Name)
			}
		}
		for header := range step.Headers {
			if strings.EqualFold(header, "Host") {
				return fmt.Errorf("Host override forbidden")
			}
		}
	}
	return nil
}

func expand(input string, variables map[string]string) (string, error) {
	missing := false
	output := variablePattern.ReplaceAllStringFunc(input, func(match string) string {
		value, exists := variables[match[2:len(match)-1]]
		if !exists {
			missing = true
		}
		return value
	})
	if missing {
		return "", fmt.Errorf("unresolved scenario variable")
	}
	return output, nil
}

func expandValue(value any, variables map[string]string) (any, error) {
	switch node := value.(type) {
	case string:
		return expand(node, variables)
	case map[string]any:
		for key, child := range node {
			expanded, err := expandValue(child, variables)
			if err != nil {
				return nil, err
			}
			node[key] = expanded
		}
	case []any:
		for index, child := range node {
			expanded, err := expandValue(child, variables)
			if err != nil {
				return nil, err
			}
			node[index] = expanded
		}
	}
	return value, nil
}

func ValidateTarget(target string, allowContainer bool) (*url.URL, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("target must be an HTTP(S) origin without credentials")
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if host != "localhost" && (address == nil || !address.IsLoopback()) && !(allowContainer && host == "api") {
		return nil, fmt.Errorf("target must be loopback, or the isolated 'api' Compose service")
	}
	return parsed, nil
}

func Run(ctx context.Context, suite Suite, target, label string, variables map[string]string, allowContainer bool) (Report, error) {
	report := Report{Schema: 1, Suite: fingerprint(suite), Expected: len(suite.Steps), Target: label}
	if err := suite.Validate(); err != nil {
		return report, err
	}
	origin, err := ValidateTarget(target, allowContainer)
	if err != nil {
		return report, err
	}
	values := make(map[string]string)
	for key, value := range suite.Vars {
		values[key] = value
	}
	for key, value := range variables {
		values[key] = value
	}
	aliases := make(map[string]string)
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, step := range suite.Steps {
		started := time.Now()
		result := Result{Name: step.Name}
		path, err := expand(step.Path, values)
		if err != nil {
			return report, fmt.Errorf("%s: %w", step.Name, err)
		}
		relative, err := url.Parse(path)
		if err != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(path, "/") {
			return report, fmt.Errorf("invalid expanded path")
		}
		var body []byte
		if len(step.Body) > 0 {
			value, err := decodeJSON(step.Body)
			if err != nil {
				return report, err
			}
			value, err = expandValue(value, values)
			if err != nil {
				return report, err
			}
			body, err = json.Marshal(value)
			if err != nil {
				return report, err
			}
		}
		request, err := http.NewRequestWithContext(ctx, step.Method, origin.ResolveReference(relative).String(), bytes.NewReader(body))
		if err != nil {
			return report, fmt.Errorf("%s: invalid request", step.Name)
		}
		request.Header.Set("Content-Type", "application/json")
		for key, value := range step.Headers {
			expanded, err := expand(value, values)
			if err != nil {
				return report, fmt.Errorf("%s: %w", step.Name, err)
			}
			request.Header.Set(key, expanded)
		}
		response, err := client.Do(request)
		if err != nil {
			result.Failures = append(result.Failures, "HTTP transport failed")
			report.Results = append(report.Results, result)
			break
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
		response.Body.Close()
		result.Status = response.StatusCode
		if readErr != nil || len(data) > 8*1024*1024 {
			result.Failures = append(result.Failures, "response unreadable or exceeds 8 MiB")
		}
		if result.Status != step.Status {
			result.Failures = append(result.Failures, fmt.Sprintf("status: expected %d, got %d", step.Status, result.Status))
		}
		var value any = string(data)
		if len(data) > 0 {
			decoded, jsonErr := decodeJSON(data)
			if jsonErr == nil {
				value = decoded
			} else if len(step.Equals)+len(step.Capture)+len(step.Ignore)+len(step.Unordered) > 0 {
				result.Failures = append(result.Failures, "expected JSON response")
			}
		}
		for path, expected := range step.Equals {
			wanted, err := decodeJSON(expected)
			if err != nil {
				return report, err
			}
			wanted, err = expandValue(wanted, values)
			if err != nil {
				return report, err
			}
			actual, exists := pointer(value, path)
			if !exists || !equalJSON(actual, wanted) {
				result.Failures = append(result.Failures, "JSON assertion failed at "+path)
			}
		}
		selectedHeaders := make(map[string]string)
		for header, expected := range step.ResponseHeaders {
			actual := response.Header.Get(header)
			selectedHeaders[http.CanonicalHeaderKey(header)] = actual
			if actual != expected {
				result.Failures = append(result.Failures, "header assertion failed: "+header)
			}
		}
		for name, path := range step.Capture {
			captured, exists := pointer(value, path)
			text, isString := captured.(string)
			if !exists || !isString || text == "" {
				result.Failures = append(result.Failures, "capture failed: "+name)
				continue
			}
			if _, exists := values[name]; exists {
				return report, fmt.Errorf("capture variable already defined: %s", name)
			}
			values[name] = text
			aliases[text] = name
		}
		result.Digest = fingerprint([]any{normalize(value, "", step.Ignore, step.Unordered, aliases), selectedHeaders})
		result.DurationMS = time.Since(started).Milliseconds()
		report.Results = append(report.Results, result)
		if len(result.Failures) > 0 {
			break
		}
	}
	return report, nil
}

func (report Report) Passed(expected int) bool {
	if len(report.Results) != expected || expected == 0 {
		return false
	}
	for _, result := range report.Results {
		if len(result.Failures) > 0 {
			return false
		}
	}
	return true
}
