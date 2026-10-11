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

/** Authentication (M02): browser sessions, never bearer tokens in storage. */
export type LoginRequest = components['schemas']['LoginRequest'];
export type SessionResponse = components['schemas']['SessionResponse'];
export type UserSummary = components['schemas']['UserSummary'];
export type CurrentUser = components['schemas']['CurrentUser'];
export type MembershipSummary = components['schemas']['MembershipSummary'];
export type Role = components['schemas']['Role'];
export type Permission = components['schemas']['Permission'];

export type Member = components['schemas']['Member'];
export type MemberPage = components['schemas']['MemberPage'];
export type MemberInvite = components['schemas']['MemberInvite'];
export type MemberUpdate = components['schemas']['MemberUpdate'];

/** Credentials expose metadata only; `value` is write-only in the contract. */
export type Credential = components['schemas']['Credential'];
export type CredentialPage = components['schemas']['CredentialPage'];
export type CredentialCreate = components['schemas']['CredentialCreate'];
export type CredentialRotate = components['schemas']['CredentialRotate'];
export type CredentialKind = components['schemas']['CredentialKind'];
export type CredentialStatus = components['schemas']['CredentialStatus'];

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
export type LoginResponse = JsonOf<'/api/v1/auth/login', 200>;
export type SessionRefreshResponse = JsonOf<'/api/v1/auth/refresh', 200>;
export type CurrentUserResponse = JsonOf<'/api/v1/me', 200>;
export type MemberListResponse = JsonOf<'/api/v1/members', 200>;
export type MemberInviteResponse = JsonOf<'/api/v1/members', 201>;
export type MemberUpdateResponse = JsonOf<'/api/v1/members/{membership_id}', 200>;
export type CredentialListResponse = JsonOf<'/api/v1/credentials', 200>;
export type CredentialCreateResponse = JsonOf<'/api/v1/credentials', 201>;
export type CredentialRotateResponse = JsonOf<'/api/v1/credentials/{credential_id}/rotate', 200>;
