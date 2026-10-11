import { ref } from 'vue';
import { defineStore } from 'pinia';

import { ApiError, createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type { Member, MemberPage, Role } from '@/api/types';
import { errorMessage } from '@/stores/organizations';

export interface MemberRejection {
  /** message is the server's explanation of a 409 (last owner, self-escalation). */
  message: string;
}

export const useMembersStore = defineStore('members', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<Member[]>([]);
  const organizationId = ref<string | null>(null);
  const loading = ref(false);
  const inviting = ref(false);
  const updatingMembershipId = ref<string | null>(null);
  const removingMembershipId = ref<string | null>(null);
  const error = ref<string | null>(null);
  const conflict = ref<MemberRejection | null>(null);

  async function load(organization: string): Promise<void> {
    loading.value = true;
    error.value = null;
    conflict.value = null;
    organizationId.value = organization;
    try {
      const page: MemberPage = await client.listMembers({ organization_id: organization });
      items.value = page.items;
    } catch (cause) {
      items.value = [];
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  async function invite(organization: string, email: string, role: Role): Promise<Member> {
    inviting.value = true;
    error.value = null;
    conflict.value = null;
    try {
      const member = await client.inviteMember(
        { organization_id: organization, email: email.trim(), role },
        newIdempotencyKey(),
      );
      await load(organization);
      return member;
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        conflict.value = { message: cause.message };
      } else {
        error.value = errorMessage(cause);
      }
      throw cause;
    } finally {
      inviting.value = false;
    }
  }

  async function changeRole(membershipId: string, role: Role): Promise<Member> {
    updatingMembershipId.value = membershipId;
    error.value = null;
    conflict.value = null;
    try {
      const updated = await client.updateMemberRole(membershipId, { role }, newIdempotencyKey());
      items.value = items.value.map((item) => (item.id === updated.id ? updated : item));
      return updated;
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        conflict.value = { message: cause.message };
      } else {
        error.value = errorMessage(cause);
      }
      throw cause;
    } finally {
      updatingMembershipId.value = null;
    }
  }

  async function remove(membershipId: string): Promise<void> {
    removingMembershipId.value = membershipId;
    error.value = null;
    conflict.value = null;
    try {
      await client.removeMember(membershipId);
      items.value = items.value.filter((item) => item.id !== membershipId);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        conflict.value = { message: cause.message };
      } else {
        error.value = errorMessage(cause);
      }
      throw cause;
    } finally {
      removingMembershipId.value = null;
    }
  }

  function byId(membershipId: string): Member | null {
    return items.value.find((item) => item.id === membershipId) ?? null;
  }

  return {
    client,
    items,
    organizationId,
    loading,
    inviting,
    updatingMembershipId,
    removingMembershipId,
    error,
    conflict,
    byId,
    load,
    invite,
    changeRole,
    remove,
  };
});
