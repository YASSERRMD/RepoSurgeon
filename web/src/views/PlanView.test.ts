import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import PlanView from './PlanView.vue';
import { setCsrfToken } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import type { CurrentUser, Permission, PlanView as PlanRevision } from '@/api/types';

const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';

/** Submitted out of order on purpose: the inspector sorts them. */
const nodes = [
  {
    key: 'test',
    title: 'Add a regression test',
    kind: 'test' as const,
    prerequisites: ['fix'],
  },
  {
    key: 'fix',
    title: 'Bound the limit before offset',
    kind: 'edit' as const,
    path: 'internal/api/list.go',
    line_hint: 120,
    prerequisites: ['investigate'],
    evidence: ['internal/api/list.go:120'],
  },
  {
    key: 'investigate',
    title: 'Confirm the off-by-one',
    kind: 'investigate' as const,
    evidence: ['internal/api/list.go:120'],
  },
];

const plan: PlanRevision = {
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
      message: 'step "test" cites no evidence',
    },
    {
      severity: 'warning',
      code: 'plan_no_review',
      message: 'plan proposes no review step',
    },
  ],
  approvals: [],
  base_sha: '9a1aecc0f2b3d4e5f60718293a4b5c6d7e8f9012',
  out_of_scope: ['UI pagination controls'],
  assumptions: ['The store returns rows in insertion order'],
  created_at: '2026-10-10T10:05:00Z',
};

const approvedPlan: PlanRevision = {
  ...plan,
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

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_plan_view' } }, status);
}

interface StubOptions {
  plan?: PlanRevision;
  planError?: { code: string; message: string; status: number };
  decide?: PlanRevision;
  decideError?: { code: string; message: string; status: number };
}

/** stubApi serves the plan read and the decision for one run. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.endsWith('/plan/approve')) {
      if (options.decideError !== undefined) {
        const failure = options.decideError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.decide ?? approvedPlan));
    }
    if (url.endsWith('/plan')) {
      if (options.planError !== undefined) {
        const failure = options.planError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.plan ?? plan));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

/** headersOf reads the request headers of a recorded call. */
function headersOf(init: RequestInit): Record<string, string> {
  return (init.headers ?? {}) as Record<string, string>;
}

function signIn(permissions: Permission[]): void {
  const auth = useAuthStore();
  const user: CurrentUser = {
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
  auth.user = user;
  auth.status = 'authenticated';
  auth.activeOrganizationId = 'org_acme';
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/runs/:runId/plan', component: PlanView }],
  });
}

async function mountPlan(
  permissions: Permission[] = ['project.read', 'approval.decide'],
): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  signIn(permissions);
  const router = testRouter();
  await router.push(`/runs/${RUN_ID}/plan`);
  await router.isReady();
  const wrapper = mount(PlanView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('PlanView', () => {
  it('renders the summary with kind, risk, revision and status from the server', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-kind"]').text()).toBe('bug');
    expect(wrapper.find('[data-testid="plan-risk"]').text()).toBe('high risk');
    expect(wrapper.find('[data-testid="plan-risk"]').classes()).toContain('badge-risk-high');
    expect(wrapper.find('[data-testid="plan-status"]').text()).toBe('awaiting approval');
    expect(wrapper.find('[data-testid="plan-revision"]').text()).toBe('1');
    expect(wrapper.find('[data-testid="plan-summary-text"]').text()).toBe(plan.summary);
    expect(wrapper.find('[data-testid="plan-run-id"]').text()).toBe(RUN_ID);
    // The base commit is truncated, never printed in full.
    expect(wrapper.find('[data-testid="plan-base-sha"]').text()).toBe('9a1aecc0f2b3…');
  });

  it('renders errors in red and warnings in amber with their codes', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    const error = wrapper.find('[data-testid="finding-error-plan_missing_evidence"]');
    expect(error.classes()).toContain('finding-error');
    expect(error.find('[data-testid="finding-severity"]').text()).toBe('error');
    expect(error.find('[data-testid="finding-code"]').text()).toBe('plan_missing_evidence');
    expect(error.find('[data-testid="finding-message"]').text()).toBe(
      'step "test" cites no evidence',
    );

    const warning = wrapper.find('[data-testid="finding-warning-plan_no_review"]');
    expect(warning.classes()).toContain('finding-warning');
    expect(warning.find('[data-testid="finding-code"]').text()).toBe('plan_no_review');
    expect(wrapper.find('[data-testid="plan-findings-none"]').exists()).toBe(false);
  });

  it('renders the steps in dependency order with their evidence and prerequisites', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    const rendered = wrapper.findAll('.node-list .node');
    expect(rendered.map((node) => node.attributes('data-testid'))).toEqual([
      'node-investigate',
      'node-fix',
      'node-test',
    ]);

    const fixNode = wrapper.find('[data-testid="node-fix"]');
    expect(fixNode.find('[data-testid="node-title"]').text()).toBe('Bound the limit before offset');
    expect(fixNode.find('[data-testid="node-kind"]').text()).toBe('edit');
    expect(fixNode.find('[data-testid="node-key"]').text()).toBe('fix');
    expect(fixNode.find('[data-testid="node-path"]').text()).toContain('internal/api/list.go');
    expect(fixNode.find('[data-testid="node-path"]').text()).toContain('120');
    expect(fixNode.findAll('[data-testid="node-evidence-item"]')).toHaveLength(1);
    expect(fixNode.find('[data-testid="node-evidence-item"]').text()).toBe(
      'internal/api/list.go:120',
    );
    expect(fixNode.findAll('[data-testid="node-prerequisite"]').map((item) => item.text())).toEqual(
      ['investigate'],
    );

    const investigateNode = wrapper.find('[data-testid="node-investigate"]');
    expect(investigateNode.find('[data-testid="node-prerequisites"]').text()).toContain(
      'no prerequisites',
    );
    const testNode = wrapper.find('[data-testid="node-test"]');
    expect(testNode.find('[data-testid="node-noevidence"]').exists()).toBe(true);
    expect(testNode.find('[data-testid="node-nopath"]').exists()).toBe(true);
  });

  it('lists the acceptance criteria with their verification commands', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    const criterion = wrapper.find('[data-testid="criterion-ac1"]');
    expect(criterion.find('[data-testid="criterion-key"]').text()).toBe('ac1');
    expect(criterion.find('[data-testid="criterion-description"]').text()).toBe(
      'A page of 10 never returns 11 rows',
    );
    expect(criterion.find('[data-testid="criterion-verification"]').text()).toContain(
      'go test ./internal/api/...',
    );
  });

  it('shows out-of-scope and assumptions explicitly', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    const scope = wrapper.find('[data-testid="plan-out-of-scope"]');
    expect(scope.find('[data-testid="plan-out-of-scope-item"]').text()).toBe(
      'UI pagination controls',
    );
    expect(wrapper.find('[data-testid="plan-assumption-item"]').text()).toBe(
      'The store returns rows in insertion order',
    );
    expect(wrapper.find('[data-testid="plan-out-of-scope-none"]').exists()).toBe(false);
  });

  it('states when the projection returns no out-of-scope entries or assumptions', async () => {
    vi.stubGlobal('fetch', stubApi({ plan: { ...plan, out_of_scope: [], assumptions: [] } }));
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-out-of-scope"]').text()).toContain(
      'The plan leaves nothing out of scope.',
    );
    expect(wrapper.find('[data-testid="plan-assumptions"]').text()).toContain(
      'The plan records no assumptions.',
    );
    expect(wrapper.find('[data-testid="plan-out-of-scope-item"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-assumption-item"]').exists()).toBe(false);
  });

  it('reports that no decision has been recorded yet', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-approvals"]').text()).toContain(
      'No decision has been recorded for this run yet.',
    );
    expect(wrapper.find('[data-testid="plan-approvals-none"]').exists()).toBe(true);
  });

  it('hides the decision controls and explains the permission without approval.decide', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPlan(['project.read']);

    expect(wrapper.find('[data-testid="plan-approve"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-reject"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-decision-form"]').exists()).toBe(false);

    const notice = wrapper.find('[data-testid="plan-decision-restricted"]');
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toContain('Deciding is restricted.');
    expect(notice.text()).toContain('approval.decide');
    // The revision stays readable without the permission.
    expect(wrapper.find('[data-testid="plan-status"]').exists()).toBe(true);
  });

  it('hides the decision controls once the revision is no longer awaiting approval', async () => {
    vi.stubGlobal('fetch', stubApi({ plan: approvedPlan }));
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-approve"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-decision-settled"]').text()).toContain(
      'Revision 1 is approved',
    );
  });

  it('approves the revision and shows the status the server returned', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_decide_plan');
    const wrapper = await mountPlan();

    await wrapper.find('[data-testid="plan-approve"]').trigger('click');
    await wrapper.find('[data-testid="plan-decision-reason"]').setValue('checks are green');
    await wrapper.find('[data-testid="plan-decision-form"]').trigger('submit.prevent');
    await flushPromises();

    const call = fetchMock.mock.calls.find((entry) => String(entry[0]).endsWith('/plan/approve'));
    expect(call).toBeDefined();
    const init = (call?.[1] ?? {}) as RequestInit;
    expect(String(call?.[0])).toBe(`/api/v1/runs/${RUN_ID}/plan/approve`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      revision: 1,
      decision: 'approve',
      reason: 'checks are green',
    });
    expect(headersOf(init)['Idempotency-Key']).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    expect(headersOf(init)['X-CSRF-Token']).toBe('csrf_decide_plan');

    // Everything shown afterwards comes from the response, never the client.
    expect(wrapper.find('[data-testid="plan-status"]').text()).toBe('approved');
    expect(wrapper.find('[data-testid="plan-error"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-decision-form"]').exists()).toBe(false);
    const approvalRow = wrapper.find('[data-testid="approval-1"]');
    expect(approvalRow.find('[data-testid="approval-actor"]').text()).toBe(
      'usr_9i8h7g6f5e4d3c2b1a0z',
    );
    expect(approvalRow.find('[data-testid="approval-decision"]').text()).toBe('approved');
    expect(approvalRow.find('[data-testid="approval-reason"]').text()).toBe(
      'verified the diff and the test evidence',
    );
  });

  it('cancels a decision without sending anything', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountPlan();

    await wrapper.find('[data-testid="plan-reject"]').trigger('click');
    expect(wrapper.find('[data-testid="plan-decision-form"]').exists()).toBe(true);

    await wrapper.find('[data-testid="plan-decision-cancel"]').trigger('click');

    expect(wrapper.find('[data-testid="plan-decision-form"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-approve"]').exists()).toBe(true);
    expect(fetchMock.mock.calls).toHaveLength(1);
  });

  it('surfaces a 409 refusal with the server code and keeps the revision', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        decideError: {
          code: 'plan_conflict',
          message: 'plan revision already decided',
          status: 409,
        },
      }),
    );
    setCsrfToken('csrf_decide_plan');
    const wrapper = await mountPlan();

    await wrapper.find('[data-testid="plan-approve"]').trigger('click');
    await wrapper.find('[data-testid="plan-decision-form"]').trigger('submit.prevent');
    await flushPromises();

    const banner = wrapper.find('[data-testid="plan-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('plan_conflict');
    expect(banner.text()).toContain('plan revision already decided');
    // The revision on screen is still the server's last answer.
    expect(wrapper.find('[data-testid="plan-status"]').text()).toBe('awaiting approval');
    expect(wrapper.find('[data-testid="plan-revision"]').text()).toBe('1');
  });

  it('reports the empty state when the run has no plan', async () => {
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
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-error"]').text()).toContain('plan_not_found');
    expect(wrapper.find('[data-testid="plan-error"]').text()).toContain(
      'no plan has been created for this run yet',
    );
    expect(wrapper.find('[data-testid="plan-empty"]').text()).toContain('No plan for this run yet');
    expect(wrapper.find('[data-testid="plan-summary"]').exists()).toBe(false);
  });

  it('retries a failed read and renders the revision', async () => {
    let failing = true;
    const fetchMock = vi.fn((input: string): Promise<Response> => {
      const url = String(input);
      if (!url.endsWith('/plan')) {
        return Promise.reject(new Error(`unexpected request to ${url}`));
      }
      if (failing) {
        return Promise.resolve(errorResponse('not_found', 'run not found', 404));
      }
      return Promise.resolve(jsonResponse(plan));
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountPlan();

    expect(wrapper.find('[data-testid="plan-empty"]').exists()).toBe(true);

    failing = false;
    await wrapper.find('[data-testid="plan-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="plan-error"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-revision"]').text()).toBe('1');
    expect(wrapper.find('[data-testid="plan-status"]').text()).toBe('awaiting approval');
  });

  it('shows the loading skeleton while the revision is in flight', async () => {
    vi.stubGlobal('fetch', stubApi());
    const pinia = createPinia();
    setActivePinia(pinia);
    signIn(['project.read', 'approval.decide']);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/runs/:runId/plan', component: PlanView }],
    });
    await router.push(`/runs/${RUN_ID}/plan`);
    await router.isReady();
    const wrapper = mount(PlanView, { global: { plugins: [pinia, router] } });

    expect(wrapper.find('[data-testid="plan-loading"]').exists()).toBe(true);
    await flushPromises();
    expect(wrapper.find('[data-testid="plan-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="plan-summary"]').exists()).toBe(true);
  });

  it('asks for a run when the route carries no identifier', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const pinia = createPinia();
    setActivePinia(pinia);
    signIn(['project.read']);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/plan', component: PlanView }],
    });
    await router.push('/plan');
    await router.isReady();
    const wrapper = mount(PlanView, { global: { plugins: [pinia, router] } });
    await flushPromises();

    expect(wrapper.find('[data-testid="plan-no-run"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="plan-summary"]').exists()).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
