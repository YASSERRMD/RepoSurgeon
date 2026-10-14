import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';
import { createPinia, setActivePinia } from 'pinia';

import { authGuard, routes } from './router';
import { useAuthStore } from '@/stores/auth';

const profile = {
  id: 'usr_1a2b3c4d5e6f7g8h9i0j',
  email: 'developer@example.com',
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

/** stubSession serves GET /me (and the post-reload rotation) as an
 * authenticated or an anonymous caller. */
function stubSession(signedIn: boolean): Mock {
  const fetchMock = vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('/api/v1/me')) {
      return signedIn
        ? Promise.resolve(jsonResponse(profile))
        : Promise.resolve(
            jsonResponse(
              {
                error: {
                  code: 'unauthenticated',
                  message: 'authentication is required',
                  request_id: 'r_1',
                },
              },
              401,
            ),
          );
    }
    if (url.includes('/api/v1/auth/refresh')) {
      // Rotation after a reload mints the CSRF token.
      return signedIn
        ? Promise.resolve(
            jsonResponse({
              user: profile,
              csrf_token: 'csrf_after_refresh',
              expires_at: '2026-10-11T10:00:00Z',
            }),
          )
        : Promise.resolve(
            jsonResponse(
              { error: { code: 'unauthenticated', message: 'no session', request_id: 'r_2' } },
              401,
            ),
          );
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function guardRouter(signedIn: boolean): Router {
  const router = createRouter({ history: createMemoryHistory(), routes });
  router.beforeEach(authGuard);
  stubSession(signedIn);
  return router;
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('authGuard', () => {
  it('sends an anonymous visitor to /login and keeps the requested path', async () => {
    const router = guardRouter(false);

    await router.push('/members');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.next).toBe('/members');
    expect(useAuthStore().status).toBe('anonymous');
  });

  it('keeps an anonymous visitor on the login screen', async () => {
    const router = guardRouter(false);

    await router.push('/login');

    expect(router.currentRoute.value.path).toBe('/login');
    expect(useAuthStore().status).toBe('anonymous');
  });

  it('protects the code search, context and plan inspector routes', async () => {
    const router = guardRouter(false);

    await router.push('/workspaces/wsp_1a2b3c4d5e6f7g8h9i0j');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.next).toBe('/workspaces/wsp_1a2b3c4d5e6f7g8h9i0j');

    await router.push('/runs/run_1a2b3c4d5e6f7g8h9i0j/context');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.next).toBe('/runs/run_1a2b3c4d5e6f7g8h9i0j/context');

    await router.push('/runs/run_1a2b3c4d5e6f7g8h9i0j/plan');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.next).toBe('/runs/run_1a2b3c4d5e6f7g8h9i0j/plan');
  });

  it('lets a signed-in visitor open the inspector routes', async () => {
    const router = guardRouter(true);

    await router.push('/workspaces/wsp_1a2b3c4d5e6f7g8h9i0j');
    expect(router.currentRoute.value.name).toBe('workspace-search');

    await router.push('/runs/run_1a2b3c4d5e6f7g8h9i0j/context');
    expect(router.currentRoute.value.name).toBe('context-inspector');

    await router.push('/runs/run_1a2b3c4d5e6f7g8h9i0j/plan');
    expect(router.currentRoute.value.name).toBe('plan-inspector');
  });

  it('lets a signed-in visitor through to the requested route', async () => {
    const router = guardRouter(true);

    await router.push('/credentials');

    expect(router.currentRoute.value.name).toBe('credentials');
    expect(useAuthStore().status).toBe('authenticated');
  });

  it('sends a signed-in visitor away from the login screen', async () => {
    const router = guardRouter(true);

    await router.push('/login');

    // A signed-in visitor is redirected to the workbench overview, not to the
    // public status page.
    expect(router.currentRoute.value.name).toBe('overview');
  });

  it('probes the session exactly once across navigations', async () => {
    const fetchMock = stubSession(true);
    const router = createRouter({ history: createMemoryHistory(), routes });
    router.beforeEach(authGuard);

    await router.push('/');
    await router.push('/members');
    await router.push('/credentials');

    expect(router.currentRoute.value.name).toBe('credentials');
    const meCalls = fetchMock.mock.calls.filter((call) => String(call[0]).includes('/api/v1/me'));
    expect(meCalls).toHaveLength(1);
  });

  it('expires the redirect when the session disappears between navigations', async () => {
    const fetchMock = stubSession(true);
    const router = createRouter({ history: createMemoryHistory(), routes });
    router.beforeEach(authGuard);
    await router.push('/members');
    expect(router.currentRoute.value.name).toBe('members');

    fetchMock.mockImplementation(() =>
      Promise.resolve(
        jsonResponse(
          {
            error: {
              code: 'unauthenticated',
              message: 'authentication is required',
              request_id: 'r_2',
            },
          },
          401,
        ),
      ),
    );
    const auth = useAuthStore();
    await auth.refresh();
    await router.push('/projects');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.next).toBe('/projects');
  });
});
