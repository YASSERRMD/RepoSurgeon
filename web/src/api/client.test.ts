import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, HealthClient } from './client';
import type { LiveStatus, ReadinessReport, SystemInfo } from './types';

const live: LiveStatus = { status: 'ok', uptime_seconds: 12.5 };
const system: SystemInfo = {
  name: 'reposurgeon',
  version: '0.1.0',
  git_commit: 'abc123',
  build_time: '2026-10-10T10:00:00Z',
  environment: 'development',
  go_version: 'go1.25.5',
  started_at: '2026-10-10T09:59:00Z',
  uptime_seconds: 60,
};
const ready: ReadinessReport = {
  status: 'ready',
  checks: [{ name: 'database', status: 'up', duration_ms: 2 }],
  generated_at: '2026-10-10T10:00:00Z',
};

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('HealthClient', () => {
  it('fetches liveness and returns parsed payload', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(live));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getLive()).resolves.toEqual(live);
    expect(fetchMock).toHaveBeenCalledWith(
      '/health/live',
      expect.objectContaining({ method: 'GET' }),
    );
  });

  it('resolves readiness even on 503 because the report carries operator detail', async () => {
    const unavailable: ReadinessReport = {
      status: 'unavailable',
      checks: [{ name: 'database', status: 'down', detail: 'unreachable', duration_ms: 5 }],
      generated_at: '2026-10-10T10:00:00Z',
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(unavailable, { status: 503 }));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await expect(client.getReady()).resolves.toEqual(unavailable);
  });

  it('converts the canonical error envelope into ApiError', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          error: {
            code: 'not_found',
            message: 'resource not found',
            request_id: 'req_123',
          },
        },
        { status: 404 },
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('/base');
    await expect(client.getSystemInfo()).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      code: 'not_found',
      requestId: 'req_123',
      retryable: false,
    });
  });

  it('classifies 503 responses as retryable', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        { error: { code: 'internal_error', message: 'boom', request_id: 'req_9' } },
        {
          status: 503,
        },
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    const error = await client.getLive().catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).retryable).toBe(true);
  });

  it('maps network failures to a retryable network_error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('ECONNREFUSED')));

    const client = new HealthClient('');
    const error = await client.getReady().catch((err: unknown) => err);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network_error');
    expect((error as ApiError).retryable).toBe(true);
    expect((error as ApiError).message).toContain('ECONNREFUSED');
  });

  it('falls back to the X-Request-ID header when the body has none', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('{}', {
          status: 500,
          headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_header' },
        }),
      ),
    );

    const client = new HealthClient('');
    const error = await client.getLive().catch((err: unknown) => err);
    expect((error as ApiError).requestId).toBe('req_header');
  });

  it('honours a caller-provided abort signal', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn().mockImplementation((_input: string, init?: RequestInit) => {
      expect(init?.signal).toBeInstanceOf(AbortSignal);
      controller.abort();
      return Promise.resolve(jsonResponse(ready));
    });
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('');
    await client.getReady({ signal: controller.signal });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('uses the configured base URL prefix', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(system));
    vi.stubGlobal('fetch', fetchMock);

    const client = new HealthClient('https://api.example.com');
    await client.getSystemInfo();
    expect(fetchMock).toHaveBeenCalledWith(
      'https://api.example.com/api/v1/system/info',
      expect.objectContaining({ method: 'GET' }),
    );
  });
});
