import { ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, type HealthClient } from '@/api/client';
import type { Alert } from '@/api/types';

export const useAlertsStore = defineStore('alerts', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<Alert[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      items.value = await client.getAlerts();
    } catch (cause) {
      items.value = [];
      error.value = cause instanceof Error ? cause.message : String(cause);
    } finally {
      loading.value = false;
    }
  }

  return { client, items, loading, error, load };
});
