import type { components, paths } from './generated/schema';

/** Wire types come from the OpenAPI contract; the client below never invents shapes. */
export type LiveStatus = components['schemas']['LiveStatus'];
export type ReadinessReport = components['schemas']['ReadinessReport'];
export type ReadinessCheck = components['schemas']['HealthCheck'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type ApiErrorBody = components['schemas']['Error'];

export type Organization = components['schemas']['Organization'];
export type OrganizationPage = components['schemas']['OrganizationPage'];
export type OrganizationCreate = components['schemas']['OrganizationCreate'];
export type OrganizationRole = components['schemas']['OrganizationRole'];
export type Project = components['schemas']['Project'];
export type ProjectPage = components['schemas']['ProjectPage'];
export type ProjectCreate = components['schemas']['ProjectCreate'];
export type ProjectUpdate = components['schemas']['ProjectUpdate'];
export type AuditEvent = components['schemas']['AuditEvent'];
export type AuditEventPage = components['schemas']['AuditEventPage'];
export type ConflictDetail = components['schemas']['ConflictDetail'];

export type ReadinessStatus = ReadinessReport['status'];
export type CheckStatus = ReadinessCheck['status'];
export type ProjectSettings = NonNullable<ProjectUpdate['settings']>;

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
export type OrganizationListResponse = JsonOf<'/api/v1/organizations', 200>;
export type OrganizationCreateResponse = JsonOf<'/api/v1/organizations', 201>;
export type ProjectListResponse = JsonOf<'/api/v1/projects', 200>;
export type ProjectCreateResponse = JsonOf<'/api/v1/projects', 201>;
export type ProjectDetailResponse = JsonOf<'/api/v1/projects/{project_id}', 200>;
export type ProjectUpdateResponse = JsonOf<'/api/v1/projects/{project_id}', 200>;
export type AuditEventListResponse = JsonOf<'/api/v1/audit', 200>;
