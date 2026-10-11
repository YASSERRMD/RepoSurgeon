import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import MembersView from './MembersView.vue';
import { useAuthStore } from '@/stores/auth';
import type { CurrentUser, Member, MemberPage, Permission, Role } from '@/api/types';

const ORGANIZATION_ID = 'org_acme';

const owner: Member = {
  id: 'mem_owner',
  user_id: 'usr_1a2b3c4d5e6f7g8h9i0j',
  email: 'owner@example.com',
  display_name: 'Owner One',
  role: 'owner',
  created_at: '2026-10-10T10:00:00Z',
};

const viewer: Member = {
  id: 'mem_viewer',
  user_id: 'usr_9i8h7g6f5e4d3c2b1a0z',
  email: 'viewer@example.com',
  display_name: 'Viewer Two',
  role: 'viewer',
  created_at: '2026-10-11T10:00:00Z',
};

function page(items: Member[]): MemberPage {
  return { items, next_cursor: null };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_members_test' } }, status);
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
    routes: [
      { path: '/members', component: MembersView },
      { path: '/organizations', component: { template: '<div />' } },
    ],
  });
}

async function mountMembers(prepare?: () => void): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  prepare?.();
  const router = testRouter();
  await router.push('/members');
  await router.isReady();
  const wrapper = mount(MembersView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

/** stubMembers serves the member collection and the member mutations. */
function stubMembers(options: {
  list?: MemberPage;
  patchStatus?: number;
  patchMessage?: string;
  deleteStatus?: number;
}): Mock {
  return vi.fn((input: string, init?: RequestInit): Promise<Response> => {
    const method = init?.method ?? 'GET';
    if (String(input).includes('/api/v1/members')) {
      if (method === 'PATCH') {
        return options.patchStatus === 409
          ? Promise.resolve(errorResponse('conflict', options.patchMessage ?? 'conflict', 409))
          : Promise.resolve(jsonResponse({ ...owner, role: 'reviewer' }));
      }
      if (method === 'DELETE') {
        return options.deleteStatus === 409
          ? Promise.resolve(errorResponse('conflict', 'the last owner cannot be removed', 409))
          : Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === 'POST') {
        return Promise.resolve(jsonResponse({ ...viewer, id: 'mem_new' }, 201));
      }
      return Promise.resolve(jsonResponse(options.list ?? page([owner, viewer])));
    }
    return Promise.reject(new Error(`unexpected request to ${String(input)}`));
  });
}

const OWNER_PERMISSIONS: Permission[] = ['project.read', 'member.admin', 'credential.admin'];
const VIEWER_PERMISSIONS: Permission[] = ['project.read'];

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('MembersView', () => {
  it('lists the members of the active organization with role badges', async () => {
    vi.stubGlobal('fetch', stubMembers({}));
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));

    const rows = wrapper.findAll('[data-testid^="member-mem_"]');
    expect(rows).toHaveLength(2);
    expect(wrapper.find('[data-testid="member-mem_owner"]').text()).toContain('owner@example.com');
    expect(wrapper.find('[data-testid="member-mem_viewer"]').text()).toContain(
      'viewer@example.com',
    );
    expect(wrapper.find('[data-testid="member-mem_owner"]').text()).toContain('owner');
    expect(wrapper.findAll('[data-testid="member-role-badge"]')[1].text()).toBe('viewer');
    expect(wrapper.find('[data-testid="members-need-organization"]').exists()).toBe(false);
  });

  it('explains that an active organization is required', async () => {
    vi.stubGlobal('fetch', stubMembers({}));
    const auth = useAuthStore();
    auth.user = {
      id: 'usr_1a2b3c4d5e6f7g8h9i0j',
      email: 'owner@example.com',
      organizations: [],
    };
    auth.status = 'authenticated';
    auth.activeOrganizationId = null;

    const wrapper = await mountMembers();

    expect(wrapper.find('[data-testid="members-need-organization"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="invite-form"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="members-list"]').exists()).toBe(false);
  });

  it('shows the invite form to a member with member.admin', async () => {
    vi.stubGlobal('fetch', stubMembers({}));
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));

    expect(wrapper.find('[data-testid="invite-form"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="members-no-permission"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="role-mem_viewer"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="remove-mem_viewer"]').exists()).toBe(true);
  });

  it('hides the form and explains the missing permission for a viewer', async () => {
    vi.stubGlobal('fetch', stubMembers({}));
    const wrapper = await mountMembers(() => signIn('viewer', VIEWER_PERMISSIONS));

    expect(wrapper.find('[data-testid="invite-form"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="role-mem_viewer"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="remove-mem_viewer"]').exists()).toBe(false);
    const notice = wrapper.find('[data-testid="members-no-permission"]');
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toContain('member.admin');
    // The list itself is still visible: reading members is allowed.
    expect(wrapper.findAll('[data-testid^="member-mem_"]')).toHaveLength(2);
  });

  it('invites a member with an idempotency key and the chosen role', async () => {
    const fetchMock = stubMembers({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    await wrapper.find('[data-testid="invite-email"]').setValue('teammate@example.com');
    await wrapper.find('[data-testid="invite-role"]').setValue('reviewer');
    await wrapper.find('[data-testid="invite-submit"]').trigger('submit');
    await flushPromises();

    const inviteCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'POST',
    );
    expect(inviteCall).toBeDefined();
    const init = (inviteCall?.[1] ?? {}) as RequestInit;
    expect(String(inviteCall?.[0])).toBe('/api/v1/members');
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: ORGANIZATION_ID,
      email: 'teammate@example.com',
      role: 'reviewer',
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);
    expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined();
    expect((wrapper.find('[data-testid="invite-email"]').element as HTMLInputElement).value).toBe(
      '',
    );
  });

  it('rejects an invalid invite email before calling the API', async () => {
    const fetchMock = stubMembers({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    await wrapper.find('[data-testid="invite-email"]').setValue('not-an-email');
    await wrapper.find('[data-testid="invite-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="invite-email-error"]').text()).toContain('valid email');
    expect(fetchMock.mock.calls.some((call) => (call[1] as RequestInit)?.method === 'POST')).toBe(
      false,
    );
  });

  it('explains a 409 when a role change is rejected', async () => {
    vi.stubGlobal(
      'fetch',
      stubMembers({ patchStatus: 409, patchMessage: 'a member cannot escalate their own role' }),
    );
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    await wrapper.find('[data-testid="role-mem_viewer"]').setValue('owner');
    await flushPromises();

    const banner = wrapper.find('[data-testid="members-conflict"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('a member cannot escalate their own role');
  });

  it('explains a 409 when the last owner cannot be removed', async () => {
    vi.stubGlobal('fetch', stubMembers({ deleteStatus: 409 }));
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    await wrapper.find('[data-testid="remove-mem_owner"]').trigger('click');
    await flushPromises();

    const banner = wrapper.find('[data-testid="members-conflict"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('the last owner cannot be removed');
    // The member stays in the list because the server rejected the removal.
    expect(wrapper.find('[data-testid="member-mem_owner"]').exists()).toBe(true);
  });

  it('removes a member with DELETE when the API accepts it', async () => {
    const fetchMock = stubMembers({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    await wrapper.find('[data-testid="remove-mem_viewer"]').trigger('click');
    await flushPromises();

    const deleteCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'DELETE',
    );
    expect(deleteCall).toBeDefined();
    expect(String(deleteCall?.[0])).toBe('/api/v1/members/mem_viewer');
    expect(wrapper.find('[data-testid="member-mem_viewer"]').exists()).toBe(false);
  });

  it('shows an error banner and retries when the list cannot be loaded', async () => {
    const fetchMock = vi.fn().mockImplementation((input: string) => {
      if (String(input).includes('/api/v1/members')) {
        return Promise.reject(new Error('ECONNREFUSED'));
      }
      return Promise.reject(new Error(`unexpected request to ${String(input)}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountMembers(() => signIn('owner', OWNER_PERMISSIONS));
    expect(wrapper.find('[data-testid="members-error"]').text()).toContain('ECONNREFUSED');

    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(page([owner]))));
    await wrapper.find('[data-testid="members-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="members-error"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-testid^="member-mem_"]')).toHaveLength(1);
  });
});
