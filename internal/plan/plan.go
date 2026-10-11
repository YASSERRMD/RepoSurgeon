// Package plan defines typed investigation plans, their verification and the
// deterministic policy that decides whether a plan may progress. A plan is a
// proposal: only verification findings and recorded approvals carry authority.
package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Plan kinds and risk levels.
const (
	KindBug      = "bug"
	KindFeature  = "feature"
	KindRefactor = "refactor"
	KindSecurity = "security"
)

// Risk levels.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Plan statuses.
const (
	StatusDraft            = "draft"
	StatusAwaitingApproval = "awaiting_approval"
	StatusApproved         = "approved"
	StatusRejected         = "rejected"
	StatusSuperseded       = "superseded"
)

// Node kinds.
const (
	NodeInvestigate = "investigate"
	NodeEdit        = "edit"
	NodeTest        = "test"
	NodeVerify      = "verify"
	NodeReview      = "review"
)

// Hypothesis is one root-cause candidate with its evidence.
type Hypothesis struct {
	Statement string   `json:"statement"`
	Evidence  []string `json:"evidence"`
}

// Node is one DAG step. Prerequisites reference other node keys.
type Node struct {
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	Kind          string   `json:"kind"`
	Path          string   `json:"path"`
	LineHint      int      `json:"line_hint"`
	Prerequisites []string `json:"prerequisites"`
	Evidence      []string `json:"evidence"`
}

// AcceptanceCriterion is one testable success condition.
type AcceptanceCriterion struct {
	Key          string `json:"key"`
	Description  string `json:"description"`
	Verification string `json:"verification"`
}

// Plan is the typed proposal produced by the model and verified by us.
type Plan struct {
	Revision    int                   `json:"revision"`
	Kind        string                `json:"kind"`
	Risk        string                `json:"risk"`
	Summary     string                `json:"summary"`
	BaseSHA     string                `json:"base_sha"`
	Hypotheses  []Hypothesis          `json:"hypotheses"`
	Nodes       []Node                `json:"nodes"`
	Acceptance  []AcceptanceCriterion `json:"acceptance"`
	OutOfScope  []string              `json:"out_of_scope"`
	Assumptions []string              `json:"assumptions"`
}

// Errors returned by validation and verification.
var (
	ErrPlanEmpty      = errors.New("plan: plan has no actionable content")
	ErrPlanNoNodes    = errors.New("plan: plan has no nodes")
	ErrPlanBadKind    = errors.New("plan: unknown plan kind")
	ErrPlanBadRisk    = errors.New("plan: unknown risk level")
	ErrPlanNoEvidence = errors.New("plan: hypothesis without evidence")
	ErrPlanCycle      = errors.New("plan: prerequisite cycle or unknown prerequisite")
	ErrPlanNoPaths    = errors.New("plan: plan cites no repository paths")
)

// Validate checks the plan's structure before it reaches the database.
func Validate(p Plan) error {
	if !validKind(p.Kind) {
		return fmt.Errorf("%w: %q", ErrPlanBadKind, p.Kind)
	}
	if !validRisk(p.Risk) {
		return fmt.Errorf("%w: %q", ErrPlanBadRisk, p.Risk)
	}
	if strings.TrimSpace(p.Summary) == "" {
		return fmt.Errorf("%w: summary is required", ErrPlanEmpty)
	}
	if len(p.Nodes) == 0 {
		return ErrPlanNoNodes
	}
	seen := map[string]bool{}
	for _, node := range p.Nodes {
		if node.Key == "" {
			return fmt.Errorf("%w: node without a key", ErrPlanNoNodes)
		}
		if seen[node.Key] {
			return fmt.Errorf("plan: duplicate node key %q", node.Key)
		}
		seen[node.Key] = true
		if !validNodeKind(node.Kind) {
			return fmt.Errorf("plan: node %q has unknown kind %q", node.Key, node.Kind)
		}
		if node.Kind == NodeEdit && strings.TrimSpace(node.Path) == "" {
			return fmt.Errorf("plan: edit node %q has no path", node.Key)
		}
	}
	for _, hypothesis := range p.Hypotheses {
		if len(hypothesis.Evidence) == 0 {
			return fmt.Errorf("%w: %q", ErrPlanNoEvidence, hypothesis.Statement)
		}
	}
	if err := validateAcyclic(p.Nodes); err != nil {
		return err
	}
	return nil
}

func validateAcyclic(nodes []Node) error {
	for _, node := range nodes {
		if err := reachable(node.Key, node.Key, nodes, map[string]bool{}); err != nil {
			return err
		}
	}
	return nil
}

// reachable reports whether target is reachable from the prerequisites of
// start, which detects cycles and unknown prerequisites.
func reachable(key, target string, nodes []Node, visited map[string]bool) error {
	if visited[key] {
		return nil
	}
	visited[key] = true
	var node *Node
	for i := range nodes {
		if nodes[i].Key == key {
			node = &nodes[i]
			break
		}
	}
	if node == nil {
		return fmt.Errorf("%w: %q references unknown node", ErrPlanCycle, key)
	}
	for _, prereq := range node.Prerequisites {
		if prereq == target {
			return fmt.Errorf("%w: %q participates in a cycle", ErrPlanCycle, target)
		}
		if err := reachable(prereq, target, nodes, visited); err != nil {
			return err
		}
	}
	return nil
}

func validKind(kind string) bool {
	switch kind {
	case KindBug, KindFeature, KindRefactor, KindSecurity:
		return true
	}
	return false
}

func validRisk(risk string) bool {
	switch risk {
	case RiskLow, RiskMedium, RiskHigh:
		return true
	}
	return false
}

func validNodeKind(kind string) bool {
	switch kind {
	case NodeInvestigate, NodeEdit, NodeTest, NodeVerify, NodeReview:
		return true
	}
	return false
}

// ParsePlan decodes a plan JSON payload, rejecting non-object payloads.
func ParsePlan(raw string) (Plan, error) {
	var p Plan
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return p, fmt.Errorf("%w: empty payload", ErrPlanEmpty)
	}
	var probe any
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return p, fmt.Errorf("%w: %v", ErrPlanEmpty, err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return p, fmt.Errorf("%w: payload must be a JSON object", ErrPlanEmpty)
	}
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return p, fmt.Errorf("%w: %v", ErrPlanEmpty, err)
	}
	return p, nil
}

// Finding is one verification result.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Finding codes.
const (
	FindingMissingPath     = "missing_path"
	FindingMissingLineHint = "missing_line_hint"
	FindingNoTests         = "no_tests"
	FindingNoAcceptance    = "no_acceptance"
	FindingNoEvidence      = "no_evidence"
)

// FilesArePresent checks that every referenced path exists and returns a
// finding per missing path.
func FilesArePresent(p Plan, exists func(path string) bool) []Finding {
	var out []Finding
	for _, node := range p.Nodes {
		if strings.TrimSpace(node.Path) == "" {
			continue
		}
		if !exists(node.Path) {
			out = append(out, Finding{
				Severity: "error",
				Code:     FindingMissingPath,
				Message:  fmt.Sprintf("plan references %q which is not in the indexed workspace", node.Path),
			})
		}
	}
	return out
}

// HasTestNodes reports whether the plan includes executable verification.
func HasTestNodes(p Plan) bool {
	for _, node := range p.Nodes {
		if node.Kind == NodeTest || node.Kind == NodeVerify {
			return true
		}
	}
	return false
}

// Verify runs the deterministic verification pass and returns findings.
// exists resolves repository paths; acceptance lists the issue's acceptance
// conditions, which the plan must address.
func Verify(p Plan, exists func(string) bool, acceptance []AcceptanceCriterion) []Finding {
	var findings []Finding
	findings = append(findings, FilesArePresent(p, exists)...)
	if !HasTestNodes(p) {
		findings = append(findings, Finding{
			Severity: "warning",
			Code:     FindingNoTests,
			Message:  "plan proposes no test or verification step",
		})
	}
	if len(p.Acceptance) == 0 {
		findings = append(findings, Finding{
			Severity: "warning",
			Code:     FindingNoAcceptance,
			Message:  "plan states no acceptance criteria",
		})
	}
	if len(p.Hypotheses) == 0 {
		findings = append(findings, Finding{
			Severity: "warning",
			Code:     FindingNoEvidence,
			Message:  "plan states no root-cause hypothesis",
		})
	}
	return findings
}

// HasBlockingFindings reports whether any finding prevents progress.
func HasBlockingFindings(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == "error" {
			return true
		}
	}
	return false
}

// RequiresApproval reports whether the plan's risk demands a recorded human
// approval before execution.
func RequiresApproval(p Plan) bool {
	return p.Risk == RiskHigh || p.Kind == KindSecurity
}

// InvalidatesApproval reports whether a change of base commit or context
// invalidates a previously recorded approval, so stale approvals cannot
// authorize new evidence.
func InvalidatesApproval(previousBaseSHA, nextBaseSHA, previousContext, nextContext string) bool {
	return previousBaseSHA != nextBaseSHA || previousContext != nextContext
}
