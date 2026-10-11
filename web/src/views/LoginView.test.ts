import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import LoginView from './LoginView.vue';
import { useAuthStore } from '@/stores/auth';
import { getCsrfToken } from '@/api/client';

const session = {
  user: {
    id: 'usr_1a2b3c4d5e6f7g8h9i0j',
    email: 'developer@example.com',
    display_name: 'Developer',
  },
  csrf_token: 'csrf_9f8e7d6c5b4a',
  expires_at: '2026-10-11T10:00:00Z',
};

const profile = {
  ...session.user,
  organizations: [
    {
      id: 'org_acme',
      name: 'Acme Engineering',
      slug: 'acme-engineering',
      role: 'owner',
      permissions: ['project.read', 'project.write', 'member.admin', 'credential.admin'],
    },
  ],
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** stubAuth answers login, /me and logout; loginStatus controls the failure code. */
function stubAuth(options: { loginStatus?: number; loginCode?: string; message?: string }): Mock {
  const fetchMock = vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('/api/v1/auth/login')) {
      return options.loginStatus === undefined || options.loginStatus === 200
        ? Promise.resolve(jsonResponse(session))
        : Promise.resolve(
            jsonResponse(
              {
                error: {
                  code: options.loginCode ?? 'invalid_credentials',
                  message: options.message ?? 'email or password is incorrect',
                  request_id: 'req_login_test',
                },
              },
              options.loginStatus,
            ),
          );
    }
    if (url.includes('/api/v1/me')) {
      return Promise.resolve(jsonResponse(profile));
    }
    return Promise.resolve(new Response(null, { status: 204 }));
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
      { path: '/projects', component: { template: '<div />' } },
    ],
  });
}

async function mountLogin(
  options: { next?: string; loginStatus?: number; loginCode?: string; message?: string } = {},
): Promise<{ wrapper: ReturnType<typeof mount>; router: Router; fetchMock: Mock }> {
  const fetchMock = stubAuth(options);
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  const target =
    options.next === undefined ? '/login' : `/login?next=${encodeURIComponent(options.next)}`;
  await router.push(target);
  await router.isReady();
  const wrapper = mount(LoginView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return { wrapper, router, fetchMock };
}

async function fillAndSubmit(
  wrapper: ReturnType<typeof mount>,
  email: string,
  password: string,
): Promise<void> {
  await wrapper.find('[data-testid="login-email"]').setValue(email);
  await wrapper.find('[data-testid="login-password"]').setValue(password);
  await wrapper.find('[data-testid="login-submit"]').trigger('submit');
  await flushPromises();
}

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  window.sessionStorage.clear();
});

describe('LoginView', () => {
  it('blocks submission until both fields are valid', async () => {
    const { wrapper, fetchMock } = await mountLogin();

    await fillAndSubmit(wrapper, '', 'short');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="login-email-error"]').text()).toContain('Email is required');
    expect(wrapper.find('[data-testid="login-password-error"]').text()).toContain(
      'at least 12 characters',
    );
  });

  it('rejects an address that is not an email', async () => {
    const { wrapper, fetchMock } = await mountLogin();

    await fillAndSubmit(wrapper, 'developer-at-example.com', 'correct-horse-battery-staple');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="login-email-error"]').text()).toContain(
      'Enter a valid email address',
    );
    expect(wrapper.find('[data-testid="login-error"]').exists()).toBe(false);
  });

  it('shows the invalid_credentials banner from the API', async () => {
    const { wrapper } = await mountLogin({ loginStatus: 401, loginCode: 'invalid_credentials' });

    await fillAndSubmit(wrapper, 'developer@example.com', 'wrong-password-value');

    const banner = wrapper.find('[data-testid="login-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.attributes('data-code')).toBe('invalid_credentials');
    expect(banner.text()).toContain('Email or password is incorrect');
    const auth = useAuthStore();
    expect(auth.status).toBe('anonymous');
  });

  it('shows the rate_limited banner when the API throttles attempts', async () => {
    const { wrapper } = await mountLogin({
      loginStatus: 429,
      loginCode: 'rate_limited',
      message: 'too many failed login attempts; try again later',
    });

    await fillAndSubmit(wrapper, 'developer@example.com', 'correct-horse-battery-staple');

    const banner = wrapper.find('[data-testid="login-error"]');
    expect(banner.attributes('data-code')).toBe('rate_limited');
    expect(banner.text()).toContain('Too many sign-in attempts');
  });

  it('shows the unauthenticated banner when the session is expired', async () => {
    const { wrapper } = await mountLogin({
      loginStatus: 401,
      loginCode: 'unauthenticated',
      message: 'authentication is required',
    });

    await fillAndSubmit(wrapper, 'developer@example.com', 'correct-horse-battery-staple');

    const banner = wrapper.find('[data-testid="login-error"]');
    expect(banner.attributes('data-code')).toBe('unauthenticated');
    expect(banner.text()).toContain('session is no longer valid');
  });

  it('signs in, keeps the csrf token in memory and returns to the next path', async () => {
    const { wrapper, router } = await mountLogin({ next: '/projects' });

    await fillAndSubmit(wrapper, 'developer@example.com', 'correct-horse-battery-staple');

    const auth = useAuthStore();
    expect(auth.status).toBe('authenticated');
    expect(getCsrfToken()).toBe('csrf_9f8e7d6c5b4a');
    expect(router.currentRoute.value.path).toBe('/projects');
    expect(wrapper.find('[data-testid="login-error"]').exists()).toBe(false);
  });

  it('signs in and lands on the dashboard when no next path was given', async () => {
    const { wrapper, router } = await mountLogin();

    await fillAndSubmit(wrapper, 'developer@example.com', 'correct-horse-battery-staple');

    expect(router.currentRoute.value.path).toBe('/');
  });

  it('disables the submit button while the request is in flight', async () => {
    let release: (response: Response) => void = () => undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(
        () =>
          new Promise<Response>((resolve) => {
            release = resolve;
          }),
      ),
    );
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = testRouter();
    await router.push('/login');
    await router.isReady();
    const wrapper = mount(LoginView, { global: { plugins: [pinia, router] } });

    await wrapper.find('[data-testid="login-email"]').setValue('developer@example.com');
    await wrapper.find('[data-testid="login-password"]').setValue('correct-horse-battery-staple');
    void wrapper.find('[data-testid="login-submit"]').trigger('submit');
    await flushPromises();

    const button = wrapper.find('[data-testid="login-submit"]');
    expect(button.attributes('disabled')).toBeDefined();
    expect(button.text()).toBe('Signing in…');

    release(jsonResponse(session));
    await flushPromises();
    await flushPromises();
  });

  it('ignores a next parameter that points at another origin', async () => {
    const { wrapper, router } = await mountLogin({ next: 'https://evil.example.com/steal' });

    await fillAndSubmit(wrapper, 'developer@example.com', 'correct-horse-battery-staple');

    expect(router.currentRoute.value.path).toBe('/');
  });
});
