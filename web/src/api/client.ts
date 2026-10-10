import type { ApiErrorBody, LiveStatus, ReadinessReport, SystemInfo } from './types';

/** Request timeout for probe calls; readiness must stay responsive for operators. */
const DEFAULT_TIMEOUT_MS = 8000;

export interface ApiErrorInit {
  status: number;
  code: string;
  message: string;
  requestId: string;
  retryable: boolean;
}

/** ApiError carries the canonical server error envelope to the UI. */
export class ApiError extends Error implements ApiErrorInit {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;

  constructor(init: ApiErrorInit) {
    super(init.message);
    this.name = 'ApiError';
    this.status = init.status;
    this.code = init.code;
    this.requestId = init.requestId;
    this.retryable = init.retryable;
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
  });
}

export interface RequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  /** acceptStatuses are non-2xx statuses whose body is a successful payload. */
  acceptStatuses?: number[];
}

/** request performs one typed GET against the API and returns parsed JSON. */
async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timeout = AbortSignal.timeout(timeoutMs);
  const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;

  let response: Response;
  try {
    response = await fetch(path, {
      method: 'GET',
      headers: { Accept: 'application/json' },
      signal,
      referrerPolicy: 'no-referrer',
    });
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

  const accepted = options.acceptStatuses?.includes(response.status) ?? false;
  if (!response.ok && !accepted) {
    throw await parseError(response);
  }
  return (await response.json()) as T;
}

/**
 * HealthClient is the typed browser client for the health and system
 * endpoints. Every method maps 1:1 to an operation in api/openapi.yaml; the
 * drift check in CI fails when the contract changes without regenerating this
 * client's types.
 */
export class HealthClient {
  constructor(private readonly baseUrl: string = '') {}

  getLive(options?: RequestOptions): Promise<LiveStatus> {
    return request<LiveStatus>(`${this.baseUrl}/health/live`, options);
  }

  /**
   * getReady treats HTTP 503 as a successful call: the unavailable report is
   * the operator-facing payload. Any other failure still throws ApiError.
   */
  async getReady(options?: RequestOptions): Promise<ReadinessReport> {
    return request<ReadinessReport>(`${this.baseUrl}/health/ready`, {
      ...options,
      acceptStatuses: [503],
    });
  }

  getSystemInfo(options?: RequestOptions): Promise<SystemInfo> {
    return request<SystemInfo>(`${this.baseUrl}/api/v1/system/info`, options);
  }
}

/**
 * createHealthClient builds the client used by the app. The base URL is empty
 * by default so the browser talks to the same origin that served the SPA.
 */
export function createHealthClient(baseUrl = ''): HealthClient {
  return new HealthClient(baseUrl);
}
