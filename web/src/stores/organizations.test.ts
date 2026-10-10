import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { ACTIVE_ORGANIZATION_STORAGE_KEY, useOrganizationsStore } from './organizations';
import type { Organization, OrganizationPage } from '@/api/types';

const acme: Organization = {
  id: 'org_acme',
  slug: 'acme-engineering',
  name: 'Acme Engineering',
  role: 'owner',
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T10:00:00Z',
};

const globex: Organization = {
  id: 'org_globex',
  slug: 'globex',
  name: 'Globex',
  role: 'member',
  created_at: '2026-10-10T10:05:00Z',
  updated_at: '2026-10-10T10:05:00Z',
};

function page(items: Organization[], next_cursor: string | null = null): OrganizationPage {
  return { items, next_cursor };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorEnvelope(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_1' } }, status);
}

/** stubOrganizations routes /api/v1/organizations GET and POST to fixtures. */
function stubOrganizations(options: {
  list?: OrganizationPage;
  listStatus?: number;
  created?: Organization;
  createdStatus?: number;
  failList?: boolean;
  failCreate?: boolean;
}): Mock {
  return vi.fn((input: string, init?: RequestInit) => {
    if (String(input).includes('/api/v1/organizations')) {
      if (init?.method === 'POST') {
        if (options.failCreate) {
          return Promise.resolve(
            errorEnvelope('duplicate_name', 'slug already in use', options.createdStatus ?? 409),
          );
        }
        return Promise.resolve(jsonResponse(options.created ?? acme, options.createdStatus ?? 201));
      }
      if (options.failList) {
        return Promise.reject(new Error('ECONNREFUSED'));
      }
      return Promise.resolve(jsonResponse(options.list ?? page([acme]), options.listStatus ?? 200));
    }
    return Promise.resolve(jsonResponse({ status: 'ok' }, 200));
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('organizations store', () => {
  it('loads organizations from the API', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ list: page([acme, globex]) }));
    const store = useOrganizationsStore();

    await store.load();

    expect(store.items).toHaveLength(2);
    expect(store.items[0].name).toBe('Acme Engineering');
    expect(store.loading).toBe(false);
    expect(store.error).toBeNull();
    expect(store.loaded).toBe(true);
  });

  it('records an error when the API is unreachable', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ failList: true }));
    const store = useOrganizationsStore();

    await store.load();

    expect(store.loading).toBe(false);
    expect(store.error).toContain('ECONNREFUSED');
    expect(store.items).toHaveLength(0);
    expect(store.loaded).toBe(false);
  });

  it('surfaces an empty list without inventing data', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ list: page([]) }));
    const store = useOrganizationsStore();

    await store.load();

    expect(store.items).toHaveLength(0);
    expect(store.error).toBeNull();
    expect(store.activeOrganization).toBeNull();
  });

  it('creates an organization, activates it and refreshes the list', async () => {
    let listCalls = 0;
    const fetchMock = vi.fn((input: string, init?: RequestInit) => {
      if (String(input).includes('/api/v1/organizations')) {
        if (init?.method === 'POST') {
          return Promise.resolve(jsonResponse(globex, 201));
        }
        listCalls += 1;
        return Promise.resolve(jsonResponse(page(listCalls > 1 ? [acme, globex] : [acme])));
      }
      return Promise.resolve(jsonResponse({ status: 'ok' }, 200));
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useOrganizationsStore();
    await store.load();
    expect(store.items).toHaveLength(1);

    const created = await store.create('Globex', 'globex');

    expect(created.id).toBe('org_globex');
    expect(store.items).toHaveLength(2);
    expect(store.activeOrganizationId).toBe('org_globex');
    expect(store.activeOrganization?.name).toBe('Globex');
    expect(store.creating).toBe(false);
    expect(store.error).toBeNull();
    expect(listCalls).toBe(2);

    const postCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'POST',
    );
    expect(postCall).toBeDefined();
    expect(JSON.parse(String((postCall?.[1] as RequestInit).body))).toEqual({
      name: 'Globex',
      slug: 'globex',
    });
    const key = ((postCall?.[1] as RequestInit).headers as Record<string, string>)[
      'Idempotency-Key'
    ];
    expect(typeof key).toBe('string');
    expect(key.length).toBeGreaterThanOrEqual(8);
  });

  it('keeps the API failure message when creation is rejected', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ failCreate: true }));
    const store = useOrganizationsStore();

    await expect(store.create('Acme', 'acme-engineering')).rejects.toThrow('slug already in use');
    expect(store.error).toBe('slug already in use');
    expect(store.activeOrganizationId).toBeNull();
    expect(store.creating).toBe(false);
  });

  it('persists the active organization id in localStorage', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ list: page([acme, globex]) }));
    const store = useOrganizationsStore();
    await store.load();

    store.setActive('org_globex');

    expect(window.localStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY)).toBe('org_globex');

    const restored = useOrganizationsStore();
    expect(restored.activeOrganizationId).toBe('org_globex');
    expect(restored.selected('org_globex')?.name).toBe('Globex');
    expect(restored.selected('org_missing')).toBeNull();
  });

  it('clears a stale active organization when the API no longer returns it', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ list: page([acme]) }));
    const store = useOrganizationsStore();
    store.setActive('org_gone');
    await store.load();

    expect(store.activeOrganizationId).toBeNull();
    expect(window.localStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY)).toBeNull();
  });
});
