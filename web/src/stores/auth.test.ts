import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { getCsrfToken } from '@/api/client';
import type { CurrentUser, Permission, Role, SessionResponse } from '@/api/types';
import { useAuthStore } from './auth';

const OWNER_PERMISSIONS: Permission[] = [
  'project.read',
  'project.write',
  'run.execute',
  'member.admin',
  'credential.admin',
  'merge.execute',
];

const VIEWER_PERMISSIONS: Permission[] = ['project.read'];

const session: SessionResponse = {
  user: {
    id: 'usr_1a2b3c4d5e6f7g8h9i0j',
    email: 'developer@example.com',
    display_name: 'Developer',
  },
  csrf_token: 'csrf_9f8e7d6c5b4a',
  expires_at: '2026-10-11T10:00:00Z',
};

function profile(role: Role, permissions: Permission[]): CurrentUser {
  return {
    ...session.user,
    organizations: [
      {
        id: 'org_acme',
        name: 'Acme Engineering',
        slug: 'acme-engineering',
        role,
        permissions,
      },
    ],
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_auth_test' } }, status);
}

/** stubAuth serves login, /me, refresh and logout from one fetch stub. */
function stubAuth(options: {
  loginStatus?: number;
  loginCode?: string;
  loginMessage?: string;
  meStatus?: number;
  meCode?: string;
  me?: CurrentUser;
  logoutStatus?: number;
  refresh?: SessionResponse;
}): Mock {
  const fetchMock = vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('/api/v1/auth/login')) {
      return options.loginStatus !== undefined && options.loginStatus !== 200
        ? Promise.resolve(
            errorResponse(
              options.loginCode ?? 'invalid_credentials',
              options.loginMessage ?? 'email or password is incorrect',
              options.loginStatus,
            ),
          )
        : Promise.resolve(jsonResponse(session));
    }
    if (url.includes('/api/v1/auth/refresh')) {
      return Promise.resolve(jsonResponse(options.refresh ?? session));
    }
    if (url.includes('/api/v1/auth/logout')) {
      if (options.logoutStatus !== undefined && options.logoutStatus !== 204) {
        return Promise.resolve(
          errorResponse('unauthenticated', 'authentication is required', options.logoutStatus),
        );
      }
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    if (url.includes('/api/v1/me')) {
      return options.meStatus !== undefined && options.meStatus !== 200
        ? Promise.resolve(
            errorResponse(
              options.meCode ?? 'unauthenticated',
              'authentication is required',
              options.meStatus,
            ),
          )
        : Promise.resolve(jsonResponse(options.me ?? profile('owner', OWNER_PERMISSIONS)));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

/** headersOf reads the recorded request headers of a call. */
function headersOf(call: unknown[]): Record<string, string> {
  const init = (call?.[1] ?? {}) as RequestInit;
  return (init.headers ?? {}) as Record<string, string>;
}

function bodyOf(call: unknown[]): Record<string, unknown> {
  const init = (call?.[1] ?? {}) as RequestInit;
  return JSON.parse(String(init.body)) as Record<string, unknown>;
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
  window.sessionStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  window.sessionStorage.clear();
});

describe('auth store', () => {
  it('signs in, keeps the CSRF token in memory and computes permissions', async () => {
    const fetchMock = stubAuth({ me: profile('owner', OWNER_PERMISSIONS) });
    const auth = useAuthStore();

    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    expect(auth.status).toBe('authenticated');
    expect(auth.isAuthenticated).toBe(true);
    expect(auth.user?.email).toBe('developer@example.com');
    expect(auth.csrfToken).toBe('csrf_9f8e7d6c5b4a');
    expect(getCsrfToken()).toBe('csrf_9f8e7d6c5b4a');
    expect(auth.error).toBeNull();
    expect(auth.activeOrganization?.name).toBe('Acme Engineering');
    expect(auth.activeOrganizationId).toBe('org_acme');
    expect(auth.permissionsFor('org_acme')).toEqual(OWNER_PERMISSIONS);
    expect(auth.permissionsFor('org_unknown')).toEqual([]);
    expect(auth.can('member.admin')).toBe(true);
    expect(auth.can('credential.admin')).toBe(true);

    const loginCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).includes('/api/v1/auth/login'),
    );
    expect(loginCall).toBeDefined();
    expect((loginCall?.[1] as RequestInit).credentials).toBe('include');
    expect(bodyOf(loginCall ?? [])).toEqual({
      email: 'developer@example.com',
      password: 'correct-horse-battery-staple',
    });
  });

  it('keeps only the active organization id in web storage', async () => {
    stubAuth({ me: profile('owner', OWNER_PERMISSIONS) });
    const auth = useAuthStore();

    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    expect(window.localStorage.getItem('reposurgeon.activeOrganization')).toBe('org_acme');
    expect(window.sessionStorage.length).toBe(0);
    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (let index = 0; index < storage.length; index += 1) {
        const key = storage.key(index);
        expect(key).toBe('reposurgeon.activeOrganization');
      }
      const serialized = JSON.stringify(Object.assign({}, storage));
      expect(serialized).not.toContain(session.csrf_token);
      expect(serialized).not.toContain('correct-horse-battery-staple');
    }
  });

  it('records the server message and stays anonymous when login fails', async () => {
    stubAuth({ loginStatus: 401, loginCode: 'invalid_credentials' });
    const auth = useAuthStore();

    await expect(auth.login('developer@example.com', 'wrong-password-value')).rejects.toMatchObject(
      {
        name: 'ApiError',
        code: 'invalid_credentials',
        status: 401,
      },
    );

    expect(auth.status).toBe('anonymous');
    expect(auth.user).toBeNull();
    expect(auth.error).toBe('email or password is incorrect');
    expect(getCsrfToken()).toBeNull();
  });

  it('reports rate limiting from the API', async () => {
    stubAuth({
      loginStatus: 429,
      loginCode: 'rate_limited',
      loginMessage: 'too many failed login attempts; try again later',
    });
    const auth = useAuthStore();

    await expect(
      auth.login('developer@example.com', 'correct-horse-battery-staple'),
    ).rejects.toMatchObject({ code: 'rate_limited', status: 429, retryable: true });
    expect(auth.status).toBe('anonymous');
    expect(auth.error).toContain('too many failed login attempts');
  });

  it('signs out, clears the session and sends the CSRF token', async () => {
    const fetchMock = stubAuth({ me: profile('owner', OWNER_PERMISSIONS) });
    const auth = useAuthStore();
    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    await auth.logout();

    expect(auth.status).toBe('anonymous');
    expect(auth.user).toBeNull();
    expect(auth.csrfToken).toBeNull();
    expect(getCsrfToken()).toBeNull();

    const logoutCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).includes('/api/v1/auth/logout'),
    );
    expect(logoutCall).toBeDefined();
    expect((logoutCall?.[1] as RequestInit).method).toBe('POST');
    expect(headersOf(logoutCall ?? [])['X-CSRF-Token']).toBe('csrf_9f8e7d6c5b4a');
  });

  it('clears local state even when logout is rejected', async () => {
    stubAuth({ me: profile('owner', OWNER_PERMISSIONS), logoutStatus: 401 });
    const auth = useAuthStore();
    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    await auth.logout();

    expect(auth.status).toBe('anonymous');
    expect(auth.error).toBe('authentication is required');
  });

  it('becomes anonymous when refreshing an expired session', async () => {
    stubAuth({ meStatus: 401, meCode: 'unauthenticated' });
    const auth = useAuthStore();

    await auth.refresh();

    expect(auth.status).toBe('anonymous');
    expect(auth.user).toBeNull();
    expect(auth.isAuthenticated).toBe(false);
  });

  it('bootstrap is a no-op once the session is known', async () => {
    const fetchMock = stubAuth({ me: profile('owner', OWNER_PERMISSIONS) });
    const auth = useAuthStore();

    await auth.bootstrap();
    await auth.bootstrap();

    expect(auth.status).toBe('authenticated');
    const meCalls = fetchMock.mock.calls.filter((call) => String(call[0]).includes('/api/v1/me'));
    expect(meCalls).toHaveLength(1);
  });

  it('bootstrap marks an anonymous visitor anonymous', async () => {
    stubAuth({ meStatus: 401 });
    const auth = useAuthStore();

    await auth.bootstrap();

    expect(auth.status).toBe('anonymous');
    expect(auth.can('member.admin')).toBe(false);
  });

  it('rotates the session and swaps the CSRF token', async () => {
    stubAuth({
      me: profile('owner', OWNER_PERMISSIONS),
      refresh: { ...session, csrf_token: 'csrf_rotated_1' },
    });
    const auth = useAuthStore();
    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    const expiresAt = await auth.rotateSession();

    expect(expiresAt).toBe('2026-10-11T10:00:00Z');
    expect(auth.csrfToken).toBe('csrf_rotated_1');
    expect(getCsrfToken()).toBe('csrf_rotated_1');
    expect(auth.status).toBe('authenticated');
  });

  it('denies permissions for a viewer of the active organization', async () => {
    stubAuth({ me: profile('viewer', VIEWER_PERMISSIONS) });
    const auth = useAuthStore();

    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    expect(auth.status).toBe('authenticated');
    expect(auth.activeOrganization?.role).toBe('viewer');
    expect(auth.permissionsFor('org_acme')).toEqual(VIEWER_PERMISSIONS);
    expect(auth.can('project.read')).toBe(true);
    expect(auth.can('member.admin')).toBe(false);
    expect(auth.can('credential.admin')).toBe(false);
  });

  it('falls back to the first membership when the stored organization is unknown', async () => {
    window.localStorage.setItem('reposurgeon.activeOrganization', 'org_not_mine');
    stubAuth({ me: profile('owner', OWNER_PERMISSIONS) });
    const auth = useAuthStore();

    await auth.refresh();

    expect(auth.status).toBe('authenticated');
    expect(auth.activeOrganizationId).toBe('org_acme');
    expect(auth.can('member.admin')).toBe(true);
  });

  it('selecting another membership switches the permission scope', async () => {
    const multi: CurrentUser = {
      ...session.user,
      organizations: [
        {
          id: 'org_acme',
          name: 'Acme Engineering',
          slug: 'acme-engineering',
          role: 'owner',
          permissions: OWNER_PERMISSIONS,
        },
        {
          id: 'org_globex',
          name: 'Globex',
          slug: 'globex',
          role: 'viewer',
          permissions: VIEWER_PERMISSIONS,
        },
      ],
    };
    stubAuth({ me: multi });
    const auth = useAuthStore();
    await auth.login('developer@example.com', 'correct-horse-battery-staple');

    expect(auth.can('member.admin')).toBe(true);

    auth.selectOrganization('org_globex');

    expect(auth.activeOrganizationId).toBe('org_globex');
    expect(window.localStorage.getItem('reposurgeon.activeOrganization')).toBe('org_globex');
    expect(auth.can('member.admin')).toBe(false);
    expect(auth.can('project.read')).toBe(true);

    auth.selectOrganization('org_not_mine');
    expect(auth.activeOrganizationId).toBe('org_globex');
  });
});
