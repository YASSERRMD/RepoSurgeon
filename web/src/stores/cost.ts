import { ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, type HealthClient } from '@/api/client';
import type { CostAggregate } from '@/api/types';

export const useCostStore = defineStore('cost', () => {
  const client: HealthClient = createHealthClient();
  const aggregate = ref<CostAggregate | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      aggregate.value = await client.getCost();
    } catch (cause) {
      aggregate.value = null;
      error.value = cause instanceof Error ? cause.message : String(cause);
    } finally {
      loading.value = false;
    }
  }

  return { client, aggregate, loading, error, load };
});
