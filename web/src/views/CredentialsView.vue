<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { CredentialKind, CredentialStatus } from '@/api/types';
import { useAuthStore } from '@/stores/auth';
import { useCredentialsStore } from '@/stores/credentials';

const auth = useAuthStore();
const credentials = useCredentialsStore();

const KINDS: CredentialKind[] = ['github_app', 'stepfun', 'other'];
const KIND_LABELS: Record<CredentialKind, string> = {
  github_app: 'GitHub App',
  stepfun: 'StepFun',
  other: 'Other',
};
const STATUS_LABELS: Record<CredentialStatus, string> = {
  active: 'active',
  rotating: 'rotating',
  revoked: 'revoked',
  failed: 'failed',
};

const kind = ref<CredentialKind>('stepfun');
const label = ref('');
const value = ref('');
const labelError = ref('');
const valueError = ref('');
const rotateError = ref('');
const rotatingId = ref<string | null>(null);
const rotateValue = ref('');
const confirmingRevokeId = ref<string | null>(null);

const activeOrganizationId = computed(() => auth.activeOrganizationId);
const canManageCredentials = computed(() => auth.can('credential.admin'));

onMounted(() => {
  if (activeOrganizationId.value !== null) void credentials.load(activeOrganizationId.value);
});

watch(activeOrganizationId, (organizationId) => {
  if (organizationId !== null) void credentials.load(organizationId);
});

function shortFingerprint(fingerprint: string): string {
  return fingerprint.length <= 12 ? fingerprint : `${fingerprint.slice(0, 12)}…`;
}

function verificationLabel(lastVerifiedAt: string | null | undefined): string {
  if (lastVerifiedAt === null || lastVerifiedAt === undefined) return 'not verified yet';
  return `verified ${new Date(lastVerifiedAt).toLocaleString()}`;
}

function startRotation(credentialId: string): void {
  rotatingId.value = credentialId;
  rotateValue.value = '';
  rotateError.value = '';
  confirmingRevokeId.value = null;
}

function cancelRotation(): void {
  rotatingId.value = null;
  rotateValue.value = '';
}

function confirmRevoke(credentialId: string): void {
  confirmingRevokeId.value = credentialId;
  rotatingId.value = null;
}

function cancelRevoke(): void {
  confirmingRevokeId.value = null;
}

/** validateValue mirrors the contract bounds; the server re-checks both. */
function validateValue(raw: string): string | null {
  if (raw.length < 8) return 'Enter at least 8 characters.';
  if (raw.length > 4096) return 'Values are limited to 4096 characters.';
  return null;
}

async function submitCreate(): Promise<void> {
  const organizationId = activeOrganizationId.value;
  if (organizationId === null) return;
  const trimmedLabel = label.value.trim();
  labelError.value = '';
  valueError.value = '';
  if (trimmedLabel === '') {
    labelError.value = 'Label is required.';
    return;
  }
  const invalid = validateValue(value.value);
  if (invalid !== null) {
    valueError.value = invalid;
    return;
  }
  try {
    await credentials.create({
      organization_id: organizationId,
      kind: kind.value,
      label: trimmedLabel,
      value: value.value,
    });
    label.value = '';
    value.value = '';
  } catch {
    // credentials.error shows the API explanation; the value is discarded.
  }
}

async function submitRotation(credentialId: string): Promise<void> {
  rotateError.value = '';
  const invalid = validateValue(rotateValue.value);
  if (invalid !== null) {
    rotateError.value = invalid;
    return;
  }
  try {
    await credentials.rotate(credentialId, rotateValue.value);
    cancelRotation();
  } catch {
    // The stored credential keeps the previous value when rotation fails.
  }
}

async function revoke(credentialId: string): Promise<void> {
  try {
    await credentials.revoke(credentialId);
    confirmingRevokeId.value = null;
  } catch {
    // credentials.error shows the API explanation.
  }
}
</script>

<template>
  <div class="credentials">
    <div class="page-head">
      <div>
        <h2 class="page-title">Credentials</h2>
        <p class="muted page-subtitle">
          Encrypted provider credentials for the active organization. Values are never returned,
          logged or shown again after they are stored.
        </p>
      </div>
    </div>

    <p
      v-if="!canManageCredentials"
      class="banner banner-info"
      data-testid="credentials-no-permission"
    >
      <strong>Read-only.</strong> Managing credentials requires the
      <code>credential.admin</code> permission in the active organization. Ask an owner or an admin;
      the API enforces the same rule before storing anything.
    </p>

    <section v-else class="card" data-testid="create-credential-form">
      <h3 class="card-title">Store a credential</h3>
      <form novalidate @submit.prevent="submitCreate">
        <div class="create-grid">
          <FormField label="Kind" field-id="credential-kind">
            <select id="credential-kind" v-model="kind" data-testid="credential-kind">
              <option v-for="option in KINDS" :key="option" :value="option">
                {{ KIND_LABELS[option] }}
              </option>
            </select>
          </FormField>
          <FormField label="Label" field-id="credential-label" :error="labelError">
            <input
              id="credential-label"
              v-model="label"
              type="text"
              maxlength="120"
              placeholder="production keys"
              data-testid="credential-label"
            />
          </FormField>
        </div>
        <FormField
          label="Value"
          field-id="credential-value"
          :error="valueError"
          hint="Encrypted with an envelope key before it reaches the database. It is never displayed afterwards — rotate to replace it."
        >
          <input
            id="credential-value"
            v-model="value"
            type="password"
            autocomplete="off"
            maxlength="4096"
            placeholder="paste the provider secret"
            data-testid="credential-value"
          />
        </FormField>
        <button
          class="primary"
          type="submit"
          data-testid="create-credential-submit"
          :disabled="credentials.creating"
        >
          {{ credentials.creating ? 'Storing…' : 'Store credential' }}
        </button>
      </form>
    </section>

    <p v-if="credentials.error" class="banner banner-error" data-testid="credentials-error">
      <strong>Credential operation failed.</strong> {{ credentials.error }}
      <button
        class="retry"
        type="button"
        data-testid="credentials-retry"
        @click="activeOrganizationId !== null && credentials.load(activeOrganizationId)"
      >
        Retry
      </button>
    </p>

    <div v-if="credentials.loading" data-testid="credentials-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="credentials.items.length === 0"
      title="No credentials yet"
      description="Store a provider credential to let engineering runs authenticate against GitHub or the model provider."
      test-id="empty-credentials"
    />

    <ul v-else class="credential-list" data-testid="credentials-list">
      <li
        v-for="credential in credentials.items"
        :key="credential.id"
        class="credential-row card"
        :data-testid="`credential-${credential.id}`"
      >
        <div class="credential-identity">
          <span class="credential-label">{{ credential.label }}</span>
          <span class="credential-kind">{{ KIND_LABELS[credential.kind] }}</span>
          <span class="credential-meta">
            key v{{ credential.key_version }} · fingerprint
            <code>{{ shortFingerprint(credential.fingerprint) }}</code>
          </span>
          <span class="credential-verified" data-testid="credential-verified">
            {{ verificationLabel(credential.last_verified_at) }}
          </span>
        </div>

        <div class="credential-side">
          <span
            class="badge"
            :class="`status-${credential.status}`"
            data-testid="credential-status"
          >
            {{ STATUS_LABELS[credential.status] }}
          </span>

          <template v-if="canManageCredentials">
            <button
              class="ghost"
              type="button"
              :data-testid="`rotate-${credential.id}`"
              :disabled="credentials.rotatingCredentialId === credential.id"
              @click="startRotation(credential.id)"
            >
              Rotate
            </button>

            <span v-if="confirmingRevokeId === credential.id" class="confirm">
              <span class="confirm-label">Revoke this credential?</span>
              <button
                class="danger"
                type="button"
                :data-testid="`confirm-revoke-${credential.id}`"
                :disabled="credentials.revokingCredentialId === credential.id"
                @click="revoke(credential.id)"
              >
                Yes, revoke
              </button>
              <button
                class="ghost"
                type="button"
                :data-testid="`cancel-revoke-${credential.id}`"
                @click="cancelRevoke"
              >
                Cancel
              </button>
            </span>
            <button
              v-else
              class="danger"
              type="button"
              :data-testid="`revoke-${credential.id}`"
              :disabled="credentials.revokingCredentialId === credential.id"
              @click="confirmRevoke(credential.id)"
            >
              Revoke
            </button>
          </template>
        </div>

        <form
          v-if="canManageCredentials && rotatingId === credential.id"
          class="rotate-form"
          novalidate
          @submit.prevent="submitRotation(credential.id)"
        >
          <FormField
            label="New value"
            field-id="rotate-value"
            :error="rotateError"
            hint="The new value is encrypted; the previous ciphertext is destroyed in the same transaction."
          >
            <input
              id="rotate-value"
              v-model="rotateValue"
              type="password"
              autocomplete="off"
              maxlength="4096"
              placeholder="paste the replacement secret"
              :data-testid="`rotate-value-${credential.id}`"
            />
          </FormField>
          <div class="rotate-actions">
            <button class="primary" type="submit" data-testid="rotate-submit">Rotate</button>
            <button class="ghost" type="button" data-testid="rotate-cancel" @click="cancelRotation">
              Cancel
            </button>
          </div>
        </form>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.page-title {
  font-size: 1.5rem;
}

.page-subtitle {
  margin: 0.35rem 0 0;
  font-size: 0.9rem;
  max-width: 76ch;
}

.credentials {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
}

.card-title {
  font-size: 1rem;
  margin-bottom: 0.9rem;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
}

.banner-info {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
}

.retry {
  border: 1px solid currentcolor;
  background: transparent;
  color: inherit;
  padding: 0.25rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

input,
select {
  font: inherit;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
}

input:focus,
select:focus {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

.primary {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  font-weight: 600;
}

.primary:hover:not(:disabled) {
  background: var(--color-navy-soft);
}

.ghost {
  border: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.35rem 0.8rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.ghost:hover:not(:disabled) {
  border-color: var(--color-navy);
}

.danger {
  border: 1px solid var(--color-danger);
  background: var(--color-surface);
  color: var(--color-danger);
  padding: 0.3rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.danger:hover:not(:disabled) {
  background: var(--color-danger-soft);
}

.create-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0.9rem;
  margin-bottom: 0.9rem;
}

form {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}

.credential-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.credential-row {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 0.75rem 1rem;
  align-items: start;
  padding: 0.9rem 1.1rem;
}

.credential-identity {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  min-width: 0;
}

.credential-label {
  font-weight: 650;
  color: var(--color-navy);
}

.credential-kind {
  font-size: 0.8rem;
  color: var(--color-text-muted);
}

.credential-meta {
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.credential-verified {
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.credential-side {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.confirm {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.2rem 0.4rem;
  border: 1px dashed var(--color-danger);
  border-radius: var(--radius-sm);
}

.confirm-label {
  font-size: 0.76rem;
  color: var(--color-danger);
  font-weight: 600;
}

.rotate-form {
  grid-column: 1 / -1;
  max-width: 32rem;
  border-top: 1px dashed var(--color-gray-line);
  padding-top: 0.75rem;
}

.rotate-actions {
  display: flex;
  gap: 0.5rem;
}

.status-active {
  background: var(--color-ok-soft);
  color: var(--color-ok);
  border: 1px solid rgb(28 124 70 / 30%);
}

.status-rotating {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid var(--color-gold);
}

.status-revoked {
  background: #f3f4f6;
  color: var(--color-text-muted);
}

.status-failed {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.9em;
}

.skeleton {
  height: 5rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
