import type {
  ApiErrorBody,
  Approval,
  ApprovalCreate,
  ApprovalDecision,
  ApprovalListResponse,
  AuditEventPage,
  ContextRefresh,
  CurrentUser,
  Credential,
  CredentialCreate,
  CredentialPage,
  CredentialRotate,
  LiveStatus,
  LoginRequest,
  ModelProviderStatus,
  Member,
  MemberInvite,
  MemberPage,
  MemberUpdate,
  Organization,
  OrganizationCreate,
  OrganizationPage,
  PlanDecision,
  PlanSubmission,
  PlanView,
  AbandonBody,
  DashboardView,
  IssueListResponse,
  MergePullRequestBody,
  PreferencesSaveRequest,
  PreferencesView,
  MergeResult,
  OpenPullRequestBody,
  PullRequestView,
  RecoverRequest,
  RecoverResponse,
  RecoveryView,
  RollbackRequest,
  RollbackResponse,
  Project,
  ProjectCreate,
  ProjectPage,
  ProjectUpdate,
  ReadinessReport,
  RunContextRefreshResponse,
  RunContextResponse,
  RunListResponse,
  SessionResponse,
  SystemInfo,
  ToolListResponse,
  WorkspaceFileListResponse,
  WorkspaceSearchResponse,
  WorkspaceSymbolLookupResponse,
} from './types';

/** Request timeout for probe calls; readiness must stay responsive for operators. */
const DEFAULT_TIMEOUT_MS = 8000;

/** Header names defined by the OpenAPI contract's `components/parameters`. */
const IDEMPOTENCY_KEY_HEADER = 'Idempotency-Key';
const CSRF_TOKEN_HEADER = 'X-CSRF-Token';

export interface ApiErrorInit {
  status: number;
  code: string;
  message: string;
  requestId: string;
  retryable: boolean;
  /** details carries structured conflict data such as `current_version`. */
  details?: Record<string, unknown> | undefined;
}

/** ApiError carries the canonical server error envelope to the UI. */
export class ApiError extends Error implements ApiErrorInit {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;
  readonly details: Record<string, unknown> | undefined;

  constructor(init: ApiErrorInit) {
    super(init.message);
    this.name = 'ApiError';
    this.status = init.status;
    this.code = init.code;
    this.requestId = init.requestId;
    this.retryable = init.retryable;
    this.details = init.details;
  }

  /** currentVersion reads `details.current_version` sent on a 409 conflict. */
  currentVersion(): number | null {
    const value = this.details?.current_version;
    return typeof value === 'number' ? value : null;
  }
}

function retryable(status: number): boolean {
  return status === 0 || status === 408 || status === 429 || status >= 500;
}

/** parseError converts a non-2xx response into an ApiError with the server code. */
async function parseError(response: Response): Promise<ApiError> {
  let parsedBody: Partial<ApiErrorBody>;
  try {
    parsedBody = (await response.json()) as Partial<ApiErrorBody>;
  } catch {
    parsedBody = {};
  }
  const error = parsedBody.error;
  return new ApiError({
    status: response.status,
    code: error?.code ?? 'http_error',
    message: error?.message ?? `request failed with status ${response.status}`,
    requestId: error?.request_id ?? response.headers.get('X-Request-ID') ?? '',
    retryable: retryable(response.status),
    details: error?.details,
  });
}

/**
 * csrfToken is held in module memory only. It is never written to
 * localStorage, sessionStorage or a cookie of ours: the session travels in the
 * HttpOnly cookie the server sets, and this value only proves the request came
 * from our own page.
 */
let csrfToken: string | null = null;

/** setCsrfToken records the token returned by login or refresh. */
export function setCsrfToken(token: string | null): void {
  csrfToken = token === null || token.trim() === '' ? null : token;
}

/** getCsrfToken returns the in-memory CSRF token, or null before any session. */
export function getCsrfToken(): string | null {
  return csrfToken;
}

export interface RequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  /** acceptStatuses are non-2xx statuses whose body is a successful payload. */
  acceptStatuses?: number[];
}

export type HttpMethod = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

/** Mutations carry CSRF protection; read-only calls never need the token. */
const MUTATING_METHODS: readonly HttpMethod[] = ['POST', 'PATCH', 'PUT', 'DELETE'];

interface RawRequest {
  method: HttpMethod;
  path: string;
  body?: unknown;
  idempotencyKey?: string | undefined;
  signal?: AbortSignal | undefined;
  timeoutMs?: number | undefined;
  acceptStatuses?: number[] | undefined;
}

/**
 * newIdempotencyKey builds a stable-per-user-action key. The timestamp prefix
 * keeps keys unique across rapid repeats and the random suffix keeps them
 * unique within the same millisecond.
 */
export function newIdempotencyKey(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `idem_${Date.now().toString(36)}_${random}`;
}

/** query serialises defined parameters, skipping empty values entirely. */
function query(params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams();
  for (const [name, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue;
    search.set(name, String(value));
  }
  const serialized = search.toString();
  return serialized === '' ? '' : `?${serialized}`;
}

/** send performs one call against the API and turns failures into ApiError. */
async function send(init: RawRequest): Promise<Response> {
  const timeoutMs = init.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timeout = AbortSignal.timeout(timeoutMs);
  const signal = init.signal ? AbortSignal.any([init.signal, timeout]) : timeout;

  const headers: Record<string, string> = { Accept: 'application/json' };
  if (init.body !== undefined) headers['Content-Type'] = 'application/json';
  if (init.idempotencyKey !== undefined) {
    headers[IDEMPOTENCY_KEY_HEADER] = init.idempotencyKey;
  }
  if (csrfToken !== null && MUTATING_METHODS.includes(init.method)) {
    headers[CSRF_TOKEN_HEADER] = csrfToken;
  }

  // `include` is mandatory: the session cookie is the only way a browser
  // authenticates. `no-referrer` keeps internal URLs out of other origins.
  const requestInit: RequestInit = {
    method: init.method,
    headers,
    credentials: 'include',
    referrerPolicy: 'no-referrer',
    signal,
  };
  if (init.body !== undefined) {
    requestInit.body = JSON.stringify(init.body);
  }

  let response: Response;
  try {
    response = await fetch(init.path, requestInit);
  } catch (cause) {
    const reason = cause instanceof Error ? cause.message : 'network error';
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: `could not reach the RepoSurgeon API (${reason})`,
      requestId: '',
      retryable: true,
    });
  }

  const accepted = init.acceptStatuses?.includes(response.status) ?? false;
  if (!response.ok && !accepted) {
    throw await parseError(response);
  }
  return response;
}

/** request performs one typed JSON call against the API and returns parsed JSON. */
async function request<T>(init: RawRequest): Promise<T> {
  const response = await send(init);
  return (await response.json()) as T;
}

/** requestEmpty performs a call whose success carries no body (204). */
async function requestEmpty(init: RawRequest): Promise<void> {
  await send(init);
}

export interface OrganizationListParams {
  cursor?: string;
  limit?: number;
}

export interface ProjectListParams {
  organization_id: string;
  q?: string;
  cursor?: string;
  limit?: number;
}

export interface AuditEventListParams {
  project_id: string;
  action?: string;
  cursor?: string;
  limit?: number;
}

export interface MemberListParams {
  organization_id: string;
  cursor?: string;
  limit?: number;
}

export interface CredentialListParams {
  organization_id: string;
  cursor?: string;
  limit?: number;
}

/**
 * HealthClient is the typed browser client for the health, system, auth,
 * organization, project, member, credential, audit, tool, approval, search,
 * context and plan endpoints. Every method maps 1:1 to an operation in
 * api/openapi.yaml; the drift check in CI fails when the contract changes
 * without regenerating this client's types.
 */
export class HealthClient {
  constructor(private readonly baseUrl: string = '') {}

  private call<T>(init: RawRequest): Promise<T> {
    return request<T>(init);
  }

  private callEmpty(init: RawRequest): Promise<void> {
    return requestEmpty(init);
  }

  getLive(options?: RequestOptions): Promise<LiveStatus> {
    return this.call<LiveStatus>({
      method: 'GET',
      path: `${this.baseUrl}/health/live`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * getReady treats HTTP 503 as a successful call: the unavailable report is
   * the operator-facing payload. Any other failure still throws ApiError.
   */
  getReady(options?: RequestOptions): Promise<ReadinessReport> {
    return this.call<ReadinessReport>({
      method: 'GET',
      path: `${this.baseUrl}/health/ready`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses ?? [503],
    });
  }

  getSystemInfo(options?: RequestOptions): Promise<SystemInfo> {
    return this.call<SystemInfo>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/system/info`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * login exchanges email and password for a session cookie and CSRF token.
   * The token is kept in memory for the mutations that follow.
   */
  async login(body: LoginRequest): Promise<SessionResponse> {
    const session = await this.call<SessionResponse>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/auth/login`,
      body,
    });
    setCsrfToken(session.csrf_token);
    return session;
  }

  /** logout revokes the current session; the server clears the cookie. */
  logout(): Promise<void> {
    return this.callEmpty({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/auth/logout`,
    });
  }

  /** refreshSession rotates the session and returns a fresh CSRF token. */
  async refreshSession(): Promise<SessionResponse> {
    const session = await this.call<SessionResponse>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/auth/refresh`,
    });
    setCsrfToken(session.csrf_token);
    return session;
  }

  /** getCurrentUser returns the caller with roles and permissions per organization. */
  getCurrentUser(options?: RequestOptions): Promise<CurrentUser> {
    return this.call<CurrentUser>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/me`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  listOrganizations(
    params: OrganizationListParams = {},
    options?: RequestOptions,
  ): Promise<OrganizationPage> {
    return this.call<OrganizationPage>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/organizations${query({
        cursor: params.cursor,
        limit: params.limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  createOrganization(body: OrganizationCreate, key: string): Promise<Organization> {
    return this.call<Organization>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/organizations`,
      body,
      idempotencyKey: key,
    });
  }

  listProjects(params: ProjectListParams, options?: RequestOptions): Promise<ProjectPage> {
    return this.call<ProjectPage>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/projects${query({
        organization_id: params.organization_id,
        q: params.q,
        cursor: params.cursor,
        limit: params.limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  createProject(body: ProjectCreate, key: string): Promise<Project> {
    return this.call<Project>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/projects`,
      body,
      idempotencyKey: key,
    });
  }

  getProject(projectId: string, options?: RequestOptions): Promise<Project> {
    return this.call<Project>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/projects/${encodeURIComponent(projectId)}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  updateProject(projectId: string, body: ProjectUpdate, key: string): Promise<Project> {
    return this.call<Project>({
      method: 'PATCH',
      path: `${this.baseUrl}/api/v1/projects/${encodeURIComponent(projectId)}`,
      body,
      idempotencyKey: key,
    });
  }

  listAuditEvents(params: AuditEventListParams, options?: RequestOptions): Promise<AuditEventPage> {
    return this.call<AuditEventPage>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/audit${query({
        project_id: params.project_id,
        action: params.action,
        cursor: params.cursor,
        limit: params.limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  listMembers(params: MemberListParams, options?: RequestOptions): Promise<MemberPage> {
    return this.call<MemberPage>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/members${query({
        organization_id: params.organization_id,
        cursor: params.cursor,
        limit: params.limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  inviteMember(body: MemberInvite, key: string): Promise<Member> {
    return this.call<Member>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/members`,
      body,
      idempotencyKey: key,
    });
  }

  updateMemberRole(membershipId: string, body: MemberUpdate, key: string): Promise<Member> {
    return this.call<Member>({
      method: 'PATCH',
      path: `${this.baseUrl}/api/v1/members/${encodeURIComponent(membershipId)}`,
      body,
      idempotencyKey: key,
    });
  }

  removeMember(membershipId: string): Promise<void> {
    return this.callEmpty({
      method: 'DELETE',
      path: `${this.baseUrl}/api/v1/members/${encodeURIComponent(membershipId)}`,
    });
  }

  listCredentials(params: CredentialListParams, options?: RequestOptions): Promise<CredentialPage> {
    return this.call<CredentialPage>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/credentials${query({
        organization_id: params.organization_id,
        cursor: params.cursor,
        limit: params.limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  createCredential(body: CredentialCreate, key: string): Promise<Credential> {
    return this.call<Credential>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/credentials`,
      body,
      idempotencyKey: key,
    });
  }

  rotateCredential(credentialId: string, body: CredentialRotate, key: string): Promise<Credential> {
    return this.call<Credential>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/credentials/${encodeURIComponent(credentialId)}/rotate`,
      body,
      idempotencyKey: key,
    });
  }

  revokeCredential(credentialId: string): Promise<void> {
    return this.callEmpty({
      method: 'DELETE',
      path: `${this.baseUrl}/api/v1/credentials/${encodeURIComponent(credentialId)}`,
    });
  }

  /**
   * listTools returns the tool catalog for the active organization. A
   * descriptor is catalog metadata only — risk, permission and sandbox flags —
   * so nothing here can ever be mistaken for something to execute.
   */
  listTools(options?: RequestOptions): Promise<ToolListResponse> {
    return this.call<ToolListResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/tools`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** listApprovals returns the organization's requests, newest first. */
  listApprovals(options?: RequestOptions): Promise<ApprovalListResponse> {
    return this.call<ApprovalListResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/approvals`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** createApproval records a request; it never executes the action. */
  createApproval(body: ApprovalCreate, key: string): Promise<Approval> {
    return this.call<Approval>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/approvals`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * decideApproval posts the human decision. The server owns the resulting
   * state: a request that was already decided or expired answers 409 with
   * `approval_not_pending`, which the caller must surface rather than retry.
   */
  decideApproval(approvalId: string, body: ApprovalDecision, key: string): Promise<Approval> {
    return this.call<Approval>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/approvals/${encodeURIComponent(approvalId)}/decide`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * searchWorkspace searches file contents; a `file:` prefix searches paths.
   * Hits carry line ranges so the evidence a reviewer sees is the evidence the
   * retriever used. The server owns the ranking and the index state, so an
   * empty result list is the answer, never something to fill in locally.
   */
  searchWorkspace(
    workspaceId: string,
    q: string,
    limit?: number,
    options?: RequestOptions,
  ): Promise<WorkspaceSearchResponse> {
    return this.call<WorkspaceSearchResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/workspaces/${encodeURIComponent(workspaceId)}/search${query({
        q,
        limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * listWorkspaceFiles lists the indexed files with their language summary and
   * content hashes, plus the notice that repository content is untrusted data.
   */
  listWorkspaceFiles(
    workspaceId: string,
    language?: string,
    limit?: number,
    options?: RequestOptions,
  ): Promise<WorkspaceFileListResponse> {
    return this.call<WorkspaceFileListResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/workspaces/${encodeURIComponent(workspaceId)}/files${query({
        language,
        limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** lookupWorkspaceSymbols finds declared symbols by name, with signatures. */
  lookupWorkspaceSymbols(
    workspaceId: string,
    name: string,
    limit?: number,
    options?: RequestOptions,
  ): Promise<WorkspaceSymbolLookupResponse> {
    return this.call<WorkspaceSymbolLookupResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/workspaces/${encodeURIComponent(workspaceId)}/symbols${query({
        name,
        limit,
      })}`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** getRunContext returns the manifest the run actually used, with provenance. */
  getRunContext(runId: string, options?: RequestOptions): Promise<RunContextResponse> {
    return this.call<RunContextResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/context`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * refreshRunContext rebuilds the context for a run inside the requested
   * token budget and records the manifest. The budget defaults to 32768 server
   * side; the UI never assumes the full model window. A 409 means the manifest
   * could not be built under those constraints, and the caller must surface the
   * server code instead of inventing a manifest.
   */
  refreshRunContext(
    runId: string,
    body: ContextRefresh,
    key: string,
  ): Promise<RunContextRefreshResponse> {
    return this.call<RunContextRefreshResponse>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/context/refresh`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * getPlan returns the latest plan revision for a run with its findings and
   * recorded approvals. A 404 answers `plan_not_found`: no plan has been stored
   * for the run yet, which the caller reports instead of showing a blank plan.
   */
  getPlan(runId: string, options?: RequestOptions): Promise<PlanView> {
    return this.call<PlanView>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/plan`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * savePlan stores a plan revision. The server verifies the referenced paths
   * and answers 422 `plan_verification_failed` with the offending finding
   * message, and 404 when the run does not exist; a refusal never produces a
   * locally invented revision.
   */
  savePlan(runId: string, body: PlanSubmission, key: string): Promise<PlanView> {
    return this.call<PlanView>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/plan`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * decidePlan records the human decision for one revision. The revision the
   * caller sends is the one the server last reported, so a stale decision
   * answers 409 `plan_conflict` and the caller reloads instead of guessing.
   */
  decidePlan(runId: string, body: PlanDecision, key: string): Promise<PlanView> {
    return this.call<PlanView>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/plan/approve`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * getRecovery returns the recovery state of a run: the journal, the attempt
   * history, the bounded budgets, the effect receipts and the known-good
   * checkpoints. Nothing here is derived locally, so the timeline an operator
   * reads is the one the server journalled.
   */
  getRecovery(runId: string, options?: RequestOptions): Promise<RecoveryView> {
    return this.call<RecoveryView>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/recovery`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * recoverRun plans the next bounded repair attempt. The server refuses with
   * 409 `recovery_loop_detected` when a loop is proven and with 429
   * `recovery_retry_exhausted` when the budget is spent; both carry the
   * evidence that stopped the run, which the UI shows instead of retrying.
   */
  recoverRun(runId: string, body: RecoverRequest, key: string): Promise<RecoverResponse> {
    return this.call<RecoverResponse>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/recover`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * rollbackRun restores the workspace to a known-good checkpoint. The effect
   * id makes the restore idempotent, so a replayed request answers the
   * recorded outcome with `duplicate: true` rather than writing the workspace
   * a second time.
   */
  rollbackRun(runId: string, body: RollbackRequest, key: string): Promise<RollbackResponse> {
    return this.call<RollbackResponse>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/rollback`,
      body,
      idempotencyKey: key,
    });
  }

  /**
   * openPullRequest composes the branch and opens one pull request. The
   * effect id makes the call exactly-once, so a replay answers with the
   * recorded pull request instead of opening a second one. A 400 carries the
   * push gate's reasons and a 409 the merge gate's.
   */
  openPullRequest(runId: string, body: OpenPullRequestBody, key: string): Promise<PullRequestView> {
    return this.call<PullRequestView>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/pr`,
      body,
      idempotencyKey: key,
      acceptStatuses: [200, 201],
    });
  }

  /**
   * listIssues reads the issues of one project with cursor pagination. The
   * cursor comes from the previous page's `next_cursor`.
   */
  listIssues(
    projectId: string,
    options?: { cursor?: string; limit?: number; signal?: AbortSignal },
  ): Promise<IssueListResponse> {
    return this.call<IssueListResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/issues${query({ project_id: projectId, cursor: options?.cursor, limit: options?.limit })}`,
      signal: options?.signal,
    });
  }

  /**
   * listRuns reads the runs of one project with cursor pagination. The state
   * filter is applied by the server, so a filtered page is the filtered set.
   */
  listRuns(
    projectId: string,
    options?: { cursor?: string; limit?: number; state?: string; signal?: AbortSignal },
  ): Promise<RunListResponse> {
    return this.call<RunListResponse>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/runs${query({
        project_id: projectId,
        cursor: options?.cursor,
        limit: options?.limit,
        state: options?.state,
      })}`,
      signal: options?.signal,
    });
  }

  /** getModelProvider reads the configured model provider status. */
  getModelProvider(options?: RequestOptions): Promise<ModelProviderStatus> {
    return this.call<ModelProviderStatus>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/model/provider`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** getDashboard reads the real aggregate metrics of the organization. */
  getDashboard(options?: RequestOptions): Promise<DashboardView> {
    return this.call<DashboardView>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/dashboard`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** getPreferences reads the stored view settings of the caller. */
  getPreferences(options?: RequestOptions): Promise<PreferencesView> {
    return this.call<PreferencesView>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/me/preferences`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /** savePreferences stores the view settings of the caller. */
  savePreferences(body: PreferencesSaveRequest, key: string): Promise<PreferencesView> {
    return this.call<PreferencesView>({
      method: 'PUT',
      path: `${this.baseUrl}/api/v1/me/preferences`,
      body,
      idempotencyKey: key,
    });
  }

  /** getPullRequest reads the tracked pull request of a run. */
  getPullRequest(runId: string, options?: RequestOptions): Promise<PullRequestView> {
    return this.call<PullRequestView>({
      method: 'GET',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/pr`,
      signal: options?.signal,
      timeoutMs: options?.timeoutMs,
      acceptStatuses: options?.acceptStatuses,
    });
  }

  /**
   * mergePullRequest merges the tracked pull request. The authorization must
   * name a permitted human: the server refuses a model or bot identity, an
   * empty one, and any merge whose required checks or approvals are missing.
   */
  mergePullRequest(runId: string, body: MergePullRequestBody, key: string): Promise<MergeResult> {
    return this.call<MergeResult>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/merge`,
      body,
      idempotencyKey: key,
    });
  }

  /** abandonPullRequest closes the pull request without merging. */
  abandonPullRequest(runId: string, body: AbandonBody, key: string): Promise<PullRequestView> {
    return this.call<PullRequestView>({
      method: 'POST',
      path: `${this.baseUrl}/api/v1/runs/${encodeURIComponent(runId)}/abandon`,
      body,
      idempotencyKey: key,
    });
  }
}

/**
 * createHealthClient builds the client used by the app. The base URL is empty
 * by default so the browser talks to the same origin that served the SPA.
 */
export function createHealthClient(baseUrl = ''): HealthClient {
  return new HealthClient(baseUrl);
}
