import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';

import { ApiError, HealthClient, newIdempotencyKey } from './client';
import type {
  AuditEvent,
  AuditEventPage,
  LiveStatus,
  Organization,
  OrganizationPage,
  Project,
  ProjectPage,
  ReadinessReport,
  SystemInfo,
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

afterEach(() => {
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

describe('HealthClient dev subject', () => {
  it('sends X-Dev-Subject when configured', async () => {
    const fetchMock = stubFetch({ items: [], next_cursor: null } satisfies OrganizationPage);
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('', { devSubject: 'usr_dev_1' }).listOrganizations();

    expect(lastCall(fetchMock).init.headers).toMatchObject({ 'X-Dev-Subject': 'usr_dev_1' });
  });

  it('omits X-Dev-Subject when unconfigured, including on mutations', async () => {
    const fetchMock = stubFetch(organization, 201);
    vi.stubGlobal('fetch', fetchMock);

    await new HealthClient('').createOrganization({ name: 'Acme', slug: 'acme-eng' }, 'idem_key_9');

    const headers = lastCall(fetchMock).init.headers;
    const headerNames = Object.keys(headers ?? {});
    expect(headerNames).toContain('Idempotency-Key');
    expect(headerNames).not.toContain('X-Dev-Subject');
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
