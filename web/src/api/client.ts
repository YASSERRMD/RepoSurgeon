import type {
  ApiErrorBody,
  AuditEventPage,
  LiveStatus,
  Organization,
  OrganizationCreate,
  OrganizationPage,
  Project,
  ProjectCreate,
  ProjectPage,
  ProjectUpdate,
  ReadinessReport,
  SystemInfo,
} from './types';

/** Request timeout for probe calls; readiness must stay responsive for operators. */
const DEFAULT_TIMEOUT_MS = 8000;

/** Header names defined by the OpenAPI contract's `components/parameters`. */
const IDEMPOTENCY_KEY_HEADER = 'Idempotency-Key';
const DEV_SUBJECT_HEADER = 'X-Dev-Subject';

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

export interface RequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  /** acceptStatuses are non-2xx statuses whose body is a successful payload. */
  acceptStatuses?: number[];
}

export type HttpMethod = 'GET' | 'POST' | 'PATCH';

interface RawRequest {
  method: HttpMethod;
  path: string;
  body?: unknown;
  idempotencyKey?: string | undefined;
  devSubject?: string | undefined;
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

/** request performs one typed JSON call against the API and returns parsed JSON. */
async function request<T>(init: RawRequest): Promise<T> {
  const timeoutMs = init.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timeout = AbortSignal.timeout(timeoutMs);
  const signal = init.signal ? AbortSignal.any([init.signal, timeout]) : timeout;

  const headers: Record<string, string> = { Accept: 'application/json' };
  if (init.body !== undefined) headers['Content-Type'] = 'application/json';
  if (init.idempotencyKey !== undefined) {
    headers[IDEMPOTENCY_KEY_HEADER] = init.idempotencyKey;
  }
  if (init.devSubject !== undefined && init.devSubject !== '') {
    headers[DEV_SUBJECT_HEADER] = init.devSubject;
  }

  const requestInit: RequestInit = {
    method: init.method,
    headers,
    signal,
    referrerPolicy: 'no-referrer',
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
  return (await response.json()) as T;
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

export interface HealthClientOptions {
  /**
   * devSubject is sent as `X-Dev-Subject`. The API resolves the calling subject
   * from it until authentication lands in M02; it is only accepted outside
   * production, so it stays undefined unless explicitly configured.
   */
  devSubject?: string | undefined;
}

/**
 * HealthClient is the typed browser client for the health, system, organization,
 * project and audit endpoints. Every method maps 1:1 to an operation in
 * api/openapi.yaml; the drift check in CI fails when the contract changes
 * without regenerating this client's types.
 */
export class HealthClient {
  private readonly devSubject: string | undefined;

  constructor(
    private readonly baseUrl: string = '',
    options: HealthClientOptions = {},
  ) {
    this.devSubject = options.devSubject;
  }

  private call<T>(init: RawRequest): Promise<T> {
    return request<T>({ ...init, devSubject: this.devSubject });
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
}

/**
 * createHealthClient builds the client used by the app. The base URL is empty
 * by default so the browser talks to the same origin that served the SPA.
 */
export function createHealthClient(baseUrl = '', options: HealthClientOptions = {}): HealthClient {
  return new HealthClient(baseUrl, options);
}
