<script setup lang="ts">
import { computed } from 'vue';

import type { ReadinessCheck } from '@/api/types';

const props = defineProps<{ check: ReadinessCheck }>();

const tone = computed(() => (props.check.status === 'up' ? 'ok' : 'fail'));
</script>

<template>
  <li class="check-row" :class="`check-row-${tone}`" :data-testid="`check-${check.name}`">
    <div class="check-row-head">
      <span class="check-name">{{ check.name }}</span>
      <span class="check-status">{{ check.status }}</span>
    </div>
    <p v-if="check.detail" class="check-detail">{{ check.detail }}</p>
    <span class="check-duration">{{ check.duration_ms }} ms</span>
  </li>
</template>

<style scoped>
.check-row {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.7rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  background: #fff;
}

.check-row-fail {
  border-color: rgb(180 35 24 / 35%);
  background: var(--color-danger-soft);
}

.check-row-head {
  display: flex;
  justify-content: space-between;
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--color-navy);
}

.check-status {
  text-transform: uppercase;
  font-size: 0.7rem;
  letter-spacing: 0.08em;
}

.check-row-fail .check-status {
  color: var(--color-danger);
}

.check-row-ok .check-status {
  color: var(--color-ok);
}

.check-detail {
  margin: 0;
  font-size: 0.82rem;
  color: var(--color-text-muted);
}

.check-duration {
  font-size: 0.72rem;
  color: var(--color-text-muted);
}
</style>
