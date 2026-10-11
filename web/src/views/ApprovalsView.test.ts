import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import ApprovalsView from './ApprovalsView.vue';
import { useAuthStore } from '@/stores/auth';
import { setCsrfToken } from '@/api/client';
import type { Approval, CurrentUser, Permission, Role } from '@/api/types';

const ORGANIZATION_ID = 'org_acme';

const pendingApproval: Approval = {
  id: 'apr_pending',
  organization_id: ORGANIZATION_ID,
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

const approvedApproval: Approval = {
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
  updated_at: '2026-10-10T09:00:00Z',
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_approvals_test' } }, status);
}

/** signIn seeds the auth store directly; the router guard already ran. */
function signIn(role: Role, permissions: Permission[]): void {
  const auth = useAuthStore();
  const user: CurrentUser = {
    id: 'usr_1a2b3c4d5e6f7g8h9i0j',
    email: 'owner@example.com',
    organizations: [
      {
        id: ORGANIZATION_ID,
        name: 'Acme Engineering',
        slug: 'acme-engineering',
        role,
        permissions,
      },
    ],
  };
  auth.user = user;
  auth.status = 'authenticated';
  auth.activeOrganizationId = ORGANIZATION_ID;
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/approvals', component: ApprovalsView }],
  });
}

async function mountApprovals(prepare?: () => void): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  prepare?.();
  const router = testRouter();
  await router.push('/approvals');
  await router.isReady();
  const wrapper = mount(ApprovalsView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

interface StubOptions {
  list?: Approval[];
  /** decide is the approval the server returns for a decision. */
  decide?: Approval;
  /** decideError makes the decision fail; conflictList is then served on reload. */
  decideError?: { code: string; message: string; status: number };
  conflictList?: Approval[];
}

/** stubApprovals serves the inbox; its state changes only as the server says. */
function stubApprovals(options: StubOptions = {}): Mock {
  let items = options.list ?? [pendingApproval];
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.endsWith('/decide')) {
      if (options.decideError !== undefined) {
        const failure = options.decideError;
        items = options.conflictList ?? [approvedApproval];
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      const decided = options.decide ?? approvedApproval;
      items = items.map((item) => (item.id === decided.id ? decided : item));
      return Promise.resolve(jsonResponse(decided));
    }
    if (url.includes('/api/v1/approvals')) {
      return Promise.resolve(jsonResponse({ items, next_cursor: null }));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

const DECIDING_PERMISSIONS: Permission[] = ['project.read', 'approval.decide'];
const REVIEW_ONLY_PERMISSIONS: Permission[] = ['project.read'];

beforeEach(() => {
  window.localStorage.clear();
  setCsrfToken(null);
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('ApprovalsView', () => {
  it('renders a pending request with decision controls for a deciding role', async () => {
    vi.stubGlobal('fetch', stubApprovals({}));
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    expect(wrapper.find('[data-testid="approvals-no-permission"]').exists()).toBe(false);
    const row = wrapper.find('[data-testid="approval-apr_pending"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('merge_pr');
    expect(row.text()).toContain('required checks are green and the diff is reviewed');
    expect(row.text()).toContain('requested by');
    expect(row.text()).toContain('usr_requester');
    expect(row.find('[data-testid="approval-state"]').text()).toBe('pending');
    expect(row.text()).toContain('expires');
    expect(row.find('[data-testid="approve-apr_pending"]').exists()).toBe(true);
    expect(row.find('[data-testid="reject-apr_pending"]').exists()).toBe(true);
    // Nothing has been decided yet, so no decision line is shown.
    expect(row.find('[data-testid="approval-decision"]').exists()).toBe(false);
  });

  it('shows the recorded decision for a request that is no longer pending', async () => {
    vi.stubGlobal('fetch', stubApprovals({ list: [approvedApproval, rejectedApproval] }));
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    const approved = wrapper.find('[data-testid="approval-apr_pending"]');
    expect(approved.find('[data-testid="approval-state"]').text()).toBe('approved');
    expect(approved.find('[data-testid="approval-decision"]').text()).toContain('approved by');
    expect(approved.find('[data-testid="approval-decision"]').text()).toContain(
      'verified the diff and the test evidence',
    );
    expect(approved.find('[data-testid="approve-apr_pending"]').exists()).toBe(false);

    const rejected = wrapper.find('[data-testid="approval-apr_rejected"]');
    expect(rejected.find('[data-testid="approval-state"]').text()).toBe('rejected');
    expect(rejected.find('[data-testid="reject-apr_rejected"]').exists()).toBe(false);
  });

  it('explains that deciding is restricted instead of rendering controls', async () => {
    vi.stubGlobal('fetch', stubApprovals({}));
    const wrapper = await mountApprovals(() => signIn('viewer', REVIEW_ONLY_PERMISSIONS));

    const banner = wrapper.find('[data-testid="approvals-no-permission"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('approval.decide');
    expect(banner.text()).toContain('restricted');
    expect(wrapper.find('[data-testid="approve-apr_pending"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="reject-apr_pending"]').exists()).toBe(false);
    // The request itself stays visible for review.
    expect(wrapper.find('[data-testid="approval-apr_pending"]').text()).toContain('merge_pr');
  });

  it('approves after a confirmation and shows the state the server returned', async () => {
    setCsrfToken('csrf_decide');
    const fetchMock = stubApprovals({ decide: approvedApproval });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    await wrapper.find('[data-testid="approve-apr_pending"]').trigger('click');
    await wrapper.find('[data-testid="decision-reason-apr_pending"]').setValue('checked the diff');
    await wrapper.find('[data-testid="decision-confirm"]').trigger('submit');
    await flushPromises();

    const decisionCall = fetchMock.mock.calls.find((call) => String(call[0]).endsWith('/decide'));
    expect(decisionCall).toBeDefined();
    const init = (decisionCall?.[1] ?? {}) as RequestInit;
    expect(String(decisionCall?.[0])).toBe('/api/v1/approvals/apr_pending/decide');
    expect(JSON.parse(String(init.body))).toEqual({
      decision: 'approve',
      reason: 'checked the diff',
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);
    expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf_decide');

    const row = wrapper.find('[data-testid="approval-apr_pending"]');
    expect(row.find('[data-testid="approval-state"]').text()).toBe('approved');
    expect(row.find('[data-testid="approval-decision"]').text()).toContain('approved by usr_admin');
    // The confirmation closes once the server has answered.
    expect(wrapper.find('[data-testid="decision-confirm"]').exists()).toBe(false);
  });

  it('cancels a decision without calling the API', async () => {
    const fetchMock = stubApprovals({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    await wrapper.find('[data-testid="reject-apr_pending"]').trigger('click');
    expect(wrapper.find('[data-testid="decision-confirm"]').exists()).toBe(true);

    await wrapper.find('[data-testid="decision-cancel"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="decision-confirm"]').exists()).toBe(false);
    expect(
      fetchMock.mock.calls.some((call) => (call[1] as RequestInit | undefined)?.method === 'POST'),
    ).toBe(false);
    const state = wrapper
      .find('[data-testid="approval-apr_pending"]')
      .find('[data-testid="approval-state"]');
    expect(state.text()).toBe('pending');
  });

  it('reports a 409 with the server message and shows the state it recorded', async () => {
    setCsrfToken('csrf_decide');
    const fetchMock = stubApprovals({
      decideError: {
        code: 'approval_not_pending',
        message: 'approval is not pending',
        status: 409,
      },
      conflictList: [approvedApproval],
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    await wrapper.find('[data-testid="approve-apr_pending"]').trigger('click');
    await wrapper.find('[data-testid="decision-confirm"]').trigger('submit');
    await flushPromises();

    const banner = wrapper.find('[data-testid="approvals-conflict"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('approval is not pending');
    const row = wrapper.find('[data-testid="approval-apr_pending"]');
    expect(row.find('[data-testid="approval-state"]').text()).toBe('approved');
    expect(row.find('[data-testid="approve-apr_pending"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="decision-confirm"]').exists()).toBe(false);

    await wrapper.find('[data-testid="approvals-conflict-dismiss"]').trigger('click');
    expect(wrapper.find('[data-testid="approvals-conflict"]').exists()).toBe(false);
  });

  it('shows an empty state when nothing is waiting', async () => {
    vi.stubGlobal('fetch', stubApprovals({ list: [] }));
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    expect(wrapper.find('[data-testid="empty-approvals"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="empty-approvals"]').text()).toContain(
      'No approvals are waiting.',
    );
    expect(wrapper.find('[data-testid="approvals-list"]').exists()).toBe(false);
  });

  it('reports a load failure in a banner and reloads on retry', async () => {
    let failed = true;
    const fetchMock = vi.fn((input: string): Promise<Response> => {
      const url = String(input);
      if (!url.includes('/api/v1/approvals')) {
        return Promise.reject(new Error(`unexpected request to ${url}`));
      }
      return failed
        ? Promise.resolve(errorResponse('internal_error', 'approval service unavailable', 500))
        : Promise.resolve(jsonResponse({ items: [pendingApproval], next_cursor: null }));
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    const banner = wrapper.find('[data-testid="approvals-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('approval service unavailable');
    expect(wrapper.find('[data-testid="approvals-list"]').exists()).toBe(false);

    failed = false;
    await wrapper.find('[data-testid="approvals-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="approvals-error"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="approval-apr_pending"]').exists()).toBe(true);
  });

  it('reloads the inbox from the manual refresh button', async () => {
    const fetchMock = stubApprovals({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountApprovals(() => signIn('owner', DECIDING_PERMISSIONS));

    const readsBefore = fetchMock.mock.calls.length;
    await wrapper.find('[data-testid="approvals-refresh"]').trigger('click');
    await flushPromises();

    expect(fetchMock.mock.calls.length).toBe(readsBefore + 1);
    expect(wrapper.find('[data-testid="approval-apr_pending"]').exists()).toBe(true);
  });
});
