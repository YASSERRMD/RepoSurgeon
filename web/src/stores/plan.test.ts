import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { setCsrfToken } from '@/api/client';
import type { Permission, PlanNode, PlanSubmission, PlanView } from '@/api/types';
import { useAuthStore } from './auth';
import { orderByPrerequisites, usePlanStore } from './plan';

const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';

const investigate: PlanNode = {
  key: 'investigate',
  title: 'Confirm the off-by-one',
  kind: 'investigate',
  evidence: ['internal/api/list.go:120'],
};

const fix: PlanNode = {
  key: 'fix',
  title: 'Bound the limit before offset',
  kind: 'edit',
  path: 'internal/api/list.go',
  line_hint: 120,
  prerequisites: ['investigate'],
  evidence: ['internal/api/list.go:120'],
};

const test: PlanNode = {
  key: 'test',
  title: 'Add a regression test',
  kind: 'test',
  prerequisites: ['fix'],
};

/** Submitted in dependency order; the inspector must show investigate first. */
const nodes: PlanNode[] = [test, fix, investigate];

const serverPlan: PlanView = {
  id: 'pln_1a2b3c4d5e6f7g8h9i0j',
  run_id: RUN_ID,
  revision: 1,
  kind: 'bug',
  risk: 'high',
  summary: 'Fix the pagination off-by-one in the list handler',
  status: 'awaiting_approval',
  nodes,
  acceptance: [
    {
      key: 'ac1',
      description: 'A page of 10 never returns 11 rows',
      verification: 'go test ./internal/api/...',
    },
  ],
  findings: [
    {
      severity: 'error',
      code: 'plan_missing_evidence',
      message: 'step "fix" cites no evidence',
    },
    {
      severity: 'warning',
      code: 'plan_no_tests',
      message: 'plan proposes no test or verification step',
    },
  ],
  approvals: [],
  created_at: '2026-10-10T10:05:00Z',
};

const approvedPlan: PlanView = {
  ...serverPlan,
  status: 'approved',
  approvals: [
    {
      revision: 1,
      actor_id: 'usr_9i8h7g6f5e4d3c2b1a0z',
      decision: 'approved',
      reason: 'verified the diff and the test evidence',
      created_at: '2026-10-10T11:00:00Z',
    },
  ],
};

const submission: PlanSubmission = {
  base_sha: '9a1aecc0f2b3d4e5f60718293a4b5c6d7e8f9012',
  context_sha256: '4f2a1b3c5d6e7f8a9b0c',
  plan: {
    kind: 'bug',
    risk: 'high',
    summary: 'Fix the pagination off-by-one in the list handler',
    base_sha: '9a1aecc0f2b3d4e5f60718293a4b5c6d7e8f9012',
    hypotheses: [],
    nodes: [investigate, fix, test],
    acceptance: serverPlan.acceptance,
  },
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_plan_store' } }, status);
}

interface StubOptions {
  plan?: PlanView;
  planError?: { code: string; message: string; status: number };
  save?: PlanView;
  saveError?: { code: string; message: string; status: number };
  decide?: PlanView;
  decideError?: { code: string; message: string; status: number };
}

/** stubApi serves the plan read, the store and the decision for one run. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.endsWith('/plan/approve')) {
      if (options.decideError !== undefined) {
        const failure = options.decideError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.decide ?? approvedPlan));
    }
    if (url.endsWith('/plan') && method === 'POST') {
      if (options.saveError !== undefined) {
        const failure = options.saveError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.save ?? serverPlan, 201));
    }
    if (url.endsWith('/plan')) {
      if (options.planError !== undefined) {
        const failure = options.planError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.plan ?? serverPlan));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

/** headersOf reads the request headers of a recorded call. */
function headersOf(init: RequestInit): Record<string, string> {
  return (init.headers ?? {}) as Record<string, string>;
}

function lastPlanCall(fetchMock: Mock): { url: string; init: RequestInit } {
  const call = fetchMock.mock.calls.find((entry) => String(entry[0]).endsWith('/plan'));
  expect(call).toBeDefined();
  return { url: String(call?.[0]), init: (call?.[1] ?? {}) as RequestInit };
}

function decideCall(fetchMock: Mock): { url: string; init: RequestInit } {
  const call = fetchMock.mock.calls.find((entry) => String(entry[0]).endsWith('/plan/approve'));
  expect(call).toBeDefined();
  return { url: String(call?.[0]), init: (call?.[1] ?? {}) as RequestInit };
}

function signIn(permissions: Permission[]): void {
  const auth = useAuthStore();
  auth.user = {
    id: 'usr_1a2b3c4d5e6f7g8h9i0j',
    email: 'maintainer@example.com',
    organizations: [
      {
        id: 'org_acme',
        name: 'Acme Engineering',
        slug: 'acme-engineering',
        role: 'maintainer',
        permissions,
      },
    ],
  };
  auth.status = 'authenticated';
  auth.activeOrganizationId = 'org_acme';
}

beforeEach(() => {
  setActivePinia(createPinia());
  setCsrfToken(null);
  signIn(['project.read']);
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
});

describe('orderByPrerequisites', () => {
  it('lists a node after every node it names as a prerequisite', () => {
    expect(orderByPrerequisites(nodes).map((node) => node.key)).toEqual([
      'investigate',
      'fix',
      'test',
    ]);
  });

  it('keeps independent nodes in the order the server sent them', () => {
    const chain: PlanNode[] = [
      { key: 'review', title: 'Review the result', kind: 'review' },
      { key: 'verify', title: 'Run the checks', kind: 'verify' },
    ];
    expect(orderByPrerequisites(chain).map((node) => node.key)).toEqual(['review', 'verify']);
  });

  it('ignores a prerequisite that names an unknown step', () => {
    const orphan: PlanNode = { key: 'edit', title: 'Edit', kind: 'edit', prerequisites: ['ghost'] };
    expect(orderByPrerequisites([orphan]).map((node) => node.key)).toEqual(['edit']);
  });

  it('keeps a cycle visible in the submitted order instead of dropping it', () => {
    const cycle: PlanNode[] = [
      { key: 'a', title: 'A', kind: 'edit', prerequisites: ['b'] },
      { key: 'b', title: 'B', kind: 'test', prerequisites: ['a'] },
    ];
    expect(orderByPrerequisites(cycle).map((node) => node.key)).toEqual(['a', 'b']);
  });
});

describe('plan store', () => {
  it('loads the latest revision with the server status and findings', async () => {
    vi.stubGlobal('fetch', stubApi());
    const store = usePlanStore();

    const loaded = await store.load(RUN_ID);

    expect(loaded).toEqual(serverPlan);
    expect(store.runId).toBe(RUN_ID);
    expect(store.plan).toEqual(serverPlan);
    // Status, findings and approvals are the server's; nothing is derived here.
    expect(store.plan?.status).toBe('awaiting_approval');
    expect(store.isAwaitingApproval).toBe(true);
    expect(store.isApproved).toBe(false);
    expect(store.blockingFindings.map((finding) => finding.code)).toEqual([
      'plan_missing_evidence',
    ]);
    expect(store.warnings.map((finding) => finding.code)).toEqual(['plan_no_tests']);
    expect(store.error).toBeNull();
    expect(store.loading).toBe(false);
  });

  it('orders the nodes by their prerequisites', async () => {
    vi.stubGlobal('fetch', stubApi());
    const store = usePlanStore();
    await store.load(RUN_ID);

    expect(store.orderedNodes.map((node) => node.key)).toEqual(['investigate', 'fix', 'test']);
  });

  it('reports a 404 with the server code and keeps no revision', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        planError: {
          code: 'plan_not_found',
          message: 'no plan has been created for this run yet',
          status: 404,
        },
      }),
    );
    const store = usePlanStore();

    await expect(store.load(RUN_ID)).resolves.toBeNull();

    expect(store.plan).toBeNull();
    expect(store.isAwaitingApproval).toBe(false);
    expect(store.error?.status).toBe(404);
    expect(store.error?.code).toBe('plan_not_found');
    expect(store.error?.message).toBe('no plan has been created for this run yet');
    expect(store.error?.retryable).toBe(false);
  });

  it('surfaces a 403 read refusal with the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        planError: {
          code: 'permission_denied',
          message: 'project.read is required to read a plan',
          status: 403,
        },
      }),
    );
    const store = usePlanStore();

    await expect(store.load(RUN_ID)).resolves.toBeNull();

    expect(store.error?.status).toBe(403);
    expect(store.error?.code).toBe('permission_denied');
  });

  it('stores a revision and adopts the server plan, including its status', async () => {
    const fetchMock = stubApi({ save: { ...serverPlan, status: 'draft', revision: 2 } });
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_plan');
    const store = usePlanStore();
    store.bindRun(RUN_ID);

    const stored = await store.save(submission);

    expect(stored?.status).toBe('draft');
    expect(stored?.revision).toBe(2);
    expect(store.plan).toEqual({ ...serverPlan, status: 'draft', revision: 2 });
    expect(store.isAwaitingApproval).toBe(false);
    expect(store.error).toBeNull();

    const { url, init } = lastPlanCall(fetchMock);
    expect(url).toBe(`/api/v1/runs/${RUN_ID}/plan`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual(submission);
    expect(headersOf(init)['Idempotency-Key']).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    expect(headersOf(init)['X-CSRF-Token']).toBe('csrf_plan');
  });

  it('reports a 422 verification failure with the server message', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        saveError: {
          code: 'plan_verification_failed',
          message: 'plan references "ghost.go" which is not in the indexed workspace',
          status: 422,
        },
      }),
    );
    setCsrfToken('csrf_plan');
    const store = usePlanStore();
    store.bindRun(RUN_ID);

    await expect(store.save(submission)).resolves.toBeNull();

    expect(store.plan).toBeNull();
    expect(store.error?.status).toBe(422);
    expect(store.error?.code).toBe('plan_verification_failed');
    expect(store.error?.message).toContain('ghost.go');
  });

  it('keeps the last revision on screen when a reload is refused', async () => {
    let refusing = false;
    const fetchMock = vi.fn((input: string): Promise<Response> => {
      const url = String(input);
      if (!url.endsWith('/plan')) {
        return Promise.reject(new Error(`unexpected request to ${url}`));
      }
      if (refusing) {
        return Promise.resolve(
          errorResponse('internal_error', 'the plan service is unavailable', 500),
        );
      }
      return Promise.resolve(jsonResponse(serverPlan));
    });
    vi.stubGlobal('fetch', fetchMock);
    const store = usePlanStore();
    await store.load(RUN_ID);
    expect(store.plan).toEqual(serverPlan);

    refusing = true;
    await expect(store.load(RUN_ID)).resolves.toBeNull();

    // The reviewer keeps the server's last answer; the banner explains the refusal.
    expect(store.plan).toEqual(serverPlan);
    expect(store.error?.status).toBe(500);
    expect(store.error?.retryable).toBe(true);
  });

  it('refuses a save for an unbound run instead of inventing a revision', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = usePlanStore();

    await expect(store.save(submission)).resolves.toBeNull();

    expect(fetchMock).not.toHaveBeenCalled();
    expect(store.error?.code).toBe('plan_run_unbound');
    expect(store.plan).toBeNull();
  });

  it('decides the loaded revision and adopts the server answer', async () => {
    const fetchMock = stubApi({ decide: approvedPlan });
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_decide');
    const store = usePlanStore();
    await store.load(RUN_ID);

    const decided = await store.decide('approve', 'verified the diff and the test evidence');

    expect(decided).toEqual(approvedPlan);
    expect(store.plan).toEqual(approvedPlan);
    expect(store.isApproved).toBe(true);
    expect(store.isAwaitingApproval).toBe(false);
    expect(store.error).toBeNull();
    expect(store.deciding).toBe(false);

    const { url, init } = decideCall(fetchMock);
    expect(url).toBe(`/api/v1/runs/${RUN_ID}/plan/approve`);
    expect(init.method).toBe('POST');
    // The revision decided is the one the server reported, never a local guess.
    expect(JSON.parse(String(init.body))).toEqual({
      revision: 1,
      decision: 'approve',
      reason: 'verified the diff and the test evidence',
    });
    expect(headersOf(init)['Idempotency-Key']).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    expect(headersOf(init)['X-CSRF-Token']).toBe('csrf_decide');
  });

  it('omits an empty reason from the decision body', async () => {
    const fetchMock = stubApi({ decide: approvedPlan });
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_decide');
    const store = usePlanStore();
    await store.load(RUN_ID);

    await store.decide('reject');

    const { init } = decideCall(fetchMock);
    expect(JSON.parse(String(init.body))).toEqual({ revision: 1, decision: 'reject' });
  });

  it('surfaces a 409 with the server code and keeps the revision on screen', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        plan: serverPlan,
        decideError: {
          code: 'plan_conflict',
          message: 'plan revision already decided',
          status: 409,
        },
      }),
    );
    setCsrfToken('csrf_decide');
    const store = usePlanStore();
    await store.load(RUN_ID);

    await expect(store.decide('approve')).resolves.toBeNull();

    // The last server answer stays; a conflict is never resolved locally.
    expect(store.plan).toEqual(serverPlan);
    expect(store.error?.status).toBe(409);
    expect(store.error?.code).toBe('plan_conflict');
    expect(store.error?.message).toBe('plan revision already decided');
    expect(store.deciding).toBe(false);
  });

  it('surfaces a 403 decision refusal with the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        plan: serverPlan,
        decideError: {
          code: 'permission_denied',
          message: 'approval.decide is required to decide a plan revision',
          status: 403,
        },
      }),
    );
    setCsrfToken('csrf_decide');
    const store = usePlanStore();
    await store.load(RUN_ID);

    await expect(store.decide('approve')).resolves.toBeNull();

    expect(store.plan).toEqual(serverPlan);
    expect(store.error?.status).toBe(403);
    expect(store.error?.code).toBe('permission_denied');
  });

  it('refuses a decision when no revision is loaded', async () => {
    const fetchMock = stubApi({ plan: serverPlan });
    vi.stubGlobal('fetch', fetchMock);
    const store = usePlanStore();
    store.bindRun(RUN_ID);

    await expect(store.decide('approve')).resolves.toBeNull();

    expect(fetchMock).not.toHaveBeenCalled();
    expect(store.error?.code).toBe('plan_revision_unavailable');
  });

  it('gates canDecide on the approval.decide permission of the active organization', () => {
    vi.stubGlobal('fetch', stubApi());
    const store = usePlanStore();

    expect(store.canDecide).toBe(false);

    const auth = useAuthStore();
    auth.activeOrganizationId = 'org_other';
    expect(store.canDecide).toBe(false);

    auth.user = {
      id: 'usr_1a2b3c4d5e6f7g8h9i0j',
      email: 'owner@example.com',
      organizations: [
        {
          id: 'org_other',
          name: 'Other',
          slug: 'other',
          role: 'owner',
          permissions: ['approval.decide'],
        },
      ],
    };
    expect(store.canDecide).toBe(true);
  });

  it('drops a revision that answers for a run the operator left', async () => {
    vi.stubGlobal('fetch', stubApi({ plan: serverPlan }));
    const store = usePlanStore();

    const loaded = store.load(RUN_ID);
    store.bindRun('run_9z8y7x6w5v4u3t2s1r0q');

    await expect(loaded).resolves.toBeNull();
    expect(store.plan).toBeNull();
    expect(store.error).toBeNull();
  });

  it('clears the revision and the failure on reset', async () => {
    vi.stubGlobal('fetch', stubApi({ plan: serverPlan }));
    const store = usePlanStore();
    await store.load(RUN_ID);
    expect(store.plan).not.toBeNull();

    store.reset();

    expect(store.plan).toBeNull();
    expect(store.runId).toBeNull();
    expect(store.error).toBeNull();
    expect(store.isAwaitingApproval).toBe(false);
  });
});
