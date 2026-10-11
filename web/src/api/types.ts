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

/** Tools (M07): catalog metadata only, never a command, script or secret. */
export type ToolDescriptor = components['schemas']['ToolDescriptor'];
export type ToolRisk = ToolDescriptor['risk'];

/** Approvals (M07): a decision is recorded server-side and never inferred. */
export type Approval = components['schemas']['Approval'];
export type ApprovalState = Approval['state'];
export type ApprovalCreate = components['schemas']['ApprovalCreate'];
export type ApprovalDecision = components['schemas']['ApprovalDecision'];
export type ApprovalDecisionChoice = ApprovalDecision['decision'];

export type ReadinessStatus = ReadinessReport['status'];
export type CheckStatus = ReadinessCheck['status'];
export type ProjectSettings = NonNullable<ProjectUpdate['settings']>;

/**
 * ResponsesOf collects the response blocks of every method declared on a path.
 * Keys that are not operations (parameters, and the methods the generator types
 * as `never`) contribute `never`, which drops out of the resulting union.
 */
type ResponsesOf<TPath extends keyof paths> = {
  [K in keyof paths[TPath]]: paths[TPath][K] extends { responses: infer R } ? R : never;
}[keyof paths[TPath]];

/**
 * Response type helpers keyed by path and status code. The conditional is
 * distributive over the union of methods, so a status that only one method
 * answers (a 201 on POST) resolves to that method's payload.
 */
type JsonOf<TPath extends keyof paths, TStatus extends number> =
  ResponsesOf<TPath> extends infer R
    ? R extends { [K in `${TStatus}`]: infer Response }
      ? Response extends { content: { 'application/json': infer T } }
        ? T
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
export type ToolListResponse = JsonOf<'/api/v1/tools', 200>;
export type ApprovalListResponse = JsonOf<'/api/v1/approvals', 200>;
export type ApprovalCreateResponse = JsonOf<'/api/v1/approvals', 201>;
export type ApprovalDecisionResponse = JsonOf<'/api/v1/approvals/{approval_id}/decide', 200>;
