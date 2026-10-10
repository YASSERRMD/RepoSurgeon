import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { useProjectsStore } from './projects';
import type { Project, ProjectPage } from '@/api/types';

const organizationId = 'org_acme';

const billing: Project = {
  id: 'prj_billing',
  organization_id: organizationId,
  name: 'billing-service',
  description: 'Payments and invoicing',
  version: 3,
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T11:30:00Z',
};

const search: Project = {
  ...billing,
  id: 'prj_search',
  name: 'search-index',
  description: 'Indexing pipeline',
  version: 1,
};

function page(items: Project[], next_cursor: string | null = null): ProjectPage {
  return { items, next_cursor };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorEnvelope(code: string, message: string, status: number, details?: unknown): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_1', details } }, status);
}

/** stubProjects serves GET /api/v1/projects, POST /api/v1/projects and PATCH /api/v1/projects/:id. */
function stubProjects(options: {
  list?: ProjectPage;
  detail?: Project;
  created?: Project;
  updated?: Project;
  failList?: boolean;
  failDetail?: boolean;
  conflictOnPatch?: boolean;
}): Mock {
  return vi.fn((input: string, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';

    if (url.includes('/api/v1/projects')) {
      const isCollection = !url.includes('/api/v1/projects/');
      if (isCollection) {
        if (method === 'POST') {
          return Promise.resolve(jsonResponse(options.created ?? billing, 201));
        }
        return options.failList
          ? Promise.reject(new Error('ECONNREFUSED'))
          : Promise.resolve(jsonResponse(options.list ?? page([billing])));
      }
      if (method === 'PATCH') {
        if (options.conflictOnPatch) {
          return Promise.resolve(
            errorEnvelope('version_mismatch', 'project was modified by someone else', 409, {
              current_version: 7,
            }),
          );
        }
        return Promise.resolve(jsonResponse(options.updated ?? { ...billing, version: 4 }));
      }
      return options.failDetail
        ? Promise.resolve(errorEnvelope('not_found', 'resource not found', 404))
        : Promise.resolve(jsonResponse(options.detail ?? billing));
    }
    return Promise.resolve(jsonResponse({ status: 'ok' }, 200));
  });
}

function callUrl(fetchMock: Mock, index = 0): string {
  return String(fetchMock.mock.calls[index]?.[0]);
}

function callInit(fetchMock: Mock, index = 0): RequestInit {
  return (fetchMock.mock.calls[index]?.[1] ?? {}) as RequestInit;
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('projects store', () => {
  it('loads the first page for an organization', async () => {
    const fetchMock = stubProjects({ list: page([billing, search], 'cursor_2') });
    vi.stubGlobal('fetch', fetchMock);
    const store = useProjectsStore();

    await store.load(organizationId);

    expect(store.items).toHaveLength(2);
    expect(store.nextCursor).toBe('cursor_2');
    expect(store.organizationId).toBe(organizationId);
    expect(store.error).toBeNull();
    expect(store.loading).toBe(false);
    expect(callUrl(fetchMock)).toBe(`/api/v1/projects?organization_id=${organizationId}`);
  });

  it('passes the search query through', async () => {
    const fetchMock = stubProjects({ list: page([search]) });
    vi.stubGlobal('fetch', fetchMock);
    const store = useProjectsStore();

    await store.load(organizationId, { q: 'search' });

    expect(callUrl(fetchMock)).toBe(`/api/v1/projects?organization_id=${organizationId}&q=search`);
    expect(store.searchQuery).toBe('search');
  });

  it('records a failure and clears stale items', async () => {
    vi.stubGlobal('fetch', stubProjects({ failList: true }));
    const store = useProjectsStore();

    await store.load(organizationId);

    expect(store.items).toHaveLength(0);
    expect(store.nextCursor).toBeNull();
    expect(store.error).toContain('ECONNREFUSED');
  });

  it('shows an empty list without inventing projects', async () => {
    vi.stubGlobal('fetch', stubProjects({ list: page([]) }));
    const store = useProjectsStore();

    await store.load(organizationId);

    expect(store.items).toHaveLength(0);
    expect(store.nextCursor).toBeNull();
    expect(store.error).toBeNull();
  });

  it('appends the next page on loadMore using the cursor', async () => {
    const fetchMock = stubProjects({
      list: page([billing], 'cursor_2'),
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useProjectsStore();
    await store.load(organizationId);

    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(page([search]))));
    await store.loadMore();

    expect(store.items.map((item) => item.id)).toEqual(['prj_billing', 'prj_search']);
    expect(store.nextCursor).toBeNull();
    expect(store.loadingMore).toBe(false);
    expect(callUrl(fetchMock, 1)).toContain('cursor=cursor_2');
  });

  it('does not request another page without a cursor', async () => {
    vi.stubGlobal('fetch', stubProjects({ list: page([billing]) }));
    const store = useProjectsStore();
    await store.load(organizationId);
    const fetchMock = globalThis.fetch as unknown as Mock;

    await store.loadMore();

    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('creates a project with an Idempotency-Key', async () => {
    const fetchMock = stubProjects({ list: page([billing]), created: search });
    vi.stubGlobal('fetch', fetchMock);
    const store = useProjectsStore();
    await store.load(organizationId, { q: '' });

    const created = await store.create({
      organization_id: organizationId,
      name: 'search-index',
      description: 'Indexing pipeline',
    });

    expect(created.id).toBe('prj_search');
    expect(store.items.map((item) => item.id)).toEqual(['prj_search', 'prj_billing']);
    expect(store.error).toBeNull();
    expect(store.creating).toBe(false);

    const postIndex = fetchMock.mock.calls.findIndex(
      (_call, index) => callInit(fetchMock, index).method === 'POST',
    );
    expect(postIndex).toBeGreaterThanOrEqual(0);
    const init = callInit(fetchMock, postIndex);
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: organizationId,
      name: 'search-index',
      description: 'Indexing pipeline',
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toBeTruthy();
  });

  it('reports a rejected creation', async () => {
    vi.stubGlobal('fetch', stubProjects({ created: billing }));
    const globalFetch = globalThis.fetch as unknown as Mock;
    globalFetch.mockImplementation((_input: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        return Promise.resolve(errorEnvelope('permission_denied', 'you do not have access', 403));
      }
      return Promise.resolve(jsonResponse(page([billing])));
    });
    const store = useProjectsStore();

    await expect(
      store.create({ organization_id: organizationId, name: 'billing-service' }),
    ).rejects.toThrow('you do not have access');
    expect(store.error).toBe('you do not have access');
    expect(store.creating).toBe(false);
  });

  it('loads one project and exposes it as current', async () => {
    vi.stubGlobal('fetch', stubProjects({ detail: billing }));
    const store = useProjectsStore();

    await store.loadOne(billing.id);

    expect(store.currentId).toBe(billing.id);
    expect(store.current?.name).toBe('billing-service');
    expect(store.byId(billing.id)?.version).toBe(3);
    expect(store.select('other')).toBeUndefined();
    expect(store.currentId).toBe('other');
    expect(store.error).toBeNull();
  });

  it('surfaces a 404 when the project is gone', async () => {
    vi.stubGlobal('fetch', stubProjects({ failDetail: true }));
    const store = useProjectsStore();

    await store.loadOne('prj_missing');

    expect(store.error).toBe('resource not found');
    expect(store.current).toBeNull();
    expect(store.loading).toBe(false);
  });

  it('applies the server response after a successful update', async () => {
    const fetchMock = stubProjects({
      updated: { ...billing, version: 4, description: 'New copy' },
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useProjectsStore();
    await store.loadOne(billing.id);

    const updated = await store.update(billing.id, { version: 3, description: 'New copy' });

    expect(updated.version).toBe(4);
    expect(store.current?.description).toBe('New copy');
    expect(store.conflict).toBeNull();

    const patchIndex = fetchMock.mock.calls.findIndex(
      (_call, index) => callInit(fetchMock, index).method === 'PATCH',
    );
    expect(patchIndex).toBeGreaterThanOrEqual(0);
    const init = callInit(fetchMock, patchIndex);
    expect(JSON.parse(String(init.body))).toEqual({ version: 3, description: 'New copy' });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toBeTruthy();
  });

  it('keeps the server version authoritative on a 409 conflict', async () => {
    vi.stubGlobal('fetch', stubProjects({ conflictOnPatch: true, detail: billing }));
    const store = useProjectsStore();
    await store.loadOne(billing.id);

    const error = await store
      .update(billing.id, { version: 2, description: 'stale edit' })
      .catch((err: unknown) => err);

    expect((error as { status: number }).status).toBe(409);
    expect(store.conflict?.currentVersion).toBe(7);
    expect(store.conflict?.message).toContain('modified');
    expect(store.error).toBeNull();
    expect(store.saving).toBe(false);
    // The stale local copy is not written over the server state.
    expect(store.current?.version).toBe(3);
    expect(store.current?.description).toBe('Payments and invoicing');

    store.clearConflict();
    expect(store.conflict).toBeNull();
  });
});
