package e2e

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/NorskHelsenett/ror/pkg/models/aclmodels/aclcaps"
	"github.com/NorskHelsenett/ror/pkg/rorresources/rordefs"
)

// e2eAdminFixtureAccess mirrors the access list of the seeded e2e-admin grant in
// testenv/seed.js (group admins@e2e.invalid, scope ror, subject all). The
// registry-coverage guard below asserts it grants read+write for every protected
// kind, so a newly protected kind fails the test until both this list and
// seed.js are updated.
var e2eAdminFixtureAccess = []aclcaps.AccessTypeV3{
	"ror:read", "ror:create", "ror:update", "ror:delete",
	"ror:config:read", "ror:config:write",
}

// protectedKind pairs a protected resource kind with the capability guarding it.
type protectedKind struct {
	Kind       string
	Capability aclcaps.Capability
}

// protectedKinds enumerates every protected resource kind and its guarding
// capability from the shared rordefs registry (ApiResource.ProtectedBy), so
// these tests track the registry automatically as kinds gain protection.
func protectedKinds() []protectedKind {
	var out []protectedKind
	for _, r := range rordefs.Resourcedefs {
		if r.ProtectedBy == "" {
			continue
		}
		out = append(out, protectedKind{Kind: r.GetKind(), Capability: r.ProtectedBy})
	}
	return out
}

// buildProtectedGrantsSuite generates an ACL-lookup suite covering every
// protected capability: a holder (admin) is admitted (200) and a non-holder
// (outsider) is denied (403), for both the read and write verb. The lookup
// subject is the kind name (type-level), matching how protected-kind grants are
// keyed ({ror, <Kind>}).
func buildProtectedGrantsSuite() Suite {
	suite := Suite{Version: 1, Name: "Protected resource-kind grants (dynamic)"}
	for _, pk := range protectedKinds() {
		for _, verb := range []aclcaps.Verb{aclcaps.VerbRead, aclcaps.VerbWrite} {
			access := pk.Capability.WithVerb(verb)
			path := fmt.Sprintf("/v2/acl/lookup/ror/%s/%s", pk.Kind, access)
			suite.Steps = append(suite.Steps,
				Step{
					Name:    fmt.Sprintf("admin holds %s for %s", access, pk.Kind),
					Method:  "HEAD",
					Path:    path,
					Headers: map[string]string{"Authorization": "Bearer ${ADMIN_TOKEN}"},
					Status:  200,
				},
				Step{
					Name:    fmt.Sprintf("outsider denied %s for %s", access, pk.Kind),
					Method:  "HEAD",
					Path:    path,
					Headers: map[string]string{"Authorization": "Bearer ${OUTSIDER_TOKEN}"},
					Status:  403,
				},
			)
		}
	}
	return suite
}

// TestProtectedGrants_FixtureCoversRegistry fails when the rordefs registry gains
// a protected kind whose read or write capability the e2e-admin fixture does not
// grant. Keeping the fixture and registry in lockstep keeps the enforcement suite
// valid (admin must be admitted for every protected capability).
func TestProtectedGrants_FixtureCoversRegistry(t *testing.T) {
	kinds := protectedKinds()
	if len(kinds) == 0 {
		t.Skip("no protected resource kinds in registry")
	}
	for _, pk := range kinds {
		for _, verb := range []aclcaps.Verb{aclcaps.VerbRead, aclcaps.VerbWrite} {
			required := pk.Capability.WithVerb(verb)
			if !slices.Contains(e2eAdminFixtureAccess, required) {
				t.Errorf("protected kind %s requires %q, but the e2e-admin fixture (testenv/seed.js) does not grant it", pk.Kind, required)
			}
		}
	}
}

// TestProtectedGrants_SuiteValid asserts the dynamically generated suite is
// well-formed (unique names, valid methods/paths) so the harness can execute it
// unchanged, and that it covers every protected capability for both identities.
func TestProtectedGrants_SuiteValid(t *testing.T) {
	suite := buildProtectedGrantsSuite()
	if len(suite.Steps) == 0 {
		t.Skip("no protected resource kinds in registry")
	}
	if err := suite.Validate(); err != nil {
		t.Fatalf("generated protected-grants suite is invalid: %v", err)
	}
	// One holder + one non-holder step per protected capability verb (read, write).
	if want := len(protectedKinds()) * 2 * 2; len(suite.Steps) != want {
		t.Fatalf("expected %d steps, got %d", want, len(suite.Steps))
	}
}

// TestProtectedGrants_Enforcement runs the generated suite against a live API.
// Opt-in: set E2E_TARGET (a loopback origin) plus ADMIN_TOKEN and OUTSIDER_TOKEN
// (as minted by the synthetic stack). It is skipped in the offline race tests.
func TestProtectedGrants_Enforcement(t *testing.T) {
	target := os.Getenv("E2E_TARGET")
	admin := os.Getenv("ADMIN_TOKEN")
	outsider := os.Getenv("OUTSIDER_TOKEN")
	if target == "" || admin == "" || outsider == "" {
		t.Skip("set E2E_TARGET, ADMIN_TOKEN and OUTSIDER_TOKEN to run against a live API")
	}

	suite := buildProtectedGrantsSuite()
	if len(suite.Steps) == 0 {
		t.Skip("no protected resource kinds in registry")
	}

	variables := map[string]string{"ADMIN_TOKEN": admin, "OUTSIDER_TOKEN": outsider}
	report, err := Run(context.Background(), suite, target, "protected-grants", variables, false)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !report.Passed(len(suite.Steps)) {
		for _, r := range report.Results {
			for _, f := range r.Failures {
				t.Errorf("%s: %s", r.Name, f)
			}
		}
	}
}
