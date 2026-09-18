package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-only" {
			writer.WriteHeader(401)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/create":
			writer.WriteHeader(201)
			writer.Write([]byte(`{"id":"generated","access":["read"]}`))
		case "/generated":
			writer.Write([]byte(`{"id":"generated","access":["read"]}`))
		default:
			writer.WriteHeader(404)
		}
	}))
	defer server.Close()
	suite := Suite{Version: 1, Name: "contract", Steps: []Step{
		{Name: "anonymous", Method: "GET", Path: "/create", Status: 401},
		{Name: "create", Method: "POST", Path: "/create", Status: 201, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}, Capture: map[string]string{"ID": "/id"}, Equals: map[string]json.RawMessage{"/access": json.RawMessage(`["read"]`)}},
		{Name: "read", Method: "GET", Path: "/${ID}", Status: 200, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}, Equals: map[string]json.RawMessage{"/id": json.RawMessage(`"${ID}"`)}},
	}}
	report, err := Run(context.Background(), suite, server.URL, "test", map[string]string{"TOKEN": "test-only"}, false)
	if err != nil || !report.Passed(3) {
		t.Fatalf("run failed: %v %+v", err, report)
	}
	data, _ := json.Marshal(report)
	for _, secret := range []string{"test-only", "generated"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("report leaked %s", secret)
		}
	}
	suite.Steps[0].Status = 200
	failed, err := Run(context.Background(), suite, server.URL, "test", nil, false)
	if err != nil || failed.Passed(3) || len(failed.Results) != 1 {
		t.Fatalf("failure not closed: %v %+v", err, failed)
	}
}

func TestTargetAndRedirectSafety(t *testing.T) {
	for _, target := range []string{"https://prod.example", "http://127.0.0.1@prod.example", "file:///tmp/test", "http://localhost/path", "http://api"} {
		if _, err := ValidateTarget(target, false); err == nil {
			t.Fatalf("unsafe target accepted: %s", target)
		}
	}
	if _, err := ValidateTarget("http://api:8080", true); err != nil {
		t.Fatal(err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://prod.example", 302)
	}))
	defer redirect.Close()
	suite := Suite{Version: 1, Name: "redirect", Steps: []Step{{Name: "no redirect", Method: "GET", Path: "/", Status: 302}}}
	report, err := Run(context.Background(), suite, redirect.URL, "local", nil, false)
	if err != nil || !report.Passed(1) {
		t.Fatalf("redirect was followed: %v %+v", err, report)
	}
}
