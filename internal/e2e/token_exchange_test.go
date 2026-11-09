package e2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestTokenExchangeMigrationScenarios(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("..", "..", "testenv", "scenarios", "acl.json"))
	if err != nil {
		t.Fatal(err)
	}

	type expectedScenario struct {
		status  int
		cluster string
		token   string
		admin   bool
		capture string
	}
	expected := map[string]expectedScenario{
		"reader normal exchange on migrated cluster": {
			status: 200, cluster: "${CLUSTER_A}", token: "${READER_TOKEN}", capture: "READER_EXCHANGED_TOKEN_A",
		},
		"reader admin exchange denied on migrated cluster": {
			status: 403, cluster: "${CLUSTER_A}", token: "${READER_TOKEN}", admin: true,
		},
		"admin grant permits exchange on migrated cluster": {
			status: 200, cluster: "${CLUSTER_A}", token: "${ADMIN_TOKEN}", admin: true, capture: "ADMIN_EXCHANGED_TOKEN_A",
		},
		"reader admin exchange uses legacy logon on unmigrated cluster": {
			status: 200, cluster: "${CLUSTER_B}", token: "${READER_TOKEN}", admin: true, capture: "READER_EXCHANGED_TOKEN_B",
		},
		"outsider admin exchange denied on unmigrated cluster": {
			status: 403, cluster: "${CLUSTER_B}", token: "${OUTSIDER_TOKEN}", admin: true,
		},
	}

	found := make(map[string]bool, len(expected))
	for _, step := range suite.Steps {
		want, ok := expected[step.Name]
		if !ok {
			continue
		}
		found[step.Name] = true
		if step.Method != "POST" || step.Path != "/v2/token/exchange" || step.Status != want.status {
			t.Errorf("%s: unexpected request or status", step.Name)
			continue
		}
		if got := step.Headers["Authorization"]; got != "Bearer "+want.token {
			t.Errorf("%s: Authorization = %q, want bearer token variable", step.Name, got)
		}
		var body struct {
			ClusterID string `json:"clusterId"`
			Token     string `json:"token"`
			Admin     bool   `json:"admin"`
		}
		if err := json.Unmarshal(step.Body, &body); err != nil {
			t.Errorf("%s: invalid request body: %v", step.Name, err)
			continue
		}
		if body.ClusterID != want.cluster || body.Token != want.token || body.Admin != want.admin {
			t.Errorf("%s: unexpected token exchange body", step.Name)
		}
		if want.capture == "" {
			if len(step.Capture) != 0 {
				t.Errorf("%s: denied response must not capture a token", step.Name)
			}
		} else if capturePath, exists := step.Capture[want.capture]; !exists || capturePath != "" {
			t.Errorf("%s: expected token capture at response root", step.Name)
		}
	}

	for name := range expected {
		if !found[name] {
			t.Errorf("missing token exchange scenario %q", name)
		}
	}
}
