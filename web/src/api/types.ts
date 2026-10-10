import type { components, paths } from './generated/schema';

/** Wire types come from the OpenAPI contract; the client below never invents shapes. */
export type LiveStatus = components['schemas']['LiveStatus'];
export type ReadinessReport = components['schemas']['ReadinessReport'];
export type ReadinessCheck = components['schemas']['HealthCheck'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type ApiErrorBody = components['schemas']['Error'];

export type ReadinessStatus = ReadinessReport['status'];
export type CheckStatus = ReadinessCheck['status'];

/** Response type helpers keyed by operation id and status code. */
type JsonOf<TPath extends keyof paths, TStatus extends number> = paths[TPath] extends {
  responses: infer R;
}
  ? R extends Record<string, unknown>
    ? TStatus extends keyof R
      ? R[TStatus] extends { content: { 'application/json': infer T } }
        ? T
        : never
      : never
    : never
  : never;

export type LiveStatusResponse = JsonOf<'/health/live', 200>;
export type ReadinessOkResponse = JsonOf<'/health/ready', 200>;
export type ReadinessUnavailableResponse = JsonOf<'/health/ready', 503>;
export type SystemInfoResponse = JsonOf<'/api/v1/system/info', 200>;
