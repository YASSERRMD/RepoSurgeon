<script setup lang="ts">
defineProps<{
  label: string;
  error?: string;
  hint?: string;
  fieldId: string;
}>();
</script>

<template>
  <div class="form-field">
    <label class="form-label" :for="fieldId">{{ label }}</label>
    <p v-if="hint" class="form-hint">{{ hint }}</p>
    <slot />
    <p v-if="error" class="form-error" :data-testid="`${fieldId}-error`">{{ error }}</p>
  </div>
</template>

<style scoped>
.form-field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.form-label {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-navy);
}

.form-hint {
  margin: 0;
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.form-error {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-danger);
}
</style>
