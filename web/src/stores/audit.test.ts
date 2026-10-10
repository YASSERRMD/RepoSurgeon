import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { useAuditStore } from './audit';
import type { AuditEvent, AuditEventPage } from '@/api/types';

const projectId = 'prj_billing';

const created: AuditEvent = {
  id: 'aud_created',
  organization_id: 'org_acme',
  project_id: projectId,
  actor_id: 'usr_1',
  action: 'project.created',
  entity_type: 'project',
  entity_id: projectId,
  request_id: 'req_1',
  payload: { name: 'billing-service' },
  sequence: 1,
  created_at: '2026-10-10T10:00:00Z',
};

const updated: AuditEvent = {
  ...created,
  id: 'aud_updated',
  action: 'project.updated',
  payload: { description: 'Payments and invoicing' },
  sequence: 2,
  created_at: '2026-10-10T10:05:00Z',
};

function page(items: AuditEvent[], next_cursor: string | null = null): AuditEventPage {
  return { items, next_cursor };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function auditUrl(fetchMock: Mock, index = 0): string {
  return String(fetchMock.mock.calls[index]?.[0]);
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('audit store', () => {
  it('loads audit events for a project', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(page([updated, created]))));
    const store = useAuditStore();

    await store.load(projectId);

    expect(store.items).toHaveLength(2);
    expect(store.items[0].action).toBe('project.updated');
    expect(store.projectId).toBe(projectId);
    expect(store.nextCursor).toBeNull();
    expect(store.error).toBeNull();
    expect(store.loading).toBe(false);
  });

  it('sends the action filter to the API', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page([created])));
    vi.stubGlobal('fetch', fetchMock);
    const store = useAuditStore();

    await store.load(projectId, 'project.created');

    expect(auditUrl(fetchMock)).toBe(
      `/api/v1/audit?project_id=${projectId}&action=project.created`,
    );
    expect(store.items).toHaveLength(1);
  });

  it('keeps the filter in state and refetches when applied', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page([created])));
    vi.stubGlobal('fetch', fetchMock);
    const store = useAuditStore();

    store.setFilterAction('project.created');
    await store.load(projectId, store.filterAction);

    expect(store.filterAction).toBe('project.created');
    expect(auditUrl(fetchMock)).toContain('action=project.created');
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('records an error and drops stale rows', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(
            { error: { code: 'not_found', message: 'resource not found', request_id: 'req_1' } },
            404,
          ),
        ),
    );
    const store = useAuditStore();

    await store.load(projectId);

    expect(store.error).toBe('resource not found');
    expect(store.items).toHaveLength(0);
    expect(store.nextCursor).toBeNull();
    expect(store.loading).toBe(false);
  });

  it('shows an empty trail without inventing events', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(page([]))));
    const store = useAuditStore();

    await store.load(projectId);

    expect(store.items).toHaveLength(0);
    expect(store.error).toBeNull();
  });

  it('appends older events on loadMore with the cursor', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(page([updated], 'cursor_2')))
      .mockResolvedValueOnce(jsonResponse(page([created])));
    vi.stubGlobal('fetch', fetchMock);
    const store = useAuditStore();
    await store.load(projectId);

    await store.loadMore();

    expect(store.items.map((item) => item.id)).toEqual(['aud_updated', 'aud_created']);
    expect(store.nextCursor).toBeNull();
    expect(auditUrl(fetchMock, 1)).toBe(`/api/v1/audit?project_id=${projectId}&cursor=cursor_2`);
    expect(store.loadingMore).toBe(false);
  });

  it('does not page again without a cursor', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page([created])));
    vi.stubGlobal('fetch', fetchMock);
    const store = useAuditStore();
    await store.load(projectId);

    await store.loadMore();

    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
