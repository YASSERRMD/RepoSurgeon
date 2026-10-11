// Package tools defines the tool catalog, risk classification and the
// deterministic policy that authorizes tool use. Model output is an untrusted
// proposal; only this package grants authority.
package tools

import (
	"errors"
	"fmt"
)

// Risk classes, ordered from least to most dangerous.
const (
	RiskReadOnly  = "read_only"
	RiskWorkspace = "workspace_write"
	RiskSandboxed = "sandboxed_exec"
	RiskExternal  = "external_side_effect"
)

// Definition describes one tool. The agent may only call registered tools.
type Definition struct {
	Name        string
	Description string
	Risk        string
	// Permission is the role permission required to call the tool.
	Permission string
	// Approval, when true, always requires a human decision before execution.
	Approval bool
	// Sandboxed marks tools that must run inside the isolated runner.
	Sandboxed bool
}

// Catalog is the immutable tool registry.
type Catalog struct {
	definitions map[string]Definition
	order       []string
}

// NewCatalog returns the M07 catalog.
func NewCatalog() *Catalog {
	c := &Catalog{definitions: map[string]Definition{}}
	for _, def := range []Definition{
		{Name: "file_read", Description: "Read an authorized repository file", Risk: RiskReadOnly, Permission: "project.read"},
		{Name: "file_search", Description: "Search repository files by pattern", Risk: RiskReadOnly, Permission: "project.read"},
		{Name: "list_tree", Description: "List the repository file tree", Risk: RiskReadOnly, Permission: "project.read"},
		{Name: "git_status", Description: "Show worktree status", Risk: RiskReadOnly, Permission: "project.read"},
		{Name: "git_diff", Description: "Show the worktree diff", Risk: RiskReadOnly, Permission: "project.read"},
		{Name: "apply_patch", Description: "Apply a structured unified diff inside the worktree", Risk: RiskWorkspace, Permission: "project.write"},
		{Name: "restore_checkpoint", Description: "Restore the worktree to a checkpoint", Risk: RiskWorkspace, Permission: "project.write"},
		{Name: "run_checks", Description: "Run a verified test command inside the sandbox", Risk: RiskSandboxed, Permission: "run.execute", Sandboxed: true},
		{Name: "push_branch", Description: "Push the task branch to GitHub", Risk: RiskExternal, Permission: "run.execute", Approval: true},
		{Name: "open_pr", Description: "Open a pull request", Risk: RiskExternal, Permission: "run.execute", Approval: true},
		{Name: "merge_pr", Description: "Merge a pull request", Risk: RiskExternal, Permission: "merge.execute", Approval: true},
		{Name: "delete_branch", Description: "Delete a merged branch", Risk: RiskExternal, Permission: "run.execute", Approval: true},
		{Name: "request_approval", Description: "Ask a human to decide a pending action", Risk: RiskReadOnly, Permission: "project.read"},
	} {
		c.register(def)
	}
	return c
}

func (c *Catalog) register(def Definition) {
	if _, exists := c.definitions[def.Name]; exists {
		panic("tools: duplicate tool " + def.Name)
	}
	if err := validateRisk(def.Risk); err != nil {
		panic("tools: " + err.Error())
	}
	c.definitions[def.Name] = def
	c.order = append(c.order, def.Name)
}

// Get returns a tool definition.
func (c *Catalog) Get(name string) (Definition, error) {
	def, ok := c.definitions[name]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %q", ErrUnsupportedTool, name)
	}
	return def, nil
}

// All returns every registered tool in registration order.
func (c *Catalog) All() []Definition {
	out := make([]Definition, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, c.definitions[name])
	}
	return out
}

// Declarations renders the catalog as provider tool declarations.
func (c *Catalog) Declarations() []map[string]any {
	out := make([]map[string]any, 0, len(c.order))
	for _, def := range c.definitions {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        def.Name,
				"description": def.Description,
				"parameters": map[string]any{
					"type":       "object",
					"properties": parametersFor(def.Name),
				},
			},
		})
	}
	return out
}

// parametersFor returns the JSON schema of a tool's arguments. Unknown tools
// accept no arguments, so a hallucinated tool cannot smuggle data.
func parametersFor(name string) map[string]any {
	switch name {
	case "file_read":
		return map[string]any{"path": map[string]any{"type": "string", "description": "Repository-relative file path"}}
	case "file_search":
		return map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Literal or glob pattern"},
			"path":    map[string]any{"type": "string", "description": "Optional repository-relative subdirectory"},
		}
	case "list_tree":
		return map[string]any{"path": map[string]any{"type": "string", "description": "Optional repository-relative subdirectory"}}
	case "apply_patch":
		return map[string]any{
			"path":            map[string]any{"type": "string"},
			"unified_diff":    map[string]any{"type": "string"},
			"expected_sha256": map[string]any{"type": "string"},
		}
	case "restore_checkpoint":
		return map[string]any{"checkpoint_id": map[string]any{"type": "string"}}
	case "run_checks":
		return map[string]any{
			"profile":    map[string]any{"type": "string", "description": "Verified command profile"},
			"timeout_ms": map[string]any{"type": "integer"},
		}
	case "push_branch", "open_pr", "merge_pr", "delete_branch":
		return map[string]any{"branch": map[string]any{"type": "string"}}
	case "request_approval":
		return map[string]any{
			"action": map[string]any{"type": "string"},
			"reason": map[string]any{"type": "string"},
		}
	}
	return map[string]any{}
}

// ErrUnsupportedTool is returned for tools outside the catalog.
var ErrUnsupportedTool = errors.New("tools: unsupported tool")

func validateRisk(risk string) error {
	switch risk {
	case RiskReadOnly, RiskWorkspace, RiskSandboxed, RiskExternal:
		return nil
	}
	return fmt.Errorf("tools: unknown risk class %q", risk)
}

// Decision is the policy verdict for a proposed tool call.
type Decision struct {
	Allowed bool
	Reason  string
	// RequiresApproval marks actions a human must explicitly approve.
	RequiresApproval bool
	Risk             string
}

// Policy is the deterministic authorizer. It receives the caller's granted
// permissions and never trusts model-supplied roles.
type Policy struct {
	catalog *Catalog
}

// NewPolicy builds a policy over the catalog.
func NewPolicy(catalog *Catalog) *Policy {
	return &Policy{catalog: catalog}
}

// Authorize decides whether a tool call may run. granted lists the caller's
// permissions for the active organization.
func (p *Policy) Authorize(name string, granted []string) Decision {
	def, err := p.catalog.Get(name)
	if err != nil {
		return Decision{Allowed: false, Reason: err.Error(), Risk: RiskReadOnly}
	}
	if !contains(granted, def.Permission) {
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("permission %q is required for %s", def.Permission, def.Name),
			Risk:    def.Risk,
		}
	}
	if def.Approval {
		return Decision{Allowed: true, RequiresApproval: true, Risk: def.Risk, Reason: "human approval required"}
	}
	return Decision{Allowed: true, Risk: def.Risk, Reason: "permitted by role"}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// RiskRank orders risks so callers can compare severity.
func RiskRank(risk string) int {
	switch risk {
	case RiskReadOnly:
		return 0
	case RiskWorkspace:
		return 1
	case RiskSandboxed:
		return 2
	case RiskExternal:
		return 3
	}
	return 99
}
