import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { setCsrfToken } from '@/api/client';
import type { Approval, ToolDescriptor } from '@/api/types';
import { useToolsStore } from './tools';

const pendingApproval: Approval = {
  id: 'apr_pending',
  organization_id: 'org_acme',
  project_id: 'prj_billing',
  run_id: 'run_1a2b3c4d5e6f7g8h9i0j',
  action: 'merge_pr',
  reason: 'required checks are green and the diff is reviewed',
  state: 'pending',
  requested_by: 'usr_requester',
  expires_at: '2026-10-11T10:00:00Z',
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T10:00:00Z',
};

const decidedApproval: Approval = {
  ...pendingApproval,
  state: 'approved',
  decided_by: 'usr_admin',
  decision_reason: 'verified the diff and the test evidence',
  updated_at: '2026-10-10T11:00:00Z',
};

const rejectedApproval: Approval = {
  ...pendingApproval,
  id: 'apr_rejected',
  action: 'delete_branch',
  reason: 'branch is no longer needed',
  state: 'rejected',
  decided_by: 'usr_admin',
  decision_reason: 'keep the branch until the release is tagged',
  updated_at: '2026-10-10T09:00:00Z',
};

const toolCatalog: ToolDescriptor[] = [
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
    name: 'merge_pr',
    description: 'Merge a reviewed pull request on GitHub',
    risk: 'external_side_effect',
    permission: 'merge.execute',
    approval: true,
    sandboxed: false,
    permitted: false,
  },
];

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_tools_test' } }, status);
}

interface StubOptions {
  list?: Approval[];
  /** listError makes the inbox request fail with that status and envelope. */
  listError?: { code: string; message: string; status: number };
  tools?: ToolDescriptor[];
  toolsError?: { code: string; message: string; status: number };
  /** decision is the approval returned by a successful decision. */
  decision?: Approval;
  /** decisionError makes POST …/decide fail; conflictList is then served on reload. */
  decisionError?: { code: string; message: string; status: number };
  conflictList?: Approval[];
}

/**
 * stubApprovals serves the tool catalog and the approval endpoints. The inbox
 * state lives in the stub so a 409 can be followed by a reload that shows the
 * server's own answer instead of the request the UI tried to send.
 */
function stubApprovals(options: StubOptions = {}): { fetchMock: Mock; served: () => Approval[] } {
  let items = options.list ?? [pendingApproval, rejectedApproval];
  const served = (): Approval[] => items;
  const fetchMock = vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('/api/v1/tools')) {
      if (options.toolsError !== undefined) {
        const failure = options.toolsError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse({ items: options.tools ?? toolCatalog }));
    }
    if (url.endsWith('/decide')) {
      if (options.decisionError !== undefined) {
        const failure = options.decisionError;
        items = options.conflictList ?? [decidedApproval];
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.decision ?? decidedApproval));
    }
    if (url.includes('/api/v1/approvals')) {
      if (options.listError !== undefined) {
        const failure = options.listError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse({ items, next_cursor: null }));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
  return { fetchMock, served };
}

/** headersOf reads the request headers of a recorded call. */
function headersOf(init: RequestInit): Record<string, string> {
  return (init.headers ?? {}) as Record<string, string>;
}

beforeEach(() => {
  setActivePinia(createPinia());
  setCsrfToken(null);
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
});

describe('tools store', () => {
  it('loads the approval inbox and counts the pending requests', async () => {
    const { fetchMock } = stubApprovals({});
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();

    await store.load();

    expect(store.items).toHaveLength(2);
    expect(store.pendingCount).toBe(1);
    expect(store.error).toBeNull();
    expect(store.loading).toBe(false);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/approvals');
  });

  it('records a 403 failure and drops the stale rows', async () => {
    const { fetchMock } = stubApprovals({
      listError: {
        code: 'permission_denied',
        message: 'you do not have access to this organization',
        status: 403,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();

    await store.load();

    expect(store.error).toBe('you do not have access to this organization');
    expect(store.items).toHaveLength(0);
    expect(store.pendingCount).toBe(0);
    expect(store.loading).toBe(false);
  });

  it('approves a request and adopts the server response verbatim', async () => {
    setCsrfToken('csrf_decide');
    const { fetchMock } = stubApprovals({ list: [pendingApproval], decision: decidedApproval });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();
    await store.load();

    const decided = await store.approve(pendingApproval.id, 'verified the diff');

    expect(decided).toEqual(decidedApproval);
    expect(store.items[0]?.state).toBe('approved');
    expect(store.items[0]?.decided_by).toBe('usr_admin');
    expect(store.pendingCount).toBe(0);
    expect(store.error).toBeNull();
    expect(store.conflict).toBeNull();
    expect(store.decidingApprovalId).toBeNull();

    const decisionCall = fetchMock.mock.calls.find((call) => String(call[0]).endsWith('/decide'));
    expect(decisionCall).toBeDefined();
    const init = (decisionCall?.[1] ?? {}) as RequestInit;
    expect(String(decisionCall?.[0])).toBe(`/api/v1/approvals/${pendingApproval.id}/decide`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      decision: 'approve',
      reason: 'verified the diff',
    });
    // Every decision is minted its own idempotency key.
    expect(headersOf(init)['Idempotency-Key']).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    expect(headersOf(init)['X-CSRF-Token']).toBe('csrf_decide');
  });

  it('rejects a request without sending an empty reason', async () => {
    setCsrfToken('csrf_reject');
    const { fetchMock } = stubApprovals({
      list: [pendingApproval],
      decision: { ...pendingApproval, state: 'rejected', decided_by: 'usr_admin' },
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();
    await store.load();

    await store.reject(pendingApproval.id);

    const decisionCall = fetchMock.mock.calls.find((call) => String(call[0]).endsWith('/decide'));
    const init = (decisionCall?.[1] ?? {}) as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ decision: 'reject' });
    expect(store.items[0]?.state).toBe('rejected');
    expect(store.decidingApprovalId).toBeNull();
  });

  it('surfaces a 409 with the server message and reloads the authoritative state', async () => {
    const { fetchMock } = stubApprovals({
      list: [pendingApproval],
      decisionError: {
        code: 'approval_not_pending',
        message: 'approval is not pending',
        status: 409,
      },
      conflictList: [decidedApproval],
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();
    await store.load();

    await expect(store.approve(pendingApproval.id)).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      code: 'approval_not_pending',
    });

    expect(store.conflict?.message).toBe('approval is not pending');
    expect(store.error).toBeNull();
    expect(store.decidingApprovalId).toBeNull();
    // The row now shows what the server recorded, not what the UI attempted.
    expect(store.items).toEqual([decidedApproval]);
    expect(store.pendingCount).toBe(0);
    const reloads = fetchMock.mock.calls.filter((call) => {
      const init = call[1] as RequestInit | undefined;
      return String(call[0]) === '/api/v1/approvals' && (init?.method ?? 'GET') === 'GET';
    });
    expect(reloads).toHaveLength(2);
  });

  it('records a server failure instead of guessing a state', async () => {
    const { fetchMock } = stubApprovals({
      list: [pendingApproval],
      decisionError: {
        code: 'internal_error',
        message: 'approval service unavailable',
        status: 500,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();
    await store.load();

    await expect(store.approve(pendingApproval.id)).rejects.toMatchObject({ status: 500 });

    expect(store.error).toBe('approval service unavailable');
    expect(store.conflict).toBeNull();
    expect(store.items).toEqual([pendingApproval]);
    expect(store.pendingCount).toBe(1);
    expect(store.decidingApprovalId).toBeNull();
  });

  it('loads the tool catalog', async () => {
    const { fetchMock } = stubApprovals({});
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();

    await store.loadTools();

    expect(store.tools).toHaveLength(2);
    expect(store.tools.map((tool) => tool.name)).toEqual(['read_file', 'merge_pr']);
    expect(store.toolsError).toBeNull();
    expect(store.loadingTools).toBe(false);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/tools');
  });

  it('records a catalog failure without touching the inbox', async () => {
    const { fetchMock } = stubApprovals({
      toolsError: {
        code: 'permission_denied',
        message: 'you do not have access to this organization',
        status: 403,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = useToolsStore();

    await store.loadTools();

    expect(store.toolsError).toBe('you do not have access to this organization');
    expect(store.tools).toHaveLength(0);
    expect(store.loadingTools).toBe(false);
  });
});
