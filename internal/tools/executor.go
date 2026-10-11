package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

// Executor executes authorized tool calls inside a workspace. It is the only
// component allowed to touch the filesystem on the worker's behalf, and it
// refuses anything outside the catalog or outside the workspace root.
//
// Every file operation goes through root, an *os.Root opened on the worktree,
// so no path (including symlinks) can escape the workspace by construction.
type Executor struct {
	catalog  *Catalog
	policy   *Policy
	worktree string
	root     *os.Root
	// Runner executes sandboxed commands. It may be nil for read-only tools.
	Runner CommandRunner
	// Grant is the caller's permission set for the active organization.
	Grant []string
	// Approver records approval decisions; nil means no approvals are pending.
	Approver Approver
}

// CommandRunner runs one verified command profile inside the sandbox.
type CommandRunner interface {
	Run(ctx context.Context, profile string, workdir string, timeoutMS int) (CommandResult, error)
}

// CommandResult is a bounded command outcome.
type CommandResult struct {
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Approver asks a human to approve a pending action and records the decision.
type Approver interface {
	RequestApproval(ctx context.Context, action, reason string) (approved bool, err error)
}

// NewExecutor builds an executor for one workspace. The worktree is opened as
// a filesystem root so every later access is confined to it.
func NewExecutor(catalog *Catalog, policy *Policy, worktree string, grant []string) (*Executor, error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, fmt.Errorf("tools: open workspace root: %w", err)
	}
	return &Executor{catalog: catalog, policy: policy, worktree: worktree, root: root, Grant: grant}, nil
}

// Execute runs one tool call after the policy allows it.
func (e *Executor) Execute(ctx context.Context, call model.ToolCall) (string, error) {
	def, err := e.catalog.Get(call.Function.Name)
	if err != nil {
		return "", err
	}
	decision := e.policy.Authorize(def.Name, e.Grant)
	if !decision.Allowed {
		return "", fmt.Errorf("tools: %s", decision.Reason)
	}
	if decision.RequiresApproval {
		if e.Approver == nil {
			return "", errors.New("tools: human approval is required but no approver is configured")
		}
		approved, err := e.Approver.RequestApproval(ctx, def.Name, call.Function.Name)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("tools: %s was denied by the approver", def.Name)
		}
	}
	return e.dispatch(ctx, def, call)
}

// dispatch runs the tool implementation. Every path is confined to the
// worktree; hidden and credential paths are refused.
func (e *Executor) dispatch(ctx context.Context, def Definition, call model.ToolCall) (string, error) {
	switch def.Name {
	case "file_read":
		return e.fileRead(ctx, call)
	case "file_search":
		return e.fileSearch(ctx, call)
	case "list_tree":
		return e.listTree(ctx, call)
	case "git_status":
		return e.gitStatus(ctx)
	case "git_diff":
		return e.gitDiff(ctx)
	case "run_checks":
		return e.runChecks(ctx, call)
	case "request_approval":
		return e.requestApproval(ctx, call)
	case "apply_patch", "restore_checkpoint", "push_branch", "open_pr", "merge_pr", "delete_branch":
		// These mutating tools are wired in later milestones; refusing them is
		// safer than pretending they ran.
		return "", fmt.Errorf("tools: %s is not available in this milestone", def.Name)
	}
	return "", fmt.Errorf("%w: %q", ErrUnsupportedTool, def.Name)
}

// safePath canonicalises a repository-relative path and refuses escapes,
// hidden paths and credential files. The returned path is relative to the
// workspace root and is only ever used through the executor's os.Root.
func (e *Executor) safePath(rel string) (string, error) {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return "", errors.New("tools: path is required")
	}
	if len(trimmed) > 1024 || strings.ContainsRune(trimmed, 0) {
		return "", errors.New("tools: path is invalid")
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("tools: path %q escapes the workspace", rel)
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("tools: path %q escapes the workspace", rel)
	}
	// Deny hidden paths and credential material.
	lower := strings.ToLower(trimmed)
	for _, segment := range strings.Split(lower, "/") {
		if strings.HasPrefix(segment, ".") {
			return "", fmt.Errorf("tools: path %q is hidden and cannot be read", rel)
		}
	}
	for _, denied := range []string{".env", ".env.", "id_rsa", "id_ed25519", ".pem", ".key", "credentials.json", "secrets.json"} {
		if strings.Contains(lower, denied) {
			return "", fmt.Errorf("tools: path %q looks like credential material and cannot be read", rel)
		}
	}
	return cleaned, nil
}

func (e *Executor) fileRead(ctx context.Context, call model.ToolCall) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := model.ParseToolArguments(call, &args); err != nil {
		return "", err
	}
	path, err := e.safePath(args.Path)
	if err != nil {
		return "", err
	}
	info, err := statPath(ctx, e.root, path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("tools: %s is a directory; use list_tree", args.Path)
	}
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("tools: %s exceeds the %d byte read limit", args.Path, maxFileBytes)
	}
	data, err := readFile(ctx, e.root, path)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"path": args.Path, "content": string(data), "size": len(data)}
	return marshalJSON(payload)
}

const maxFileBytes = 1 << 20

func (e *Executor) fileSearch(_ context.Context, call model.ToolCall) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := model.ParseToolArguments(call, &args); err != nil {
		return "", err
	}
	root := e.worktree
	if strings.TrimSpace(args.Path) != "" {
		safe, err := e.safePath(args.Path)
		if err != nil {
			return "", err
		}
		root = filepath.Join(e.worktree, safe)
	}
	matches, err := searchFiles(root, args.Pattern, maxSearchMatches)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"pattern": args.Pattern, "matches": matches, "truncated": len(matches) >= maxSearchMatches})
}

const maxSearchMatches = 200

func (e *Executor) listTree(_ context.Context, call model.ToolCall) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := model.ParseToolArguments(call, &args); err != nil {
		return "", err
	}
	root := e.worktree
	if strings.TrimSpace(args.Path) != "" {
		safe, err := e.safePath(args.Path)
		if err != nil {
			return "", err
		}
		root = filepath.Join(e.worktree, safe)
	}
	entries, err := gitworkspace.ListFiles(root)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"entries": entries, "truncated": len(entries) >= 5000})
}

func (e *Executor) gitStatus(ctx context.Context) (string, error) {
	return e.runGit(ctx, "status", "--porcelain")
}

func (e *Executor) gitDiff(ctx context.Context) (string, error) {
	return e.runGit(ctx, "diff")
}

func (e *Executor) runChecks(ctx context.Context, call model.ToolCall) (string, error) {
	if e.Runner == nil {
		return "", errors.New("tools: run_checks requires an isolated runner")
	}
	var args struct {
		Profile   string `json:"profile"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := model.ParseToolArguments(call, &args); err != nil {
		return "", err
	}
	if !verifiedProfile(args.Profile) {
		return "", fmt.Errorf("tools: %q is not a verified command profile", args.Profile)
	}
	result, err := e.Runner.Run(ctx, args.Profile, e.worktree, args.TimeoutMS)
	if err != nil {
		return "", err
	}
	return marshalJSON(result)
}

// verifiedProfiles lists the command profiles the runner may execute. Raw
// model-supplied shell strings are never accepted.
var verifiedProfiles = map[string]bool{
	"go.test":       true,
	"go.build":      true,
	"npm.test":      true,
	"npm.typecheck": true,
	"npm.lint":      true,
	"lint.go":       true,
}

func verifiedProfile(profile string) bool { return verifiedProfiles[strings.TrimSpace(profile)] }

func (e *Executor) requestApproval(ctx context.Context, call model.ToolCall) (string, error) {
	if e.Approver == nil {
		return "", errors.New("tools: no approver is configured")
	}
	var args struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := model.ParseToolArguments(call, &args); err != nil {
		return "", err
	}
	approved, err := e.Approver.RequestApproval(ctx, args.Action, args.Reason)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"action": args.Action, "approved": approved})
}

func (e *Executor) runGit(ctx context.Context, args ...string) (string, error) {
	manager, err := gitworkspace.New(e.worktree)
	if err != nil {
		return "", err
	}
	return manager.RunGit(ctx, args...)
}

func marshalJSON(payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
