package e2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestTokenExchangeIdentityBindingScenarios(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("..", "..", "testenv", "scenarios", "acl.json"))
	if err != nil {
		t.Fatal(err)
	}
	type scenario struct {
		bearer, token, cluster string
		admin                  bool
		status                 int
	}
	expected := map[string]scenario{
		"stored admin cannot elevate fresh reader":     {"${ADMIN_TOKEN}", "${READER_TOKEN}", "${CLUSTER_A}", true, 403},
		"stored admin cannot elevate fresh outsider":   {"${ADMIN_TOKEN}", "${OUTSIDER_TOKEN}", "${CLUSTER_A}", true, 403},
		"legacy logon cannot elevate another identity": {"${READER_TOKEN}", "${OUTSIDER_TOKEN}", "${CLUSTER_B}", true, 403},
		"normal exchange rejects mixed identities":     {"${ADMIN_TOKEN}", "${READER_TOKEN}", "${CLUSTER_A}", false, 403},
		"cluster key cannot delegate user elevation":   {"", "${ADMIN_TOKEN}", "${CLUSTER_A}", true, 403},
		"body token alone cannot authorize exchange":   {"", "${ADMIN_TOKEN}", "${CLUSTER_A}", true, 401},
	}
	for _, step := range suite.Steps {
		want, ok := expected[step.Name]
		if !ok {
			continue
		}
		delete(expected, step.Name)
		if step.Method != "POST" || step.Path != "/v2/token/exchange" || step.Status != want.status {
			t.Errorf("%s: wrong exchange method/path/status", step.Name)
		}
		bearer := ""
		if want.bearer != "" {
			bearer = "Bearer " + want.bearer
		}
		if step.Headers["Authorization"] != bearer {
			t.Errorf("%s: wrong bearer identity", step.Name)
		}
		key := ""
		if step.Name == "cluster key cannot delegate user elevation" {
			key = "${CLUSTER_A_KEY}"
		}
		if step.Headers["X-API-KEY"] != key {
			t.Errorf("%s: wrong API key", step.Name)
		}
		var body struct {
			Token     string `json:"token"`
			ClusterID string `json:"clusterId"`
			Admin     bool   `json:"admin"`
		}
		if err := json.Unmarshal(step.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Token != want.token || body.ClusterID != want.cluster || body.Admin != want.admin {
			t.Errorf("%s: wrong exchanged identity or target", step.Name)
		}
		if len(step.Capture) != 0 {
			t.Errorf("%s: denied exchange must not capture a signed token", step.Name)
		}
	}
	for name := range expected {
		t.Errorf("missing identity binding regression scenario %q", name)
	}
}
