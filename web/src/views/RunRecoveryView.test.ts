import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import RunRecoveryView from './RunRecoveryView.vue';
import { setCsrfToken } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import type { CurrentUser, Permission, RecoveryView } from '@/api/types';

const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';
const CHECKPOINT_ID = 'snp_1a2b3c4d5e6f7g8h9i0j';

/** recoveryView is the server projection the inspector renders verbatim. */
const recoveryView: RecoveryView = {
  run_id: RUN_ID,
  run_state: 'testing',
  budgets: [
    { class: 'test_failure', allowed: 5, used: 2, remaining: 3, exhausted: false },
    { class: 'rate_limit', allowed: 5, used: 5, remaining: 0, exhausted: true },
  ],
  attempts: [
    {
      run_id: RUN_ID,
      number: 1,
      kind: 'patched',
      fingerprint: 'fp_first',
      patch_fingerprint: 'diff_first',
      failing_count: 3,
      verdict: 'red',
      note: 'repair attempt targets 3 failing diagnostic group(s) then reruns go.test',
    },
    {
      run_id: RUN_ID,
      number: 2,
      kind: 'patched',
      fingerprint: 'fp_second',
      patch_fingerprint: 'diff_second',
      failing_count: 1,
      verdict: 'red',
      note: 'repair attempt targets 1 failing diagnostic group(s) then reruns go.test',
    },
  ],
  timeline: [
    {
      id: 'rev_scheduled_1',
      run_id: RUN_ID,
      kind: 'retry_scheduled',
      attempt: 1,
      class: 'test_failure',
      reason: 'recovery scheduled the next bounded attempt',
      evidence: { delay_ms: 0, budget: { class: 'test_failure', allowed: 5, used: 1 } },
      actor_type: 'user',
      created_at: '2026-10-10T10:05:00Z',
    },
    {
      id: 'rev_scheduled_2',
      run_id: RUN_ID,
      kind: 'retry_scheduled',
      attempt: 2,
      class: 'test_failure',
      reason: 'recovery scheduled the next bounded attempt',
      evidence: { delay_ms: 500, budget: { class: 'test_failure', allowed: 5, used: 2 } },
      actor_type: 'user',
      created_at: '2026-10-10T10:20:00Z',
    },
  ],
  receipts: [
    {
      id: 'rcp_1',
      run_id: RUN_ID,
      operation: 'restore_checkpoint',
      effect_id: 'effect_alpha',
      status: 'applied',
      created_at: '2026-10-10T09:00:00Z',
    },
  ],
  checkpoints: [
    {
      id: CHECKPOINT_ID,
      run_id: RUN_ID,
      verdict: 'green',
      file_count: 12,
      created_at: '2026-10-10T08:00:00Z',
    },
  ],
  next_attempt: 3,
};

/** blockedView is the state after the server stops the loop. */
const blockedView: RecoveryView = {
  ...recoveryView,
  run_state: 'blocked',
  timeline: [
    ...recoveryView.timeline,
    {
      id: 'rev_blocked_1',
      run_id: RUN_ID,
      kind: 'loop_detected',
      attempt: 3,
      class: 'test_failure',
      reason: 'failure signature fp_first recurred on attempt 3 without changing',
      evidence: { loop: { kind: 'unchanged_error', attempts: 3, fingerprint: 'fp_first' } },
      actor_type: 'user',
      created_at: '2026-10-10T11:00:00Z',
    },
  ],
};

const recoveredView: RecoveryView = { ...recoveryView, run_state: 'editing' };

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(
  code: string,
  message: string,
  status: number,
  details?: Record<string, unknown>,
): Response {
  const error: Record<string, unknown> = { code, message, request_id: 'req_recovery_view' };
  if (details !== undefined) error['details'] = details;
  return jsonResponse({ error }, status);
}

/** loopDetails is the payload the API answers 409 with. */
const loopDetails = {
  loop: {
    kind: 'identical_patch',
    attempts: 2,
    fingerprint: 'diff_second',
    evidence: 'diff diff_second was already applied; applying it again would repeat the change',
  },
  attempt: 2,
  fingerprint: 'diff_second',
};

interface StubOptions {
  view?: RecoveryView;
  viewError?: { code: string; message: string; status: number };
  recoverError?: {
    code: string;
    message: string;
    status: number;
    details?: Record<string, unknown>;
  };
  recoverResponse?: Record<string, unknown>;
  rollbackError?: { code: string; message: string; status: number };
  rollbackResponse?: Record<string, unknown>;
}

/** stubApi serves the recovery read and the two mutations for one run. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    if (url.endsWith('/rollback')) {
      if (options.rollbackError !== undefined) {
        const failure = options.rollbackError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.rollbackResponse ?? { duplicate: false }));
    }
    if (url.endsWith('/recover')) {
      if (options.recoverError !== undefined) {
        const failure = options.recoverError;
        return Promise.resolve(
          errorResponse(failure.code, failure.message, failure.status, failure.details),
        );
      }
      return Promise.resolve(jsonResponse(options.recoverResponse ?? { attempt: 3 }));
    }
    if (url.endsWith('/recovery')) {
      if (options.viewError !== undefined) {
        const failure = options.viewError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.view ?? recoveryView));
    }
    return Promise.reject(new Error(`unexpected request to ${url} (${init?.method ?? 'GET'})`));
  });
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
    routes: [{ path: '/runs/:runId/recovery', component: RunRecoveryView }],
  });
}

async function mountRecovery(
  permissions: Permission[] = ['project.read', 'run.execute'],
): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  signIn(permissions);
  const router = testRouter();
  await router.push(`/runs/${RUN_ID}/recovery`);
  await router.isReady();
  const wrapper = mount(RunRecoveryView, { global: { plugins: [pinia, router] } });
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

describe('RunRecoveryView', () => {
  it('renders the journalled timeline, budgets, attempts and checkpoints', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountRecovery();

    expect(wrapper.find('[data-testid="recovery-run-state"]').text()).toBe('testing');
    expect(wrapper.find('[data-testid="recovery-next-attempt"]').text()).toBe('next attempt 3');
    expect(wrapper.findAll('[data-testid^="event-retry_scheduled"]')).toHaveLength(2);
    expect(wrapper.find('[data-testid="budget-test_failure"]').text()).toContain('2 of 5 used');
    // An exhausted budget is shown as exhausted, never hidden.
    expect(wrapper.find('[data-testid="budget-rate_limit"]').text()).toContain('exhausted');
    expect(wrapper.find('[data-testid="attempt-2"] [data-testid="attempt-failing"]').text()).toBe(
      '1',
    );
    expect(wrapper.find(`[data-testid="checkpoint-${CHECKPOINT_ID}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="receipt-effect_alpha"]`).exists()).toBe(true);
  });

  it('renders the blocked panel with the evidence the server journalled', async () => {
    vi.stubGlobal('fetch', stubApi({ view: blockedView }));
    const wrapper = await mountRecovery();

    const blocked = wrapper.find('[data-testid="recovery-blocked"]');
    expect(blocked.exists()).toBe(true);
    expect(blocked.find('[data-testid="recovery-blocked-kind"]').text()).toBe('loop_detected');
    expect(wrapper.find('[data-testid="recovery-blocked-reason"]').text()).toContain(
      'failure signature',
    );
    expect(blocked.find('[data-testid="recovery-blocked-action"]').text()).toBe(
      'roll back to a checkpoint',
    );
  });

  it('reports a 409 loop refusal with the server evidence', async () => {
    const fetchMock = stubApi({
      recoverError: {
        code: 'recovery_loop_detected',
        message: 'diff diff_second was already applied',
        status: 409,
        details: loopDetails,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountRecovery();

    await wrapper.find('[data-testid="recovery-reason-input"]').setValue('retry the same fix');
    await wrapper.find('[data-testid="recovery-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="recovery-error-code"]').text()).toBe(
      'recovery_loop_detected',
    );
    expect(wrapper.find('[data-testid="recovery-error"]').text()).toContain(
      'diff diff_second was already applied',
    );
    const [, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    const body = JSON.parse(String(init.body)) as { reason: string; class?: string };
    expect(body.reason).toBe('retry the same fix');
    expect(body.class).toBe('test_failure');
  });

  it('reports a 429 budget refusal instead of hiding it', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        recoverError: {
          code: 'recovery_retry_exhausted',
          message: 'the bounded retry budget for test_failure failures is spent after 5 attempts',
          status: 429,
          details: { budget: { class: 'test_failure', allowed: 5, used: 5, exhausted: true } },
        },
      }),
    );
    const wrapper = await mountRecovery();

    await wrapper.find('[data-testid="recovery-reason-input"]').setValue('one more attempt');
    await wrapper.find('[data-testid="recovery-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="recovery-error-code"]').text()).toBe(
      'recovery_retry_exhausted',
    );
    expect(wrapper.find('[data-testid="recovery-error"]').text()).toContain('budget');
  });

  it('requires a reason before an attempt is requested', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountRecovery();

    await wrapper.find('[data-testid="recovery-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="recovery-reason-error"]').text()).toContain(
      'a reason is required',
    );
    expect(wrapper.find('[data-testid="recovery-error"]').exists()).toBe(false);
  });

  it('reports a replayed rollback as a duplicate', async () => {
    const fetchMock = stubApi({
      rollbackResponse: {
        run_id: RUN_ID,
        checkpoint_id: CHECKPOINT_ID,
        restored_files: [],
        drift_before: 1,
        drift_after: 0,
        duplicate: true,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountRecovery();

    await wrapper.find('[data-testid="recovery-rollback-reason-input"]').setValue('restore again');
    await wrapper.find('[data-testid="recovery-rollback-submit"]').trigger('submit');
    await flushPromises();

    // The initial read, the restore, and the re-read the store performs after
    // it so the timeline reflects the server's record rather than a local claim.
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const [, restoreInit] = fetchMock.mock.calls[1] as [string, RequestInit];
    const body = JSON.parse(String(restoreInit.body)) as { effect_id: string; reason: string };
    expect(body.effect_id).toMatch(/^effect_/);
    expect(body.reason).toBe('restore again');

    const banner = wrapper.find('[data-testid="recovery-rollback-result"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('replayed');
    expect(wrapper.find('[data-testid="recovery-rollback-duplicate"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="recovery-rollback-checkpoint"]').text()).toBe(CHECKPOINT_ID);
  });

  it('selects a checkpoint before the rollback is sent', async () => {
    const fetchMock = stubApi({
      rollbackResponse: {
        run_id: RUN_ID,
        checkpoint_id: CHECKPOINT_ID,
        restored_files: ['main.go'],
        drift_before: 2,
        drift_after: 0,
        duplicate: false,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountRecovery();

    await wrapper
      .find(`[data-testid="checkpoint-${CHECKPOINT_ID}"] [data-testid="checkpoint-select"]`)
      .trigger('click');
    await wrapper
      .find('[data-testid="recovery-rollback-reason-input"]')
      .setValue('revert to green');
    await wrapper.find('[data-testid="recovery-rollback-submit"]').trigger('submit');
    await flushPromises();

    const [, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    const body = JSON.parse(String(init.body)) as { checkpoint_id?: string };
    expect(body.checkpoint_id).toBe(CHECKPOINT_ID);
  });

  it('blocks the rollback action when no checkpoint exists', async () => {
    vi.stubGlobal('fetch', stubApi({ view: { ...recoveryView, checkpoints: [] } }));
    const wrapper = await mountRecovery();

    expect(wrapper.find('[data-testid="recovery-checkpoints-none"]').exists()).toBe(true);
    expect(
      wrapper.find('[data-testid="recovery-rollback-submit"]').attributes('disabled'),
    ).toBeDefined();
    expect(wrapper.find('[data-testid="recovery-no-checkpoint"]').exists()).toBe(true);
  });

  it('hides the actions from a member without run.execute', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountRecovery(['project.read']);

    expect(wrapper.find('[data-testid="recovery-submit"]').attributes('disabled')).toBeDefined();
    expect(
      wrapper.find('[data-testid="recovery-rollback-submit"]').attributes('disabled'),
    ).toBeDefined();
    // Reading the timeline stays available: the permission only gates actions.
    expect(wrapper.find('[data-testid="recovery-timeline"]').exists()).toBe(true);
  });

  it('offers no attempt once the run is terminal', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        view: { ...recoveryView, run_state: 'completed' },
        recoverResponse: { attempt: 3 },
      }),
    );
    const wrapper = await mountRecovery();

    expect(wrapper.find('[data-testid="recovery-terminal-note"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="recovery-submit"]').attributes('disabled')).toBeDefined();
  });

  it('reports an unreadable recovery state instead of an empty screen', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        viewError: { code: 'not_found', message: 'resource not found', status: 404 },
      }),
    );
    const wrapper = await mountRecovery();

    expect(wrapper.find('[data-testid="recovery-error-code"]').text()).toBe('not_found');
    expect(wrapper.find('[data-testid="recovery-empty"]').exists()).toBe(true);
  });

  it('reloads the journal from the server', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountRecovery();

    await wrapper.find('[data-testid="recovery-reload"]').trigger('click');
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [url] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toContain(`/api/v1/runs/${RUN_ID}/recovery`);
  });

  it('keeps the recovered run visible without inventing a verdict', async () => {
    vi.stubGlobal('fetch', stubApi({ view: recoveredView }));
    const wrapper = await mountRecovery();

    expect(wrapper.find('[data-testid="recovery-run-state"]').text()).toBe('editing');
    expect(wrapper.find('[data-testid="recovery-blocked"]').exists()).toBe(false);
  });
});
