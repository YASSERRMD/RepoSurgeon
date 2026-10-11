<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { Role } from '@/api/types';
import { useAuthStore } from '@/stores/auth';
import { useMembersStore } from '@/stores/members';

const auth = useAuthStore();
const members = useMembersStore();

const inviteEmail = ref('');
const inviteRole = ref<Role>('viewer');
const emailError = ref('');

/** Roles come straight from the contract; the server validates every grant. */
const ROLES: Role[] = ['owner', 'admin', 'maintainer', 'reviewer', 'member', 'viewer'];

const activeOrganization = computed(() => auth.activeOrganization);
const activeOrganizationId = computed(() => auth.activeOrganizationId);
const canManageMembers = computed(() => auth.can('member.admin'));

onMounted(() => {
  if (activeOrganizationId.value !== null) void members.load(activeOrganizationId.value);
});

watch(activeOrganizationId, (organizationId) => {
  if (organizationId !== null) void members.load(organizationId);
});

function dismissConflict(): void {
  members.conflict = null;
}

async function submitInvite(): Promise<void> {
  const organizationId = activeOrganizationId.value;
  if (organizationId === null) return;
  const email = inviteEmail.value.trim();
  emailError.value = '';
  if (email === '' || !email.includes('@')) {
    emailError.value = 'Enter a valid email address.';
    return;
  }
  try {
    await members.invite(organizationId, email, inviteRole.value);
    inviteEmail.value = '';
  } catch {
    // members.error / members.conflict carry the server explanation.
  }
}

async function changeRole(membershipId: string, role: Role): Promise<void> {
  try {
    await members.changeRole(membershipId, role);
  } catch {
    // A 409 keeps the stored role; the banner explains why.
  }
}

async function removeMember(membershipId: string): Promise<void> {
  try {
    await members.remove(membershipId);
  } catch {
    // Removal of the last owner or of yourself is rejected with a 409.
  }
}
</script>

<template>
  <div class="members">
    <div class="page-head">
      <div>
        <h2 class="page-title">Members</h2>
        <p class="muted page-subtitle">
          <template v-if="activeOrganization">
            Everyone with access to <strong>{{ activeOrganization.name }}</strong
            >. Roles decide permissions; the API re-checks every request.
          </template>
          <template v-else>Members are managed per organization.</template>
        </p>
      </div>
    </div>

    <EmptyState
      v-if="activeOrganizationId === null"
      title="No active organization"
      description="Pick an organization in Organizations to manage its members."
      test-id="members-need-organization"
    >
      <template #action>
        <RouterLink class="link-button" to="/organizations">Manage organizations</RouterLink>
      </template>
    </EmptyState>

    <template v-else>
      <section v-if="canManageMembers" class="card" data-testid="invite-form">
        <h3 class="card-title">Invite a member</h3>
        <form novalidate @submit.prevent="submitInvite">
          <div class="invite-grid">
            <FormField label="Email" field-id="invite-email" :error="emailError">
              <input
                id="invite-email"
                v-model="inviteEmail"
                type="email"
                maxlength="320"
                placeholder="teammate@example.com"
                data-testid="invite-email"
              />
            </FormField>
            <FormField
              label="Role"
              field-id="invite-role"
              hint="The role is validated server-side and can never grant more than it allows."
            >
              <select id="invite-role" v-model="inviteRole" data-testid="invite-role">
                <option v-for="role in ROLES" :key="role" :value="role">{{ role }}</option>
              </select>
            </FormField>
          </div>
          <button
            class="primary"
            type="submit"
            data-testid="invite-submit"
            :disabled="members.inviting"
          >
            {{ members.inviting ? 'Inviting…' : 'Send invite' }}
          </button>
        </form>
      </section>

      <p v-else class="banner banner-info" data-testid="members-no-permission">
        <strong>Read-only.</strong> Your role in this organization does not include the
        <code>member.admin</code> permission, so invites and role changes are disabled. Ask an owner
        or an admin to make changes; the API enforces the same rule.
      </p>

      <p v-if="members.conflict" class="banner banner-conflict" data-testid="members-conflict">
        <strong>Change rejected.</strong> {{ members.conflict.message }}
        <button
          class="retry"
          type="button"
          data-testid="members-conflict-dismiss"
          @click="dismissConflict"
        >
          Dismiss
        </button>
      </p>

      <p v-if="members.error" class="banner banner-error" data-testid="members-error">
        <strong>Could not load members.</strong> {{ members.error }}
        <button
          class="retry"
          type="button"
          data-testid="members-retry"
          @click="activeOrganizationId !== null && members.load(activeOrganizationId)"
        >
          Retry
        </button>
      </p>

      <div v-if="members.loading" data-testid="members-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="members.items.length === 0"
        title="No members yet"
        description="Invite a teammate to give them access to this organization."
        test-id="empty-members"
      />

      <ul v-else class="member-list" data-testid="members-list">
        <li
          v-for="member in members.items"
          :key="member.id"
          class="member-row card"
          :data-testid="`member-${member.id}`"
        >
          <div class="member-identity">
            <span class="member-email">{{ member.email }}</span>
            <span class="member-meta">
              joined {{ new Date(member.created_at).toLocaleDateString() }}
            </span>
          </div>
          <div class="member-actions">
            <span class="badge" :class="`role-${member.role}`" data-testid="member-role-badge">
              {{ member.role }}
            </span>
            <label class="visually-hidden" :for="`role-${member.id}`"
              >Role for {{ member.email }}</label
            >
            <select
              v-if="canManageMembers"
              :id="`role-${member.id}`"
              class="role-select"
              :data-testid="`role-${member.id}`"
              :value="member.role"
              :disabled="members.updatingMembershipId === member.id"
              @change="changeRole(member.id, ($event.target as HTMLSelectElement).value as Role)"
            >
              <option v-for="role in ROLES" :key="role" :value="role">{{ role }}</option>
            </select>
            <button
              v-if="canManageMembers"
              class="danger"
              type="button"
              :data-testid="`remove-${member.id}`"
              :disabled="members.removingMembershipId === member.id"
              @click="removeMember(member.id)"
            >
              {{ members.removingMembershipId === member.id ? 'Removing…' : 'Remove' }}
            </button>
          </div>
        </li>
      </ul>
    </template>
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
  max-width: 70ch;
}

.members {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.link-button {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.45rem 0.9rem;
  border-radius: var(--radius-sm);
  font-size: 0.82rem;
  font-weight: 600;
  text-decoration: none;
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

.invite-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0.9rem;
  margin-bottom: 1rem;
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

.banner-conflict {
  background: var(--color-gold-soft);
  border: 1px solid var(--color-gold);
  color: var(--color-navy);
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

.role-select {
  width: auto;
  min-width: 8.5rem;
  font-size: 0.8rem;
}

.member-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.member-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
  padding: 0.9rem 1.1rem;
}

.member-identity {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.member-email {
  font-weight: 650;
  color: var(--color-navy);
}

.member-meta {
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.member-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.role-owner {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 35%);
}

.role-admin {
  background: #eef2ff;
  color: #3730a3;
}

.role-maintainer {
  background: #eef2ff;
  color: #3730a3;
}

.role-member {
  background: #f3f4f6;
  color: var(--color-text);
}

.role-reviewer {
  background: #f3f4f6;
  color: var(--color-text);
}

.role-viewer {
  background: #f3f4f6;
  color: var(--color-text-muted);
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 3.4rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
