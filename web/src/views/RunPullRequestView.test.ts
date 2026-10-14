import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import RunPullRequestView from './RunPullRequestView.vue';
import { setCsrfToken } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import type { CurrentUser, Permission, PullRequestView } from '@/api/types';

const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';
const PR_URL = 'https://github.com/acme/app/pull/101';

/** pullRequest is the tracked pull request the inspector renders verbatim. */
const pullRequest: PullRequestView = {
  id: 'pr_1a2b3c4d5e6f7g8h9i0j',
  run_id: RUN_ID,
  number: 101,
  url: PR_URL,
  repo_owner: 'acme',
  repo_name: 'app',
  head_branch: 'agent/run-pr',
  base_branch: 'main',
  title: 'm13: bound the pagination limit',
  body: '## Summary\n\nBound the limit.\nCloses #42',
  issue_number: 42,
  state: 'open',
  checks: [
    {
      name: 'verify',
      status: 'completed',
      conclusion: 'success',
      details_url: 'https://ci.example/1',
    },
    { name: 'secret scan', status: 'completed', conclusion: 'success' },
  ],
  reviews: [{ author: 'maintainer', state: 'approved', commit_id: 'head-sha-headsha' }],
  approval_count: 1,
  merge_state: 'clean',
  merge_sha: '',
  branch_deleted: false,
  commits: [
    {
      sha: 'c50681beef9191536ea399e940d85331b92c5d43',
      message: 'm13: bound the limit',
      author: { name: 'yasserrmd', email: 'arafath.yasser@gmail.com' },
      committer: { name: 'yasserrmd', email: 'arafath.yasser@gmail.com' },
      files: ['internal/api/list.go'],
    },
  ],
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T11:00:00Z',
};

/** mergedRequest is the state after the merge action. */
const mergedRequest: PullRequestView = {
  ...pullRequest,
  state: 'merged',
  merge_sha: 'merge-sha-101',
  branch_deleted: true,
};

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
  const error: Record<string, unknown> = { code, message, request_id: 'req_pr_view' };
  if (details !== undefined) error['details'] = details;
  return jsonResponse({ error }, status);
}

interface StubOptions {
  pullRequest?: PullRequestView;
  prError?: { code: string; message: string; status: number };
  mergeError?: {
    code: string;
    message: string;
    status: number;
    details?: Record<string, unknown>;
  };
  mergeResult?: Record<string, unknown>;
  abandonResult?: PullRequestView;
}

/** stubApi serves the tracked pull request and the two lifecycle mutations. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.endsWith('/abandon')) {
      return Promise.resolve(
        jsonResponse(options.abandonResult ?? { ...pullRequest, state: 'abandoned' }),
      );
    }
    if (url.endsWith('/merge')) {
      if (options.mergeError !== undefined) {
        const failure = options.mergeError;
        return Promise.resolve(
          errorResponse(failure.code, failure.message, failure.status, failure.details),
        );
      }
      return Promise.resolve(
        jsonResponse(
          options.mergeResult ?? {
            run_id: RUN_ID,
            pull_request: mergedRequest,
            merge_action: {
              id: 'mrg_1',
              run_id: RUN_ID,
              authorized_by: 'maintainer@example.test',
              method: 'merge',
              merge_sha: 'merge-sha-101',
              succeeded: true,
            },
            merge_sha: 'merge-sha-101',
            already_merged: false,
          },
        ),
      );
    }
    if (url.endsWith('/pr')) {
      if (options.prError !== undefined) {
        const failure = options.prError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.pullRequest ?? pullRequest));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
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
    routes: [{ path: '/runs/:runId/pull-request', component: RunPullRequestView }],
  });
}

async function mountPullRequest(
  permissions: Permission[] = ['project.read', 'run.execute', 'merge.execute'],
): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  signIn(permissions);
  const router = testRouter();
  await router.push(`/runs/${RUN_ID}/pull-request`);
  await router.isReady();
  const wrapper = mount(RunPullRequestView, { global: { plugins: [pinia, router] } });
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

describe('RunPullRequestView', () => {
  it('renders the exact GitHub link, checks, reviews and commits', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPullRequest();

    const link = wrapper.find('[data-testid="pr-link"]');
    expect(link.attributes('href')).toBe(PR_URL);
    expect(link.text()).toBe('acme/app#101');
    expect(wrapper.find('[data-testid="pr-state"]').text()).toBe('open');
    expect(wrapper.find('[data-testid="check-verify"] [data-testid="check-status"]').text()).toBe(
      'passed',
    );
    expect(wrapper.find('[data-testid="pr-approvals"]').text()).toBe('1');
    expect(wrapper.find('[data-testid^="commit-c50681beef91"]').exists()).toBe(true);
    expect(
      wrapper.find('[data-testid^="commit-c50681beef91"] [data-testid="commit-author"]').text(),
    ).toContain('yasserrmd');
    // The unmerged branch is reported as kept, not as deleted.
    expect(wrapper.find('[data-testid="pr-branch-deleted"]').text()).toBe('no');
  });

  it('keeps a check GitHub has not reported as pending, never as passed', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        pullRequest: {
          ...pullRequest,
          checks: [{ name: 'verify', status: 'in_progress' }],
        },
      }),
    );
    const wrapper = await mountPullRequest();

    expect(wrapper.find('[data-testid="check-verify"] [data-testid="check-status"]').text()).toBe(
      'pending',
    );
    expect(wrapper.find('[data-testid="pr-merge-submit"]').attributes('disabled')).toBeDefined();
  });

  it('reports a failed check as failed', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        pullRequest: {
          ...pullRequest,
          checks: [{ name: 'verify', status: 'completed', conclusion: 'failure' }],
        },
      }),
    );
    const wrapper = await mountPullRequest();

    expect(wrapper.find('[data-testid="check-verify"] [data-testid="check-status"]').text()).toBe(
      'failed',
    );
  });

  it('merges under a named human authorizer and reports the merge SHA', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountPullRequest();

    await wrapper
      .find('[data-testid="pr-merge-authorizer-input"]')
      .setValue('maintainer@example.test');
    await wrapper.find('[data-testid="pr-merge-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="pr-merge-result"]').text()).toContain('Merge applied');
    expect(wrapper.find('[data-testid="pr-merge-result-sha"]').text()).toBe('merge-sha-101');
    expect(wrapper.find('[data-testid="pr-merge-branch-deleted"]').text()).toBe('branch deleted');
    expect(wrapper.find('[data-testid="pr-state"]').text()).toBe('merged');
    const [, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    const body = JSON.parse(String(init.body)) as { authorized_by: string; method: string };
    expect(body.authorized_by).toBe('maintainer@example.test');
    expect(body.method).toBe('merge');
  });

  it('requires an authorizer before the merge is requested', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPullRequest();

    await wrapper.find('[data-testid="pr-merge-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="pr-merge-authorizer-error"]').text()).toContain(
      'permitted human',
    );
    expect(wrapper.find('[data-testid="pr-merge-result"]').exists()).toBe(false);
  });

  it('reports a merge gate refusal with every recorded reason', async () => {
    const fetchMock = stubApi({
      pullRequest: {
        ...pullRequest,
        checks: [{ name: 'verify', status: 'in_progress' }],
      },
      mergeError: {
        code: 'pr_merge_refused',
        message: 'required GitHub check verify is still pending',
        status: 409,
        details: {
          stage: 'merge',
          reasons: [
            'required GitHub check verify is still pending',
            'no human authorized this merge; the authorizer was empty',
          ],
        },
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountPullRequest();

    await wrapper
      .find('[data-testid="pr-merge-authorizer-input"]')
      .setValue('maintainer@example.test');
    // The merge is disabled while GitHub has not finished the check, so no
    // doomed request is fired and the pending state is reported as pending.
    expect(wrapper.find('[data-testid="pr-merge-submit"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-testid="check-verify"] [data-testid="check-status"]').text()).toBe(
      'pending',
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('abandons the change and keeps the branch', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountPullRequest();

    await wrapper
      .find('[data-testid="pr-abandon-reason-input"]')
      .setValue('three attempts produced no test delta');
    await wrapper.find('[data-testid="pr-abandon-submit"]').trigger('submit');
    await flushPromises();

    const [, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      reason: 'three attempts produced no test delta',
    });
    expect(wrapper.find('[data-testid="pr-state"]').text()).toBe('abandoned');
  });

  it('requires a reason before the change is abandoned', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPullRequest();

    await wrapper.find('[data-testid="pr-abandon-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="pr-abandon-reason-error"]').text()).toContain(
      'a reason is required',
    );
    expect(wrapper.find('[data-testid="pr-state"]').text()).toBe('open');
  });

  it('hides the merge action from a member without merge.execute', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountPullRequest(['project.read', 'run.execute']);

    expect(wrapper.find('[data-testid="pr-merge-no-permission"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="pr-merge-submit"]').attributes('disabled')).toBeDefined();
    // Reading the checks and reviews stays available to any member.
    expect(wrapper.find('[data-testid="pr-checks"]').exists()).toBe(true);
  });

  it('offers no action once the pull request is merged', async () => {
    vi.stubGlobal('fetch', stubApi({ pullRequest: mergedRequest }));
    const wrapper = await mountPullRequest();

    expect(wrapper.find('[data-testid="pr-merge-terminal"]').text()).toContain('already merged');
    expect(wrapper.find('[data-testid="pr-merge-submit"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-testid="pr-abandon-submit"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-testid="pr-branch-deleted"]').text()).toBe('yes');
  });

  it('reports an unreadable pull request instead of an empty screen', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({ prError: { code: 'not_found', message: 'resource not found', status: 404 } }),
    );
    const wrapper = await mountPullRequest();

    expect(wrapper.find('[data-testid="pr-error-code"]').text()).toBe('not_found');
    expect(wrapper.find('[data-testid="pr-empty"]').exists()).toBe(true);
  });
});
