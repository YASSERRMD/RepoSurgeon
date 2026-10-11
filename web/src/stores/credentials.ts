import { ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type { Credential, CredentialKind, CredentialPage } from '@/api/types';
import { errorMessage } from '@/stores/organizations';

/** The contract never returns a credential value; only metadata is listed. */
export const useCredentialsStore = defineStore('credentials', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<Credential[]>([]);
  const organizationId = ref<string | null>(null);
  const loading = ref(false);
  const creating = ref(false);
  const rotatingCredentialId = ref<string | null>(null);
  const revokingCredentialId = ref<string | null>(null);
  const error = ref<string | null>(null);

  async function load(organization: string): Promise<void> {
    loading.value = true;
    error.value = null;
    organizationId.value = organization;
    try {
      const page: CredentialPage = await client.listCredentials({ organization_id: organization });
      items.value = page.items;
    } catch (cause) {
      items.value = [];
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  async function create(input: {
    organization_id: string;
    kind: CredentialKind;
    label: string;
    value: string;
  }): Promise<Credential> {
    creating.value = true;
    error.value = null;
    try {
      const created = await client.createCredential(input, newIdempotencyKey());
      await load(input.organization_id);
      return created;
    } catch (cause) {
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      creating.value = false;
    }
  }

  async function rotate(credentialId: string, value: string): Promise<Credential> {
    rotatingCredentialId.value = credentialId;
    error.value = null;
    try {
      const rotated = await client.rotateCredential(credentialId, { value }, newIdempotencyKey());
      items.value = items.value.map((item) => (item.id === rotated.id ? rotated : item));
      return rotated;
    } catch (cause) {
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      rotatingCredentialId.value = null;
    }
  }

  async function revoke(credentialId: string): Promise<void> {
    revokingCredentialId.value = credentialId;
    error.value = null;
    try {
      await client.revokeCredential(credentialId);
      items.value = items.value.filter((item) => item.id !== credentialId);
    } catch (cause) {
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      revokingCredentialId.value = null;
    }
  }

  function byId(credentialId: string): Credential | null {
    return items.value.find((item) => item.id === credentialId) ?? null;
  }

  return {
    client,
    items,
    organizationId,
    loading,
    creating,
    rotatingCredentialId,
    revokingCredentialId,
    error,
    byId,
    load,
    create,
    rotate,
    revoke,
  };
});
