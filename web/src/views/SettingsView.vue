<script setup lang="ts">
import { onMounted, ref } from 'vue';

import FormField from '@/components/FormField.vue';
import { useConsoleStore } from '@/stores/console';

const store = useConsoleStore();

/** view is the settings document the server stores; the shell reads it back. */
const view = ref<Record<string, unknown>>({});
const saved = ref(false);
const errorText = ref('');

async function load(): Promise<void> {
  await store.loadPreferences();
  const stored = store.preferences?.view;
  view.value = typeof stored === 'object' && stored !== null ? { ...stored } : {};
}

onMounted(load);

async function save(): Promise<void> {
  errorText.value = '';
  const ok = await store.savePreferences(view.value);
  saved.value = ok;
  if (!ok) {
    errorText.value = store.error ?? 'the settings could not be stored';
  }
}

function tableDensity(): string {
  const value = view.value['table_density'];
  return typeof value === 'string' ? value : 'comfortable';
}

function setTableDensity(next: string): void {
  view.value = { ...view.value, table_density: next };
  saved.value = false;
}

const activeSection = (): string => {
  const value = view.value['active_section'];
  return typeof value === 'string' ? value : 'overview';
};

function setActiveSection(next: string): void {
  view.value = { ...view.value, active_section: next };
  saved.value = false;
}

const storedAt = (): string => {
  const value = store.preferences?.updated_at;
  return value === undefined || value === '' ? 'not stored yet' : new Date(value).toLocaleString();
};
</script>

<template>
  <div class="settings-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Settings</h2>
        <p class="muted page-subtitle">
          Your view settings for this console. They are stored by the server, so they survive a
          reload and are not written into shared storage.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="settings-reload"
        :disabled="store.loading"
        @click="load()"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <section class="card panel" data-testid="settings-panel">
      <FormField
        label="Landing section"
        field-id="settings-section"
        hint="The section the console opens on. Stored as part of your view settings."
      >
        <select
          id="settings-section"
          :value="activeSection()"
          data-testid="settings-section-select"
          @change="setActiveSection(($event.target as HTMLSelectElement).value)"
        >
          <option value="overview">Overview</option>
          <option value="projects">Projects</option>
          <option value="issues">Issues</option>
          <option value="runs">Runs</option>
          <option value="workspaces">Workspaces</option>
          <option value="models">Models</option>
          <option value="approvals">Approvals</option>
          <option value="audit">Audit</option>
        </select>
      </FormField>

      <FormField
        label="Table density"
        field-id="settings-density"
        hint="How tightly the console packs its tables."
      >
        <select
          id="settings-density"
          :value="tableDensity()"
          data-testid="settings-density-select"
          @change="setTableDensity(($event.target as HTMLSelectElement).value)"
        >
          <option value="comfortable">Comfortable</option>
          <option value="compact">Compact</option>
        </select>
      </FormField>

      <p v-if="saved" class="banner banner-ok" data-testid="settings-saved">
        Settings stored at {{ storedAt() }}.
      </p>
      <p v-if="errorText !== ''" class="banner banner-error" data-testid="settings-error">
        {{ errorText }}
      </p>

      <div class="action-row">
        <button
          class="primary"
          type="button"
          data-testid="settings-save"
          :disabled="store.saving"
          @click="save()"
        >
          {{ store.saving ? 'Storing…' : 'Store settings' }}
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.action-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 0.75rem;
}
</style>
