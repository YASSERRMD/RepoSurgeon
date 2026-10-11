package tools

import (
	"strings"
	"testing"
)

func TestCatalogRejectsUnknownTools(t *testing.T) {
	catalog := NewCatalog()
	if _, err := catalog.Get("file_read"); err != nil {
		t.Fatalf("registered tool rejected: %v", err)
	}
	for _, name := range []string{"", "shell", "bash", "read_file", "../etc/passwd"} {
		if _, err := catalog.Get(name); err == nil {
			t.Fatalf("unknown tool %q accepted", name)
		}
	}
}

func TestCatalogRiskAndPermissions(t *testing.T) {
	catalog := NewCatalog()
	def, err := catalog.Get("apply_patch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if def.Risk != RiskWorkspace || def.Permission != "project.write" {
		t.Fatalf("apply_patch = %+v", def)
	}
	for _, name := range []string{"push_branch", "open_pr", "merge_pr", "delete_branch"} {
		def, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		if !def.Approval {
			t.Fatalf("%s must require approval", name)
		}
		if def.Risk != RiskExternal {
			t.Fatalf("%s risk = %q", name, def.Risk)
		}
	}
	run, err := catalog.Get("run_checks")
	if err != nil {
		t.Fatalf("Get(run_checks): %v", err)
	}
	if !run.Sandboxed {
		t.Fatal("run_checks must be sandboxed")
	}
}

func TestPolicyAllowsAndDenies(t *testing.T) {
	policy := NewPolicy(NewCatalog())
	readOnly := []string{"project.read"}
	writer := []string{"project.read", "project.write", "run.execute"}

	if decision := policy.Authorize("file_read", readOnly); !decision.Allowed {
		t.Fatalf("file_read denied: %+v", decision)
	}
	if decision := policy.Authorize("apply_patch", readOnly); decision.Allowed {
		t.Fatal("apply_patch allowed without project.write")
	}
	if decision := policy.Authorize("apply_patch", writer); !decision.Allowed || decision.RequiresApproval {
		t.Fatalf("apply_patch = %+v", decision)
	}
	if decision := policy.Authorize("run_checks", readOnly); decision.Allowed {
		t.Fatal("run_checks allowed without run.execute")
	}
	if decision := policy.Authorize("merge_pr", writer); decision.Allowed && !decision.RequiresApproval {
		t.Fatal("merge_pr must require approval")
	}
	if decision := policy.Authorize("merge_pr", []string{"merge.execute"}); !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("merge_pr with merge.execute = %+v", decision)
	}
	// Unknown tools never pass, whatever the caller holds.
	if decision := policy.Authorize("bash", []string{"project.read", "project.write", "run.execute", "merge.execute"}); decision.Allowed {
		t.Fatal("unknown tool was allowed")
	}
}

func TestDeclarationsCoverEveryTool(t *testing.T) {
	catalog := NewCatalog()
	declarations := catalog.Declarations()
	if len(declarations) != len(catalog.All()) {
		t.Fatalf("declarations = %d, tools = %d", len(declarations), len(catalog.All()))
	}
	for _, declaration := range declarations {
		fn, ok := declaration["function"].(map[string]any)
		if !ok {
			t.Fatalf("declaration shape = %+v", declaration)
		}
		if fn["name"] == "" || fn["description"] == "" {
			t.Fatalf("incomplete declaration: %+v", fn)
		}
		if _, ok := fn["parameters"].(map[string]any); !ok {
			t.Fatalf("missing parameters: %+v", fn)
		}
	}
}

func TestRiskRankOrders(t *testing.T) {
	if RiskRank(RiskExternal) <= RiskRank(RiskSandboxed) || RiskRank(RiskSandboxed) <= RiskRank(RiskWorkspace) {
		t.Fatal("risk ranking is not ordered")
	}
	if RiskRank("nonsense") < RiskRank(RiskExternal) {
		t.Fatal("unknown risks must rank highest")
	}
}

func TestApprovalToolsAreExternalOnly(t *testing.T) {
	for _, def := range NewCatalog().All() {
		if def.Approval && def.Risk != RiskExternal {
			t.Fatalf("%s requires approval but has risk %q", def.Name, def.Risk)
		}
	}
}

func TestToolNamesAreStable(t *testing.T) {
	all := NewCatalog().All()
	names := make([]string, 0, len(all))
	for _, def := range all {
		names = append(names, def.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"file_read", "file_search", "list_tree", "apply_patch", "run_checks", "git_status", "git_diff", "request_approval"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("catalog is missing %s", want)
		}
	}
}
