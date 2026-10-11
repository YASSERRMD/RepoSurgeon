import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';

import { ApiError, HealthClient, getCsrfToken, newIdempotencyKey, setCsrfToken } from './client';
import type {
  Approval,
  ApprovalCreate,
  ApprovalDecision,
  AuditEvent,
  AuditEventPage,
  ContextManifest,
  ContextRefresh,
  CurrentUser,
  Credential,
  CredentialPage,
  LiveStatus,
  Member,
  MemberPage,
  Organization,
  OrganizationPage,
  Project,
  ProjectPage,
  ReadinessReport,
  SessionResponse,
  SystemInfo,
  ToolDescriptor,
  WorkspaceFileListResponse,
  WorkspaceSearchResponse,
  WorkspaceSymbolLookupResponse,
} from './types';

const live: LiveStatus = { status: 'ok', uptime_seconds: 12.5 };
const system: SystemInfo = {
  name: 'reposurgeon',
  version: '0.1.0',
  git_commit: 'abc123',
  build_time: '2026-10-10T10:00:00Z',
  environment: 'development',
  go_version: 'go1.25.5',
  started_at: '2026-10-10T09:59:00Z',
  uptime_seconds: 60,
};
const ready: ReadinessReport = {
  status: 'ready',
  checks: [{ name: 'database', status: 'up', duration_ms: 2 }],
  generated_at: '2026-10-10T10:00:00Z',
};

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

function emptyResponse(status = 204): Response {
  return new Response(null, { status });
}

/** headersOf reads the request headers of a recorded call. */
function headersOf(init: RequestInit): Record<string, string> {
  return (init.headers ?? {}) as Record<string, string>;
}

beforeEach(() => {
  // The CSRF token lives in module memory; every test starts from no session.
  setCsrfToken(null);
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
});

describe('HealthClient', () => {
  it('fetches liveness and returns parsed payload', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(live));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getLive()).resolves.toEqual(live);
    expect(fetchMock).toHaveBeenCalledWith(
      '/health/live',
      expect.objectContaining({ method: 'GET' }),
    );
  });

  it('resolves readiness even on 503 because the report carries operator detail', async () => {
    const unavailable: ReadinessReport = {
      status: 'unavailable',
      checks: [{ name: 'database', status: 'down', detail: 'unreachable', duration_ms: 5 }],
      generated_at: '2026-10-10T10:00:00Z',
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(unavailable, { status: 503 }));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getReady()).resolves.toEqual(unavailable);
  });

  it('converts the canonical error envelope into ApiError', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          error: {
            code: 'not_found',
            message: 'resource not found',
            request_id: 'req_123',
          },
        },
        { status: 404 },
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('/base');
    await expect(client.getSystemInfo()).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      code: 'not_found',
      requestId: 'req_123',
      retryable: false,
    });
  });

  it('classifies 503 responses as retryable', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        { error: { code: 'internal_error', message: 'boom', request_id: 'req_9' } },
        {
          status: 503,
        },
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const error = await client.getLive().catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).retryable).toBe(true);
  });

  it('maps network failures to a retryable network_error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('ECONNREFUSED')));

    const client = new HealthClient('');
    const error = await client.getReady().catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network_error');
    expect((error as ApiError).retryable).toBe(true);
    expect((error as ApiError).message).toContain('ECONNREFUSED');
  });

  it('falls back to the X-Request-ID header when the body has none', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('{}', {
          status: 500,
          headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_header' },
        }),
      ),
    );

    const client = new HealthClient('');
    const error = await client.getLive().catch((err: unknown) => err);
    expect((error as ApiError).requestId).toBe('req_header');
  });

  it('honours a caller-provided abort signal', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn().mockImplementation((_input: string, init?: RequestInit) => {
      expect(init?.signal).toBeInstanceOf(AbortSignal);
      controller.abort();
      return Promise.resolve(jsonResponse(ready));
    });
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await client.getReady({ signal: controller.signal });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('uses the configured base URL prefix', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(system));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('https://api.example.com');
    await client.getSystemInfo();
    expect(fetchMock).toHaveBeenCalledWith(
      'https://api.example.com/api/v1/system/info',
      expect.objectContaining({ method: 'GET' }),
    );
  });
});

const organization: Organization = {
  id: 'org_2f8s8k4m2c9v0a1b3d5e7f9g1h',
  slug: 'acme-engineering',
  name: 'Acme Engineering',
  role: 'owner',
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T10:00:00Z',
};

const project: Project = {
  id: 'prj_7h2k9m4q8w3e6r1t5y0u2i4o6p',
  organization_id: organization.id,
  name: 'billing-service',
  description: 'Payments and invoicing',
  version: 3,
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T11:30:00Z',
};

const auditEvent: AuditEvent = {
  id: 'aud_9p0o8i7u6y5t4r3e2w1q',
  organization_id: organization.id,
  project_id: project.id,
  actor_id: 'usr_1a2b3c4d5e6f7g8h9i0j',
  action: 'project.created',
  entity_type: 'project',
  entity_id: project.id,
  request_id: 'req_4f2a1b3c5d6e7f8a9b0c',
  payload: { name: project.name },
  sequence: 12,
  created_at: '2026-10-10T10:00:00Z',
};

/** stubFetch records the method, headers and body of every call it serves. */
function stubFetch(body: unknown, status = 200): Mock {
  return vi.fn((): Promise<Response> => Promise.resolve(jsonResponse(body, { status })));
}

function lastCall(fetchMock: Mock): { url: string; init: RequestInit } {
  const call = fetchMock.mock.calls[0];
  expect(call).toBeDefined();
  return { url: String(call?.[0]), init: (call?.[1] ?? {}) as RequestInit };
}

describe('HealthClient organizations', () => {
  it('lists organizations without query noise when no page is requested', async () => {
    const page: OrganizationPage = { items: [organization], next_cursor: null };
    const fetchMock = stubFetch(page);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.listOrganizations()).resolves.toEqual(page);
    expect(lastCall(fetchMock).url).toBe('/api/v1/organizations');
  });

  it('sends cursor and limit as query parameters', async () => {
    const fetchMock = stubFetch({ items: [], next_cursor: null } satisfies OrganizationPage);
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('/base').listOrganizations({ cursor: 'c_1', limit: 50 });

    const { url } = lastCall(fetchMock);
    expect(url).toBe('/base/api/v1/organizations?cursor=c_1&limit=50');
  });

  it('creates an organization with an Idempotency-Key', async () => {
    const fetchMock = stubFetch(organization, 201);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(
      client.createOrganization(
        { name: 'Acme Engineering', slug: 'acme-engineering' },
        'idem_key_1',
      ),
    ).resolves.toEqual(organization);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/organizations');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      name: 'Acme Engineering',
      slug: 'acme-engineering',
    });
    expect(init.headers).toMatchObject({
      'Content-Type': 'application/json',
      'Idempotency-Key': 'idem_key_1',
    });
  });

  it('maps a duplicate slug conflict to the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'duplicate_name',
            message: 'slug already in use',
            request_id: 'req_1',
            details: { code: 'duplicate_name', current_version: 0 },
          },
        },
        409,
      ),
    );

    const client = new HealthClient('');
    const error = await client
      .createOrganization({ name: 'Acme', slug: 'acme-engineering' }, 'idem_key_1')
      .catch((err: unknown) => err);

    expect((error as ApiError).code).toBe('duplicate_name');
    expect((error as ApiError).status).toBe(409);
    expect((error as ApiError).retryable).toBe(false);
  });
});

describe('HealthClient projects', () => {
  it('lists projects with the required organization id and filters', async () => {
    const page: ProjectPage = { items: [project], next_cursor: 'cursor_2' };
    const fetchMock = stubFetch(page);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(
      client.listProjects({ organization_id: organization.id, q: 'billing', limit: 10 }),
    ).resolves.toEqual(page);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/projects?organization_id=${organization.id}&q=billing&limit=10`);
    expect(init.method).toBe('GET');
  });

  it('creates a project with an Idempotency-Key', async () => {
    const fetchMock = stubFetch(project, 201);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const created = await client.createProject(
      { organization_id: organization.id, name: 'billing-service', description: 'Payments' },
      'idem_key_2',
    );

    expect(created).toEqual(project);
    const { init } = lastCall(fetchMock);
    expect(init.method).toBe('POST');
    expect(init.headers).toMatchObject({
      Accept: 'application/json',
      'Idempotency-Key': 'idem_key_2',
    });
  });

  it('gets a project by id', async () => {
    const fetchMock = stubFetch(project);
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('').getProject(project.id);

    expect(lastCall(fetchMock).url).toBe(`/api/v1/projects/${project.id}`);
  });

  it('sends the read version on PATCH', async () => {
    const updated = { ...project, version: 4, description: 'Payments, invoicing and refunds' };
    const fetchMock = stubFetch(updated);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const result = await client.updateProject(
      project.id,
      { version: 3, description: 'Payments, invoicing and refunds', settings: { region: 'eu' } },
      'idem_key_3',
    );

    expect(result.version).toBe(4);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/projects/${project.id}`);
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(String(init.body))).toEqual({
      version: 3,
      description: 'Payments, invoicing and refunds',
      settings: { region: 'eu' },
    });
    expect(init.headers).toMatchObject({ 'Idempotency-Key': 'idem_key_3' });
  });

  it('maps a 409 version conflict to ApiError with the current version', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'version_mismatch',
            message: 'project was modified by someone else',
            request_id: 'req_9',
            details: { current_version: 4 },
          },
        },
        409,
      ),
    );

    const client = new HealthClient('');
    const error = await client
      .updateProject(project.id, { version: 2, description: 'stale' }, 'idem_key_4')
      .catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(409);
    expect(apiError.code).toBe('version_mismatch');
    expect(apiError.retryable).toBe(false);
    expect(apiError.details).toEqual({ current_version: 4 });
    expect(apiError.currentVersion()).toBe(4);
  });

  it('maps 403 permission errors', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'permission_denied',
            message: 'you do not have access to this organization',
            request_id: 'req_5',
          },
        },
        403,
      ),
    );

    const client = new HealthClient('');
    const error = await client
      .listProjects({ organization_id: 'org_other' })
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(403);
    expect((error as ApiError).code).toBe('permission_denied');
    expect((error as ApiError).retryable).toBe(false);
  });

  it('maps 404 not found errors', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        { error: { code: 'not_found', message: 'resource not found', request_id: 'req_6' } },
        404,
      ),
    );

    const client = new HealthClient('');
    const error = await client.getProject('prj_missing').catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(404);
    expect((error as ApiError).code).toBe('not_found');
    expect((error as ApiError).requestId).toBe('req_6');
  });
});

describe('HealthClient audit', () => {
  it('lists audit events for a project with an action filter', async () => {
    const page: AuditEventPage = { items: [auditEvent], next_cursor: null };
    const fetchMock = stubFetch(page);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(
      client.listAuditEvents({ project_id: project.id, action: 'project.created', limit: 5 }),
    ).resolves.toEqual(page);

    expect(lastCall(fetchMock).url).toBe(
      `/api/v1/audit?project_id=${project.id}&action=project.created&limit=5`,
    );
  });
});

const session: SessionResponse = {
  user: {
    id: 'usr_1a2b3c4d5e6f7g8h9i0j',
    email: 'developer@example.com',
    display_name: 'Developer',
  },
  csrf_token: 'csrf_9f8e7d6c5b4a',
  expires_at: '2026-10-11T10:00:00Z',
};

const currentUser: CurrentUser = {
  ...session.user,
  organizations: [
    {
      id: organization.id,
      name: organization.name,
      slug: organization.slug,
      role: 'owner',
      permissions: ['project.read', 'project.write', 'member.admin', 'credential.admin'],
    },
  ],
};

const member: Member = {
  id: 'mem_1a2b3c4d5e6f7g8h9i0j',
  user_id: 'usr_1a2b3c4d5e6f7g8h9i0j',
  email: 'developer@example.com',
  display_name: 'Developer',
  role: 'owner',
  created_at: '2026-10-10T10:00:00Z',
};

/** The contract exposes metadata only: `value` is write-only and never returned. */
const credential: Credential = {
  id: 'crd_1a2b3c4d5e6f7g8h9i0j',
  organization_id: organization.id,
  kind: 'stepfun',
  label: 'production keys',
  status: 'active',
  key_version: 2,
  fingerprint: 'a1b2c3d4e5f60718a9b0c1d2e3f40516',
  last_verified_at: null,
  created_at: '2026-10-10T10:00:00Z',
};

const CREDENTIAL_METADATA_KEYS = [
  'created_at',
  'fingerprint',
  'id',
  'key_version',
  'kind',
  'label',
  'last_verified_at',
  'organization_id',
  'status',
];

describe('HealthClient auth', () => {
  it('signs in with the session cookie, email and password', async () => {
    const fetchMock = stubFetch(session);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(
      client.login({
        email: 'developer@example.com',
        password: 'correct-horse-battery-staple',
      }),
    ).resolves.toEqual(session);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/auth/login');
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('include');
    expect(init.referrerPolicy).toBe('no-referrer');
    expect(JSON.parse(String(init.body))).toEqual({
      email: 'developer@example.com',
      password: 'correct-horse-battery-staple',
    });
    expect(headersOf(init)).toMatchObject({
      Accept: 'application/json',
      'Content-Type': 'application/json',
    });
    // Login happens before a session exists, so no CSRF token is attached.
    expect(headersOf(init)['X-CSRF-Token']).toBeUndefined();
  });

  it('exposes the returned CSRF token in memory only', async () => {
    vi.stubGlobal('fetch', stubFetch(session));

    await new HealthClient('').login({
      email: 'developer@example.com',
      password: 'correct-horse-battery-staple',
    });

    expect(getCsrfToken()).toBe('csrf_9f8e7d6c5b4a');
    setCsrfToken('rotated_csrf_token');
    expect(getCsrfToken()).toBe('rotated_csrf_token');
    setCsrfToken(null);
    expect(getCsrfToken()).toBeNull();
    setCsrfToken('');
    expect(getCsrfToken()).toBeNull();
  });

  it('maps 401 login failures to invalid_credentials', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'invalid_credentials',
            message: 'email or password is incorrect',
            request_id: 'req_0123456789abcdef',
          },
        },
        401,
      ),
    );

    const error = await new HealthClient('')
      .login({ email: 'developer@example.com', password: 'wrong-password-here' })
      .catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(401);
    expect(apiError.code).toBe('invalid_credentials');
    expect(apiError.retryable).toBe(false);
  });

  it('maps 429 to rate_limited and marks it retryable', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'rate_limited',
            message: 'too many failed login attempts; try again later',
            request_id: 'req_0123456789abcdef',
          },
        },
        429,
      ),
    );

    const error = await new HealthClient('')
      .login({ email: 'developer@example.com', password: 'correct-horse-battery-staple' })
      .catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(429);
    expect(apiError.code).toBe('rate_limited');
    expect(apiError.retryable).toBe(true);
  });

  it('sends the CSRF token and cookie on logout', async () => {
    setCsrfToken('csrf_keep_me');
    const fetchMock = vi.fn().mockResolvedValue(emptyResponse(204));
    vi.stubGlobal('fetch', fetchMock);

    await expect(new HealthClient('').logout()).resolves.toBeUndefined();

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/auth/logout');
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('include');
    expect(headersOf(init)).toMatchObject({ 'X-CSRF-Token': 'csrf_keep_me' });
    expect(init.body).toBeUndefined();
  });

  it('rotates the session with the CSRF token and returns the new one', async () => {
    setCsrfToken('csrf_before_rotate');
    const rotated: SessionResponse = {
      ...session,
      csrf_token: 'csrf_after_rotate',
    };
    const fetchMock = stubFetch(rotated);
    vi.stubGlobal('fetch', fetchMock);

    const result = await new HealthClient('/base').refreshSession();

    expect(result.csrf_token).toBe('csrf_after_rotate');
    expect(lastCall(fetchMock).url).toBe('/base/api/v1/auth/refresh');
    expect(headersOf(lastCall(fetchMock).init)).toMatchObject({
      'X-CSRF-Token': 'csrf_before_rotate',
    });
  });

  it('reads the current user with roles and permissions', async () => {
    const fetchMock = stubFetch(currentUser);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getCurrentUser()).resolves.toEqual(currentUser);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/me');
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('include');
    expect(headersOf(init)['X-CSRF-Token']).toBeUndefined();
  });

  it('maps a session that no longer exists to unauthenticated', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'unauthenticated',
            message: 'authentication is required',
            request_id: 'r_1',
          },
        },
        401,
      ),
    );

    const error = await new HealthClient('').getCurrentUser().catch((err: unknown) => err);
    expect((error as ApiError).code).toBe('unauthenticated');
    expect((error as ApiError).status).toBe(401);
  });
});

describe('HealthClient members', () => {
  it('lists members of an organization', async () => {
    const page: MemberPage = { items: [member], next_cursor: null };
    const fetchMock = stubFetch(page);
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      new HealthClient('').listMembers({ organization_id: organization.id }),
    ).resolves.toEqual(page);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/members?organization_id=${organization.id}`);
    expect(init.method).toBe('GET');
  });

  it('invites a member with an Idempotency-Key, the CSRF token and the role', async () => {
    setCsrfToken('csrf_invite');
    const fetchMock = stubFetch(member, 201);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const invited = await client.inviteMember(
      { organization_id: organization.id, email: 'teammate@example.com', role: 'maintainer' },
      'idem_invite_1',
    );

    expect(invited).toEqual(member);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/members');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: organization.id,
      email: 'teammate@example.com',
      role: 'maintainer',
    });
    expect(headersOf(init)).toMatchObject({
      'Idempotency-Key': 'idem_invite_1',
      'X-CSRF-Token': 'csrf_invite',
    });
  });

  it('updates a role through PATCH on the membership id', async () => {
    setCsrfToken('csrf_role');
    const updated: Member = { ...member, role: 'reviewer' };
    const fetchMock = stubFetch(updated);
    vi.stubGlobal('fetch', fetchMock);

    const result = await new HealthClient('').updateMemberRole(
      member.id,
      { role: 'reviewer' },
      'idem_role_1',
    );

    expect(result.role).toBe('reviewer');
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/members/${member.id}`);
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(String(init.body))).toEqual({ role: 'reviewer' });
    expect(headersOf(init)).toMatchObject({
      'Idempotency-Key': 'idem_role_1',
      'X-CSRF-Token': 'csrf_role',
    });
  });

  it('removes a member with DELETE and no body', async () => {
    setCsrfToken('csrf_remove');
    const fetchMock = vi.fn().mockResolvedValue(emptyResponse(204));
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('').removeMember(member.id);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/members/${member.id}`);
    expect(init.method).toBe('DELETE');
    expect(init.body).toBeUndefined();
    expect(headersOf(init)).toMatchObject({ 'X-CSRF-Token': 'csrf_remove' });
  });

  it('maps 409 conflicts to the server rejection message', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'conflict',
            message: 'the last owner cannot be removed',
            request_id: 'req_owner',
          },
        },
        409,
      ),
    );

    const error = await new HealthClient('').removeMember(member.id).catch((err: unknown) => err);
    expect((error as ApiError).status).toBe(409);
    expect((error as ApiError).message).toBe('the last owner cannot be removed');
    expect((error as ApiError).retryable).toBe(false);
  });
});

describe('HealthClient credentials', () => {
  it('lists credential metadata and never a value field', async () => {
    const page: CredentialPage = { items: [credential], next_cursor: null };
    const fetchMock = stubFetch(page);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const result = await client.listCredentials({ organization_id: organization.id });

    expect(result.items).toHaveLength(1);
    expect(Object.keys(result.items[0]).sort()).toEqual(CREDENTIAL_METADATA_KEYS);
    expect(Object.hasOwn(result.items[0], 'value')).toBe(false);
    expect(Object.hasOwn(result.items[0], 'secret')).toBe(false);
    expect(lastCall(fetchMock).url).toBe(`/api/v1/credentials?organization_id=${organization.id}`);
  });

  it('creates a credential with the value in the request body only', async () => {
    setCsrfToken('csrf_create');
    const fetchMock = stubFetch(credential, 201);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const created = await client.createCredential(
      {
        organization_id: organization.id,
        kind: 'stepfun',
        label: 'production keys',
        value: 'sf-live-xxxxxxxxxxxxxxxx',
      },
      'idem_credential_1',
    );

    expect(created).toEqual(credential);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/credentials');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: organization.id,
      kind: 'stepfun',
      label: 'production keys',
      value: 'sf-live-xxxxxxxxxxxxxxxx',
    });
    expect(headersOf(init)).toMatchObject({
      'Idempotency-Key': 'idem_credential_1',
      'X-CSRF-Token': 'csrf_create',
    });
  });

  it('rotates a credential at the rotate path', async () => {
    setCsrfToken('csrf_rotate');
    const rotated: Credential = { ...credential, key_version: 3 };
    const fetchMock = stubFetch(rotated);
    vi.stubGlobal('fetch', fetchMock);

    const result = await new HealthClient('').rotateCredential(
      credential.id,
      { value: 'sf-live-yyyyyyyyyyyyyyyy' },
      'idem_rotate_1',
    );

    expect(result.key_version).toBe(3);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/credentials/${credential.id}/rotate`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({ value: 'sf-live-yyyyyyyyyyyyyyyy' });
    expect(headersOf(init)).toMatchObject({
      'Idempotency-Key': 'idem_rotate_1',
      'X-CSRF-Token': 'csrf_rotate',
    });
  });

  it('revokes a credential with DELETE', async () => {
    setCsrfToken('csrf_revoke');
    const fetchMock = vi.fn().mockResolvedValue(emptyResponse(204));
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('').revokeCredential(credential.id);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/credentials/${credential.id}`);
    expect(init.method).toBe('DELETE');
    expect(headersOf(init)).toMatchObject({ 'X-CSRF-Token': 'csrf_revoke' });
  });
});

const toolDescriptors: ToolDescriptor[] = [
  {
    name: 'read_file',
    description: 'Read a file inside the workspace',
    risk: 'read_only',
    permission: 'project.read',
    approval: false,
    sandboxed: false,
    permitted: true,
  },
  {
    name: 'run_tests',
    description: 'Run the project test suite in the isolated runner',
    risk: 'sandboxed_exec',
    permission: 'run.execute',
    approval: false,
    sandboxed: true,
    permitted: true,
  },
  {
    name: 'merge_pr',
    description: 'Merge a reviewed pull request on GitHub',
    risk: 'external_side_effect',
    permission: 'merge.execute',
    approval: true,
    sandboxed: false,
    permitted: false,
  },
];

/** The contract exposes catalog metadata only: nothing here is executable. */
const TOOL_DESCRIPTOR_KEYS = [
  'approval',
  'description',
  'name',
  'permission',
  'permitted',
  'risk',
  'sandboxed',
];

const pendingApproval: Approval = {
  id: 'apr_1a2b3c4d5e6f7g8h9i0j',
  organization_id: organization.id,
  project_id: project.id,
  run_id: 'run_1a2b3c4d5e6f7g8h9i0j',
  action: 'merge_pr',
  reason: 'required checks are green and the diff is reviewed',
  state: 'pending',
  requested_by: 'usr_1a2b3c4d5e6f7g8h9i0j',
  expires_at: '2026-10-11T10:00:00Z',
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T10:00:00Z',
};

const approvedApproval: Approval = {
  ...pendingApproval,
  state: 'approved',
  decided_by: 'usr_9i8h7g6f5e4d3c2b1a0z',
  decision_reason: 'verified the diff and the test evidence',
  updated_at: '2026-10-10T11:00:00Z',
};

describe('HealthClient tools', () => {
  it('lists the tool catalog with catalog metadata only', async () => {
    const fetchMock = stubFetch({ items: toolDescriptors });
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const result = await client.listTools();

    expect(result.items).toHaveLength(3);
    expect(Object.keys(result.items[0]).sort()).toEqual(TOOL_DESCRIPTOR_KEYS);
    // A descriptor never carries an executable payload or a secret.
    for (const item of result.items) {
      expect(Object.hasOwn(item, 'command')).toBe(false);
      expect(Object.hasOwn(item, 'args')).toBe(false);
      expect(Object.hasOwn(item, 'value')).toBe(false);
      expect(Object.hasOwn(item, 'secret')).toBe(false);
      expect(Object.hasOwn(item, 'script')).toBe(false);
    }

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/tools');
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('include');
    expect(headersOf(init)['X-CSRF-Token']).toBeUndefined();
  });

  it('maps 403 to permission_denied for a caller without the tool permission', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'permission_denied',
            message: 'you do not have access to this organization',
            request_id: 'req_tools',
          },
        },
        403,
      ),
    );

    const error = await new HealthClient('').listTools().catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(403);
    expect((error as ApiError).code).toBe('permission_denied');
    expect((error as ApiError).retryable).toBe(false);
  });
});

describe('HealthClient approvals', () => {
  it('lists approval requests for the organization', async () => {
    const fetchMock = stubFetch({ items: [approvedApproval, pendingApproval], next_cursor: null });
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.listApprovals()).resolves.toEqual({
      items: [approvedApproval, pendingApproval],
      next_cursor: null,
    });

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/approvals');
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('include');
  });

  it('records a request with an Idempotency-Key and the CSRF token', async () => {
    setCsrfToken('csrf_request');
    const fetchMock = stubFetch(pendingApproval, 201);
    vi.stubGlobal('fetch', fetchMock);

    const body: ApprovalCreate = {
      run_id: 'run_1a2b3c4d5e6f7g8h9i0j',
      action: 'merge_pr',
      reason: 'required checks are green and the diff is reviewed',
    };
    const created = await new HealthClient('').createApproval(body, 'idem_approval_1');

    expect(created).toEqual(pendingApproval);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe('/api/v1/approvals');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      run_id: 'run_1a2b3c4d5e6f7g8h9i0j',
      action: 'merge_pr',
      reason: 'required checks are green and the diff is reviewed',
    });
    expect(headersOf(init)).toMatchObject({
      'Content-Type': 'application/json',
      'Idempotency-Key': 'idem_approval_1',
      'X-CSRF-Token': 'csrf_request',
    });
  });

  it('decides a request at the decide path and returns the server state', async () => {
    setCsrfToken('csrf_decide');
    const fetchMock = stubFetch(approvedApproval);
    vi.stubGlobal('fetch', fetchMock);

    const body: ApprovalDecision = {
      decision: 'approve',
      reason: 'verified the diff and the test evidence',
    };
    const result = await new HealthClient('').decideApproval(
      pendingApproval.id,
      body,
      'idem_decide',
    );

    expect(result.state).toBe('approved');
    expect(result.decided_by).toBe('usr_9i8h7g6f5e4d3c2b1a0z');
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/approvals/${pendingApproval.id}/decide`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      decision: 'approve',
      reason: 'verified the diff and the test evidence',
    });
    expect(headersOf(init)).toMatchObject({
      'Idempotency-Key': 'idem_decide',
      'X-CSRF-Token': 'csrf_decide',
    });
  });

  it('maps 409 to approval_not_pending', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'approval_not_pending',
            message: 'approval is not pending',
            request_id: 'req_0123456789abcdef',
          },
        },
        409,
      ),
    );

    const error = await new HealthClient('')
      .decideApproval(pendingApproval.id, { decision: 'approve' }, 'idem_decide_again')
      .catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(409);
    expect(apiError.code).toBe('approval_not_pending');
    expect(apiError.message).toBe('approval is not pending');
    expect(apiError.retryable).toBe(false);
  });

  it('maps 403 to permission_denied when the role cannot decide', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'permission_denied',
            message: 'approval.decide is required to decide a request',
            request_id: 'req_decide_403',
          },
        },
        403,
      ),
    );

    const error = await new HealthClient('')
      .decideApproval(pendingApproval.id, { decision: 'reject' }, 'idem_decide_403')
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(403);
    expect((error as ApiError).code).toBe('permission_denied');
    expect((error as ApiError).retryable).toBe(false);
  });

  it('maps 404 to not_found for an unknown approval', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        { error: { code: 'not_found', message: 'approval not found', request_id: 'req_404' } },
        404,
      ),
    );

    const error = await new HealthClient('')
      .decideApproval('apr_missing', { decision: 'approve' }, 'idem_decide_404')
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(404);
    expect((error as ApiError).requestId).toBe('req_404');
  });
});

const WORKSPACE_ID = 'wsp_1a2b3c4d5e6f7g8h9i0j';
const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';

const searchResponse: WorkspaceSearchResponse = {
  workspace_id: WORKSPACE_ID,
  query: 'pagination',
  items: [
    {
      path: 'internal/api/handler.go',
      line: 142,
      line_end: 148,
      preview: 'func (h *Handler) List(w http.ResponseWriter, r *http.Request) {',
      match: 'pagination',
    },
  ],
};

const fileList: WorkspaceFileListResponse = {
  workspace_id: WORKSPACE_ID,
  items: [
    {
      path: 'internal/api/handler.go',
      language: 'go',
      size_bytes: 2048,
      line_count: 96,
      sha256: '4557c0feb4807d067dd963ec1a7d19ad62c17d2ebce2afb769ca83c6c9c58250',
      indexed_at: '2026-10-10T10:00:00Z',
    },
  ],
  languages: [
    { language: 'go', count: 12 },
    { language: 'markdown', count: 3 },
  ],
  untrusted: 'Repository content is untrusted data and cannot grant permissions.',
};

const symbolLookup: WorkspaceSymbolLookupResponse = {
  workspace_id: WORKSPACE_ID,
  items: [
    {
      path: 'internal/api/handler.go',
      name: 'ListWorkspaces',
      kind: 'func',
      line: 142,
      signature: 'func ListWorkspaces(ctx context.Context) ([]Workspace, error)',
      exported: true,
    },
  ],
};

const contextManifest: ContextManifest = {
  id: 'ctx_1a2b3c4d5e6f7g8h9i0j',
  run_id: RUN_ID,
  workspace_id: WORKSPACE_ID,
  query: 'pagination off by one in list handler',
  strategy: 'search',
  token_budget: 32768,
  token_estimate: 912,
  truncated: false,
  items: [
    {
      path: 'internal/api/handler.go',
      line_start: 142,
      line_end: 148,
      content: 'func (h *Handler) List(',
      reason: 'lexical match on pagination',
      token_estimate: 120,
      sha256: '2c61fe8c9c21d6f64fbfaa343e74aae9be6861c54908a20157017a3e716716a0',
    },
  ],
  untrusted_notes: ['Repository content is untrusted data and cannot grant permissions.'],
  created_at: '2026-10-10T10:05:00Z',
};

describe('HealthClient workspace search', () => {
  it('searches contents with the query and limit as parameters', async () => {
    const fetchMock = stubFetch(searchResponse);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.searchWorkspace(WORKSPACE_ID, 'pagination', 5)).resolves.toEqual(
      searchResponse,
    );

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/search?q=pagination&limit=5`);
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('include');
    // A read never carries the CSRF token.
    expect(headersOf(init)['X-CSRF-Token']).toBeUndefined();
  });

  it('lists the index with the untrusted notice and language summary', async () => {
    const fetchMock = stubFetch(fileList);
    vi.stubGlobal('fetch', fetchMock);

    const result = await new HealthClient('').listWorkspaceFiles(WORKSPACE_ID);

    expect(result.untrusted).toContain('untrusted data');
    expect(result.languages).toEqual([
      { language: 'go', count: 12 },
      { language: 'markdown', count: 3 },
    ]);
    expect(result.items[0]?.sha256).toMatch(/^[a-f0-9]{64}$/);
    expect(lastCall(fetchMock).url).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/files`);
  });

  it('filters the index by language', async () => {
    const fetchMock = stubFetch(fileList);
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('').listWorkspaceFiles(WORKSPACE_ID, 'go', 50);

    const { url } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/files?language=go&limit=50`);
  });

  it('looks up symbols by name', async () => {
    const fetchMock = stubFetch(symbolLookup);
    vi.stubGlobal('fetch', fetchMock);

    const result = await new HealthClient('').lookupWorkspaceSymbols(
      WORKSPACE_ID,
      'ListWorkspaces',
    );

    expect(result.items[0]?.kind).toBe('func');
    expect(result.items[0]?.exported).toBe(true);
    expect(lastCall(fetchMock).url).toBe(
      `/api/v1/workspaces/${WORKSPACE_ID}/symbols?name=ListWorkspaces`,
    );
  });

  it('maps 400, 403 and 404 to the server code for search', async () => {
    for (const failure of [
      { status: 400, code: 'invalid_query', message: 'q is required' },
      { status: 403, code: 'permission_denied', message: 'run.execute is required' },
      { status: 404, code: 'not_found', message: 'workspace not found' },
    ]) {
      vi.stubGlobal(
        'fetch',
        stubFetch(
          { error: { code: failure.code, message: failure.message, request_id: 'req_search' } },
          failure.status,
        ),
      );

      const error = await new HealthClient('')
        .searchWorkspace(WORKSPACE_ID, 'pagination')
        .catch((err: unknown) => err);

      expect(error).toBeInstanceOf(ApiError);
      const apiError = error as ApiError;
      expect(apiError.status).toBe(failure.status);
      expect(apiError.code).toBe(failure.code);
      expect(apiError.requestId).toBe('req_search');
    }
  });

  it('maps 404 for an unknown workspace file index', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        { error: { code: 'not_found', message: 'workspace not found', request_id: 'req_files' } },
        404,
      ),
    );

    const error = await new HealthClient('')
      .listWorkspaceFiles(WORKSPACE_ID)
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(404);
    expect((error as ApiError).code).toBe('not_found');
    expect((error as ApiError).retryable).toBe(false);
  });

  it('maps 400 for a missing symbol name', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        { error: { code: 'invalid_request', message: 'name is required', request_id: 'req_sym' } },
        400,
      ),
    );

    const error = await new HealthClient('')
      .lookupWorkspaceSymbols(WORKSPACE_ID, 'ListWorkspaces')
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(400);
    expect((error as ApiError).code).toBe('invalid_request');
  });
});

describe('HealthClient run context', () => {
  it('reads the manifest with its provenance', async () => {
    const fetchMock = stubFetch(contextManifest);
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getRunContext(RUN_ID)).resolves.toEqual(contextManifest);

    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/runs/${RUN_ID}/context`);
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('include');
    expect(headersOf(init)['X-CSRF-Token']).toBeUndefined();
  });

  it('maps 404 when the run has no manifest yet', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        { error: { code: 'not_found', message: 'run has no context', request_id: 'req_ctx_404' } },
        404,
      ),
    );

    const error = await new HealthClient('').getRunContext(RUN_ID).catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(404);
    expect(apiError.code).toBe('not_found');
    expect(apiError.requestId).toBe('req_ctx_404');
  });

  it('rebuilds the context with an idempotency key, the CSRF token and the exact body', async () => {
    setCsrfToken('csrf_rebuild');
    const fetchMock = stubFetch(contextManifest, 201);
    vi.stubGlobal('fetch', fetchMock);

    const body: ContextRefresh = {
      workspace_id: WORKSPACE_ID,
      query: 'pagination off by one in list handler',
      token_budget: 131072,
      expand: true,
    };
    const rebuilt = await new HealthClient('').refreshRunContext(RUN_ID, body, 'idem_rebuild_1');

    expect(rebuilt).toEqual(contextManifest);
    const { url, init } = lastCall(fetchMock);
    expect(url).toBe(`/api/v1/runs/${RUN_ID}/context/refresh`);
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('include');
    expect(JSON.parse(String(init.body))).toEqual({
      workspace_id: WORKSPACE_ID,
      query: 'pagination off by one in list handler',
      token_budget: 131072,
      expand: true,
    });
    expect(headersOf(init)).toMatchObject({
      'Content-Type': 'application/json',
      'Idempotency-Key': 'idem_rebuild_1',
      'X-CSRF-Token': 'csrf_rebuild',
    });
  });

  it('maps 409 context_refresh_conflict to the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'context_refresh_conflict',
            message: 'a rebuild for this run is already in progress',
            request_id: 'req_ctx_409',
          },
        },
        409,
      ),
    );

    const error = await new HealthClient('')
      .refreshRunContext(
        RUN_ID,
        { workspace_id: WORKSPACE_ID, query: 'pagination' },
        'idem_rebuild_409',
      )
      .catch((err: unknown) => err);

    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(409);
    expect(apiError.code).toBe('context_refresh_conflict');
    expect(apiError.retryable).toBe(false);
  });

  it('maps 403 when the role may not rebuild the context', async () => {
    vi.stubGlobal(
      'fetch',
      stubFetch(
        {
          error: {
            code: 'permission_denied',
            message: 'run.execute is required to rebuild a run context',
            request_id: 'req_ctx_403',
          },
        },
        403,
      ),
    );

    const error = await new HealthClient('')
      .refreshRunContext(
        RUN_ID,
        { workspace_id: WORKSPACE_ID, query: 'pagination' },
        'idem_rebuild_403',
      )
      .catch((err: unknown) => err);

    expect((error as ApiError).status).toBe(403);
    expect((error as ApiError).code).toBe('permission_denied');
    expect((error as ApiError).retryable).toBe(false);
  });
});

describe('newIdempotencyKey', () => {
  it('produces unique keys within the contract bounds', () => {
    const keys = new Set(Array.from({ length: 100 }, () => newIdempotencyKey()));

    expect(keys.size).toBe(100);
    for (const key of keys) {
      expect(key.length).toBeGreaterThanOrEqual(8);
      expect(key.length).toBeLessThanOrEqual(128);
      expect(key).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    }
  });

  it('differs for two actions inside the same millisecond', () => {
    expect(newIdempotencyKey()).not.toBe(newIdempotencyKey());
  });
});
