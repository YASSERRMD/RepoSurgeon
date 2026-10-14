import type { components, paths } from './generated/schema';

/** Wire types come from the OpenAPI contract; the client below never invents shapes. */
export type LiveStatus = components['schemas']['LiveStatus'];
export type ReadinessReport = components['schemas']['ReadinessReport'];
export type ReadinessCheck = components['schemas']['HealthCheck'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type ApiErrorBody = components['schemas']['Error'];

export type Organization = components['schemas']['Organization'];
export type OrganizationPage = components['schemas']['OrganizationPage'];
export type OrganizationCreate = components['schemas']['OrganizationCreate'];
export type OrganizationRole = components['schemas']['OrganizationRole'];
export type Project = components['schemas']['Project'];
export type ProjectPage = components['schemas']['ProjectPage'];
export type ProjectCreate = components['schemas']['ProjectCreate'];
export type ProjectUpdate = components['schemas']['ProjectUpdate'];
export type AuditEvent = components['schemas']['AuditEvent'];
export type AuditEventPage = components['schemas']['AuditEventPage'];
export type ConflictDetail = components['schemas']['ConflictDetail'];

/** Authentication (M02): browser sessions, never bearer tokens in storage. */
export type LoginRequest = components['schemas']['LoginRequest'];
export type SessionResponse = components['schemas']['SessionResponse'];
export type UserSummary = components['schemas']['UserSummary'];
export type CurrentUser = components['schemas']['CurrentUser'];
export type MembershipSummary = components['schemas']['MembershipSummary'];
export type Role = components['schemas']['Role'];
export type Permission = components['schemas']['Permission'];

export type Member = components['schemas']['Member'];
export type MemberPage = components['schemas']['MemberPage'];
export type MemberInvite = components['schemas']['MemberInvite'];
export type MemberUpdate = components['schemas']['MemberUpdate'];

/** Credentials expose metadata only; `value` is write-only in the contract. */
export type Credential = components['schemas']['Credential'];
export type CredentialPage = components['schemas']['CredentialPage'];
export type CredentialCreate = components['schemas']['CredentialCreate'];
export type CredentialRotate = components['schemas']['CredentialRotate'];
export type CredentialKind = components['schemas']['CredentialKind'];
export type CredentialStatus = components['schemas']['CredentialStatus'];

/** Tools (M07): catalog metadata only, never a command, script or secret. */
export type ToolDescriptor = components['schemas']['ToolDescriptor'];
export type ToolRisk = ToolDescriptor['risk'];

/** Approvals (M07): a decision is recorded server-side and never inferred. */
export type Approval = components['schemas']['Approval'];
export type ApprovalState = Approval['state'];
export type ApprovalCreate = components['schemas']['ApprovalCreate'];
export type ApprovalDecision = components['schemas']['ApprovalDecision'];
export type ApprovalDecisionChoice = ApprovalDecision['decision'];

/**
 * Code search and the context inspector (M08). Every hit, file and manifest
 * item describes repository content, which is untrusted data: it is displayed
 * for review and can never grant a permission or become an instruction.
 */
export type SearchHit = components['schemas']['SearchHit'];
export type SymbolHit = components['schemas']['SymbolHit'];
export type SymbolKind = SymbolHit['kind'];
export type IndexedFile = components['schemas']['IndexedFile'];
export type ContextItem = components['schemas']['ContextItem'];
export type ContextManifest = components['schemas']['ContextManifest'];
export type ContextRefresh = components['schemas']['ContextRefresh'];
export type ContextStrategy = ContextManifest['strategy'];
/** The budget choices the contract enumerates; the full window is never implied. */
export type ContextTokenBudget = NonNullable<ContextRefresh['token_budget']>;

/**
 * Plans (M09): a plan revision is what a run proposes to do, and the plan
 * inspector renders the projection the server returns. Status, findings and
 * approvals are never inferred in the browser: a 404 means no plan exists yet,
 * a 409 means the revision was already decided, and a 422 means the referenced
 * files could not be verified.
 */
export type Plan = components['schemas']['Plan'];
export type PlanKind = Plan['kind'];
export type PlanRisk = Plan['risk'];
export type PlanNode = components['schemas']['PlanNode'];
export type NodeKind = PlanNode['kind'];
export type AcceptanceCriterion = components['schemas']['AcceptanceCriterion'];
export type PlanSubmission = components['schemas']['PlanSubmission'];
/** ContractPlanView is the documented read projection of a stored revision. */
export type ContractPlanView = components['schemas']['PlanView'];
export type PlanStatus = ContractPlanView['status'];
export type PlanFinding = ContractPlanView['findings'][number];
export type PlanFindingSeverity = PlanFinding['severity'];
export type PlanApproval = NonNullable<ContractPlanView['approvals']>[number];
export type PlanDecision = components['schemas']['PlanDecision'];
export type PlanDecisionChoice = PlanDecision['decision'];

/**
 * `base_sha`, `out_of_scope` and `assumptions` travel on the PlanSubmission
 * request, but the documented PlanView projection does not repeat them. They
 * are declared here as optional read-through fields so the inspector shows
 * them when a server includes them and stays silent when it does not — the
 * client never fills them in itself.
 */
export type PlanView = ContractPlanView & {
  base_sha?: string;
  out_of_scope?: string[];
  assumptions?: string[];
};

export type ReadinessStatus = ReadinessReport['status'];
export type CheckStatus = ReadinessCheck['status'];
export type ProjectSettings = NonNullable<ProjectUpdate['settings']>;

/**
 * ResponsesOf collects the response blocks of every method declared on a path.
 * Keys that are not operations (parameters, and the methods the generator types
 * as `never`) contribute `never`, which drops out of the resulting union.
 */
type ResponsesOf<TPath extends keyof paths> = {
  [K in keyof paths[TPath]]: paths[TPath][K] extends { responses: infer R } ? R : never;
}[keyof paths[TPath]];

/**
 * Response type helpers keyed by path and status code. The conditional is
 * distributive over the union of methods, so a status that only one method
 * answers (a 201 on POST) resolves to that method's payload.
 */
type JsonOf<TPath extends keyof paths, TStatus extends number> =
  ResponsesOf<TPath> extends infer R
    ? R extends { [K in `${TStatus}`]: infer Response }
      ? Response extends { content: { 'application/json': infer T } }
        ? T
        : never
      : never
    : never;

export type LiveStatusResponse = JsonOf<'/health/live', 200>;
export type ReadinessOkResponse = JsonOf<'/health/ready', 200>;
export type ReadinessUnavailableResponse = JsonOf<'/health/ready', 503>;
export type SystemInfoResponse = JsonOf<'/api/v1/system/info', 200>;
export type OrganizationListResponse = JsonOf<'/api/v1/organizations', 200>;
export type OrganizationCreateResponse = JsonOf<'/api/v1/organizations', 201>;
export type ProjectListResponse = JsonOf<'/api/v1/projects', 200>;
export type ProjectCreateResponse = JsonOf<'/api/v1/projects', 201>;
export type ProjectDetailResponse = JsonOf<'/api/v1/projects/{project_id}', 200>;
export type ProjectUpdateResponse = JsonOf<'/api/v1/projects/{project_id}', 200>;
export type AuditEventListResponse = JsonOf<'/api/v1/audit', 200>;
export type LoginResponse = JsonOf<'/api/v1/auth/login', 200>;
export type SessionRefreshResponse = JsonOf<'/api/v1/auth/refresh', 200>;
export type CurrentUserResponse = JsonOf<'/api/v1/me', 200>;
export type MemberListResponse = JsonOf<'/api/v1/members', 200>;
export type MemberInviteResponse = JsonOf<'/api/v1/members', 201>;
export type MemberUpdateResponse = JsonOf<'/api/v1/members/{membership_id}', 200>;
export type CredentialListResponse = JsonOf<'/api/v1/credentials', 200>;
export type CredentialCreateResponse = JsonOf<'/api/v1/credentials', 201>;
export type CredentialRotateResponse = JsonOf<'/api/v1/credentials/{credential_id}/rotate', 200>;
export type ToolListResponse = JsonOf<'/api/v1/tools', 200>;
export type ApprovalListResponse = JsonOf<'/api/v1/approvals', 200>;
export type ApprovalCreateResponse = JsonOf<'/api/v1/approvals', 201>;
export type ApprovalDecisionResponse = JsonOf<'/api/v1/approvals/{approval_id}/decide', 200>;

export type WorkspaceSearchResponse = JsonOf<'/api/v1/workspaces/{workspace_id}/search', 200>;
export type WorkspaceFileListResponse = JsonOf<'/api/v1/workspaces/{workspace_id}/files', 200>;
export type WorkspaceSymbolLookupResponse = JsonOf<
  '/api/v1/workspaces/{workspace_id}/symbols',
  200
>;
export type RunContextResponse = JsonOf<'/api/v1/runs/{run_id}/context', 200>;
export type RunContextRefreshResponse = JsonOf<'/api/v1/runs/{run_id}/context/refresh', 201>;
export type RecoveryViewResponse = JsonOf<'/api/v1/runs/{run_id}/recovery', 200>;
export type RecoverResponse = JsonOf<'/api/v1/runs/{run_id}/recover', 201>;
export type RollbackResponse = JsonOf<'/api/v1/runs/{run_id}/rollback', 200>;
export type PullRequestOpenResponse = JsonOf<'/api/v1/runs/{run_id}/pr', 201>;
export type PullRequestReplayResponse = JsonOf<'/api/v1/runs/{run_id}/pr', 200>;
export type PullRequestGetResponse = JsonOf<'/api/v1/runs/{run_id}/pr', 200>;
export type MergeResultResponse = JsonOf<'/api/v1/runs/{run_id}/merge', 200>;
export type AbandonResultResponse = JsonOf<'/api/v1/runs/{run_id}/abandon', 200>;
export type PlanResponse = JsonOf<'/api/v1/runs/{run_id}/plan', 200>;
export type PlanSaveResponse = JsonOf<'/api/v1/runs/{run_id}/plan', 201>;
export type PlanDecisionResponse = JsonOf<'/api/v1/runs/{run_id}/plan/approve', 200>;

/** LanguageSummary is one row of the per-language file counts in the index. */
export type LanguageSummary = WorkspaceFileListResponse['languages'][number];

/** ContractRecoveryView is the documented recovery projection of a run. */
export type ContractRecoveryView = components['schemas']['RecoveryView'];
export type RecoveryView = ContractRecoveryView;

/** RecoveryEvent is one journalled entry of the state recovery journal. */
export type RecoveryEvent = RecoveryView['timeline'][number];
/** RecoveryAttempt is one recorded repair attempt. */
export type RecoveryAttempt = RecoveryView['attempts'][number];
/** RecoveryBudget is the bounded attempt budget of one error class. */
export type RecoveryBudget = RecoveryView['budgets'][number];
/** EffectReceipt proves an effect was already applied. */
export type EffectReceipt = RecoveryView['receipts'][number];
/** CheckpointSummary describes one known-good snapshot. */
export type CheckpointSummary = RecoveryView['checkpoints'][number];

/** FailureClass is an error class the recovery engine can bound. */
export type FailureClass = RecoverRequest['class'];

/** RecoverRequest asks the server to plan the next bounded attempt. */
export type RecoverRequest = components['schemas']['RecoverRequest'];
/** RollbackRequest restores the workspace to a snapshot. */
export type RollbackRequest = components['schemas']['RollbackRequest'];

/** RecoveryKind is the journal kind the server recorded. */
export type RecoveryKind = RecoveryEvent['kind'];
/** RecoveryAttemptKind is what one attempt did. */
export type RecoveryAttemptKind = RecoveryAttempt['kind'];

/** ContractRecoverResponse is the server's answer to a bounded recovery. */
export type ContractRecoverResponse = components['schemas']['RecoverResponse'];
/** ContractRollbackResponse is the server's answer to a rollback. */
export type ContractRollbackResponse = components['schemas']['RollbackResponse'];

/** ContractPullRequestView is the documented projection of a tracked pull request. */
export type ContractPullRequestView = components['schemas']['PullRequestView'];
export type PullRequestView = ContractPullRequestView;

/** RemoteCheck is one real GitHub check run or status. */
export type RemoteCheck = NonNullable<PullRequestView['checks']>[number];
/** RemoteReview is one real GitHub review. */
export type RemoteReview = NonNullable<PullRequestView['reviews']>[number];
/** ComposedCommit is one atomic commit behind the pull request. */
export type ComposedCommit = NonNullable<PullRequestView['commits']>[number];
/** PullRequestState is the lifecycle state the server recorded. */
export type PullRequestState = NonNullable<PullRequestView['state']>;

/** CommitSpec is one logical change the operator composed. */
export type CommitSpec = components['schemas']['CommitSpec'];
/** LocalCheck is one locally executed check result. */
export type LocalCheck = components['schemas']['LocalCheck'];

/** OpenPullRequestBody opens one pull request for a run. */
export type OpenPullRequestBody = components['schemas']['OpenPullRequestRequest'];
/** MergePullRequestBody requests the merge of a tracked pull request. */
export type MergePullRequestBody = components['schemas']['MergePullRequestBody'];
/** AbandonBody closes a pull request without merging. */
export type AbandonBody = components['schemas']['AbandonRequest'];

/** MergeResult is the server's answer to a merge request. */
export type MergeResult = components['schemas']['MergeResult'];
/** GateDecision is the push or merge gate verdict with its reasons. */
export type GateDecision = components['schemas']['GateDecision'];
