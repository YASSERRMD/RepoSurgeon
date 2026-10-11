package plan

import (
	"strings"
	"testing"
)

func validPlan() Plan {
	return Plan{
		Revision: 1,
		Kind:     KindBug,
		Risk:     RiskHigh,
		Summary:  "Fix the pagination off-by-one in the list handler",
		BaseSHA:  strings.Repeat("a", 40),
		Hypotheses: []Hypothesis{{
			Statement: "The limit is incremented before the offset is applied",
			Evidence:  []string{"internal/api/list.go:120", "internal/api/list_test.go:44"},
		}},
		Nodes: []Node{
			{Key: "investigate", Title: "Confirm the off-by-one", Kind: NodeInvestigate, Prerequisites: []string{}, Evidence: []string{"internal/api/list.go:120"}},
			{Key: "fix", Title: "Bound the limit before offset", Kind: NodeEdit, Path: "internal/api/list.go", Prerequisites: []string{"investigate"}, Evidence: []string{"internal/api/list.go:120"}},
			{Key: "test", Title: "Add a regression test", Kind: NodeTest, Prerequisites: []string{"fix"}},
		},
		Acceptance: []AcceptanceCriterion{{
			Key:          "ac1",
			Description:  "A page of 10 never returns 11 rows",
			Verification: "go test ./internal/api/...",
		}},
		OutOfScope:  []string{"UI pagination controls"},
		Assumptions: []string{"The store returns rows in insertion order"},
	}
}

func TestValidateAcceptsWellFormedPlan(t *testing.T) {
	if err := Validate(validPlan()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsBadKindsAndRisk(t *testing.T) {
	for _, kind := range []string{"", "chore", "Bug"} {
		p := validPlan()
		p.Kind = kind
		if err := Validate(p); err == nil {
			t.Fatalf("kind %q accepted", kind)
		}
	}
	for _, risk := range []string{"", "extreme"} {
		p := validPlan()
		p.Risk = risk
		if err := Validate(p); err == nil {
			t.Fatalf("risk %q accepted", risk)
		}
	}
}

func TestValidateRejectsHypothesesWithoutEvidence(t *testing.T) {
	p := validPlan()
	p.Hypotheses = []Hypothesis{{Statement: "unverified guess"}}
	if err := Validate(p); err == nil {
		t.Fatal("hypothesis without evidence must be rejected")
	}
}

func TestValidateDetectsCyclesAndUnknownPrerequisites(t *testing.T) {
	p := validPlan()
	p.Nodes[1].Prerequisites = []string{"test"} // fix depends on test, test depends on fix
	if err := Validate(p); err == nil {
		t.Fatal("cycle must be rejected")
	}

	p = validPlan()
	p.Nodes[0].Prerequisites = []string{"ghost"}
	if err := Validate(p); err == nil {
		t.Fatal("unknown prerequisite must be rejected")
	}
}

func TestValidateRejectsDuplicateNodeKeysAndEditWithoutPath(t *testing.T) {
	p := validPlan()
	p.Nodes[1].Key = "investigate"
	if err := Validate(p); err == nil {
		t.Fatal("duplicate node key must be rejected")
	}

	p = validPlan()
	p.Nodes[1].Path = ""
	if err := Validate(p); err == nil {
		t.Fatal("edit node without a path must be rejected")
	}
}

func TestParsePlan(t *testing.T) {
	p, err := ParsePlan(`{"kind":"bug","risk":"low","summary":"s","base_sha":"` + strings.Repeat("b", 40) + `","nodes":[{"key":"a","title":"t","kind":"verify"}]}`)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(p.Nodes) != 1 {
		t.Fatalf("nodes = %+v", p.Nodes)
	}
	for _, raw := range []string{"", "not json", "[]", `"a string"`} {
		if _, err := ParsePlan(raw); err == nil {
			t.Fatalf("ParsePlan(%q) succeeded", raw)
		}
	}
}

func TestVerifyReportsMissingPathsAndTests(t *testing.T) {
	p := validPlan()
	exists := func(path string) bool { return path != "internal/api/list.go" }
	findings := Verify(p, exists, p.Acceptance)

	blocking := 0
	missingPath := 0
	for _, f := range findings {
		if f.Severity == "error" {
			blocking++
		}
		if f.Code == FindingMissingPath {
			missingPath++
		}
	}
	if missingPath != 1 {
		t.Fatalf("missing path findings = %d", missingPath)
	}
	if !HasBlockingFindings(findings) {
		t.Fatal("missing referenced file must block")
	}

	// Adding test nodes removes the warning.
	p.Nodes = append(p.Nodes, Node{Key: "verify", Title: "Run tests", Kind: NodeVerify})
	if HasBlockingFindings(Verify(p, func(string) bool { return true }, p.Acceptance)) {
		t.Fatal("no blocking findings expected")
	}
}

func TestVerifyWarnsWhenPlanLacksTestsOrAcceptance(t *testing.T) {
	p := Plan{Kind: KindFeature, Risk: RiskLow, Summary: "add a button", BaseSHA: strings.Repeat("c", 40),
		Nodes: []Node{{Key: "a", Title: "edit", Kind: NodeEdit, Path: "x.go"}}}
	findings := Verify(p, func(string) bool { return true }, nil)
	if HasBlockingFindings(findings) {
		t.Fatal("warnings must not block")
	}
	if len(findings) < 2 {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestRequiresApproval(t *testing.T) {
	if !RequiresApproval(validPlan()) {
		t.Fatal("high-risk plans require approval")
	}
	p := validPlan()
	p.Risk = RiskLow
	if RequiresApproval(p) {
		t.Fatal("low-risk plan should not require approval")
	}
	p.Kind = KindSecurity
	if !RequiresApproval(p) {
		t.Fatal("security plans always require approval")
	}
}

func TestInvalidatesApproval(t *testing.T) {
	if !InvalidatesApproval("aaa", "bbb", "c", "c") {
		t.Fatal("base commit change must invalidate")
	}
	if !InvalidatesApproval("aaa", "aaa", "c", "d") {
		t.Fatal("context change must invalidate")
	}
	if InvalidatesApproval("aaa", "aaa", "c", "c") {
		t.Fatal("identical evidence must keep the approval")
	}
}
