import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import CredentialsView from './CredentialsView.vue';
import { useAuthStore } from '@/stores/auth';
import type { Credential, CredentialPage, CurrentUser, Permission, Role } from '@/api/types';

const ORGANIZATION_ID = 'org_acme';

/** The contract never returns a value; the fixture contains metadata only. */
const credential: Credential = {
  id: 'crd_stepfun',
  organization_id: ORGANIZATION_ID,
  kind: 'stepfun',
  label: 'production keys',
  status: 'active',
  key_version: 2,
  fingerprint: 'a1b2c3d4e5f60718a9b0c1d2e3f40516',
  last_verified_at: null,
  created_at: '2026-10-10T10:00:00Z',
};

const verifiedCredential: Credential = {
  ...credential,
  id: 'crd_github',
  kind: 'github_app',
  label: 'deploy bot',
  status: 'revoked',
  key_version: 5,
  last_verified_at: '2026-10-11T12:30:00Z',
};

const SECRET_VALUE = 'sf-live-xxxxxxxxxxxxxxxx';

function page(items: Credential[]): CredentialPage {
  return { items, next_cursor: null };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

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
    routes: [{ path: '/credentials', component: CredentialsView }],
  });
}

async function mountCredentials(prepare?: () => void): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  prepare?.();
  const router = testRouter();
  await router.push('/credentials');
  await router.isReady();
  const wrapper = mount(CredentialsView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

/** stubCredentials serves the credential collection and its mutations. */
function stubCredentials(options: {
  list?: CredentialPage;
  rotated?: Credential;
  created?: Credential;
}): Mock {
  return vi.fn((input: string, init?: RequestInit): Promise<Response> => {
    const method = init?.method ?? 'GET';
    const url = String(input);
    if (url.includes('/api/v1/credentials')) {
      if (method === 'POST') {
        return url.endsWith('/rotate')
          ? Promise.resolve(jsonResponse(options.rotated ?? { ...credential, key_version: 3 }))
          : Promise.resolve(jsonResponse(options.created ?? credential, 201));
      }
      if (method === 'DELETE') {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(jsonResponse(options.list ?? page([credential, verifiedCredential])));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

const ADMIN_PERMISSIONS: Permission[] = ['project.read', 'credential.admin'];
const VIEWER_PERMISSIONS: Permission[] = ['project.read'];

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('CredentialsView', () => {
  it('renders credential metadata and never a value', async () => {
    vi.stubGlobal('fetch', stubCredentials({}));
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));

    expect(wrapper.findAll('[data-testid^="credential-crd_"]')).toHaveLength(2);
    const row = wrapper.find('[data-testid="credential-crd_stepfun"]');
    expect(row.text()).toContain('production keys');
    expect(row.text()).toContain('StepFun');
    expect(row.find('[data-testid="credential-status"]').text()).toBe('active');
    expect(row.text()).toContain('key v2');
    expect(row.text()).toContain('a1b2c3d4e5f6');
    expect(row.find('[data-testid="credential-verified"]').text()).toBe('not verified yet');

    const verified = wrapper.find('[data-testid="credential-crd_github"]');
    expect(verified.find('[data-testid="credential-status"]').text()).toBe('revoked');
    expect(verified.find('[data-testid="credential-verified"]').text()).toContain('verified');

    // No credential secret is rendered anywhere in the list or the forms.
    expect(wrapper.html()).not.toContain(SECRET_VALUE);
  });

  it('explains the missing credential.admin permission instead of the forms', async () => {
    vi.stubGlobal('fetch', stubCredentials({}));
    const wrapper = await mountCredentials(() => signIn('viewer', VIEWER_PERMISSIONS));

    expect(wrapper.find('[data-testid="credentials-no-permission"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="credentials-no-permission"]').text()).toContain(
      'credential.admin',
    );
    expect(wrapper.find('[data-testid="create-credential-form"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="credential-value"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="rotate-crd_stepfun"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="revoke-crd_stepfun"]').exists()).toBe(false);
    // Metadata is still readable.
    expect(wrapper.findAll('[data-testid^="credential-crd_"]')).toHaveLength(2);
  });

  it('stores a credential with the value only in the request body', async () => {
    const fetchMock = stubCredentials({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));
    await wrapper.find('[data-testid="credential-kind"]').setValue('stepfun');
    await wrapper.find('[data-testid="credential-label"]').setValue('production keys');
    await wrapper.find('[data-testid="credential-value"]').setValue(SECRET_VALUE);
    await wrapper.find('[data-testid="create-credential-submit"]').trigger('submit');
    await flushPromises();

    const postCall = fetchMock.mock.calls.find((call) => {
      const init = call[1] as RequestInit | undefined;
      return init?.method === 'POST' && String(call[0]).endsWith('/api/v1/credentials');
    });
    expect(postCall).toBeDefined();
    const init = (postCall?.[1] ?? {}) as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: ORGANIZATION_ID,
      kind: 'stepfun',
      label: 'production keys',
      value: SECRET_VALUE,
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);
    expect(wrapper.html()).not.toContain(SECRET_VALUE);
  });

  it('rejects a value shorter than the contract minimum', async () => {
    const fetchMock = stubCredentials({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));
    await wrapper.find('[data-testid="credential-label"]').setValue('short');
    await wrapper.find('[data-testid="credential-value"]').setValue('abc123');
    await wrapper.find('[data-testid="create-credential-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="credential-value-error"]').text()).toContain(
      'at least 8 characters',
    );
    expect(fetchMock.mock.calls.some((call) => (call[1] as RequestInit)?.method === 'POST')).toBe(
      false,
    );
  });

  it('rotates a credential at the rotate path', async () => {
    const fetchMock = stubCredentials({ rotated: { ...credential, key_version: 7 } });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));
    await wrapper.find('[data-testid="rotate-crd_stepfun"]').trigger('click');
    await wrapper.find(`[data-testid="rotate-value-crd_stepfun"]`).setValue('sf-live-newvalue');
    await wrapper.find('[data-testid="rotate-submit"]').trigger('submit');
    await flushPromises();

    const rotateCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).endsWith('/api/v1/credentials/crd_stepfun/rotate'),
    );
    expect(rotateCall).toBeDefined();
    expect(JSON.parse(String(((rotateCall?.[1] ?? {}) as RequestInit).body))).toEqual({
      value: 'sf-live-newvalue',
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="credential-crd_stepfun"]').text()).toContain('key v7');
  });

  it('revokes a credential only after confirmation', async () => {
    const fetchMock = stubCredentials({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));
    await wrapper.find('[data-testid="revoke-crd_stepfun"]').trigger('click');
    await flushPromises();

    expect(fetchMock.mock.calls.some((call) => (call[1] as RequestInit)?.method === 'DELETE')).toBe(
      false,
    );
    expect(wrapper.find('[data-testid="confirm-revoke-crd_stepfun"]').exists()).toBe(true);

    await wrapper.find('[data-testid="confirm-revoke-crd_stepfun"]').trigger('click');
    await flushPromises();

    const deleteCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'DELETE',
    );
    expect(String(deleteCall?.[0])).toBe('/api/v1/credentials/crd_stepfun');
    expect(wrapper.find('[data-testid="credential-crd_stepfun"]').exists()).toBe(false);
  });

  it('cancels a revocation without calling the API', async () => {
    const fetchMock = stubCredentials({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));
    await wrapper.find('[data-testid="revoke-crd_github"]').trigger('click');
    await wrapper.find('[data-testid="cancel-revoke-crd_github"]').trigger('click');
    await flushPromises();

    expect(fetchMock.mock.calls.some((call) => (call[1] as RequestInit)?.method === 'DELETE')).toBe(
      false,
    );
    expect(wrapper.find('[data-testid="credential-crd_github"]').exists()).toBe(true);
  });

  it('shows an empty state when the organization has no credentials', async () => {
    vi.stubGlobal('fetch', stubCredentials({ list: page([]) }));
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));

    expect(wrapper.find('[data-testid="empty-credentials"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="credentials-list"]').exists()).toBe(false);
  });

  it('reports an API failure in a banner', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation((input: string) =>
        String(input).includes('/api/v1/credentials')
          ? Promise.resolve(
              jsonResponse(
                {
                  error: {
                    code: 'permission_denied',
                    message: 'you do not have access to this organization',
                    request_id: 'req_cred',
                  },
                },
                403,
              ),
            )
          : Promise.reject(new Error('unexpected')),
      ),
    );
    const wrapper = await mountCredentials(() => signIn('owner', ADMIN_PERMISSIONS));

    const banner = wrapper.find('[data-testid="credentials-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('you do not have access to this organization');
  });
});
