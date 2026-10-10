<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import AuditEventRow from '@/components/AuditEventRow.vue';
import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import { useAuditStore } from '@/stores/audit';
import { useProjectsStore } from '@/stores/projects';

const route = useRoute();
const router = useRouter();
const projects = useProjectsStore();
const audit = useAuditStore();

const projectId = ref(String(route.params.projectId ?? ''));

const description = ref('');
const settingsText = ref('{}');
const settingsError = ref('');
const saveError = ref('');

const actionFilter = ref('');

const conflict = computed(() => projects.conflict);
const project = computed(() => projects.current);

onMounted(() => {
  void reload();
});

watch(
  () => route.params.projectId,
  (next) => {
    if (typeof next !== 'string' || next === projectId.value) return;
    projectId.value = next;
    void reload();
  },
);

async function reload(): Promise<void> {
  settingsError.value = '';
  saveError.value = '';
  if (projectId.value === '') return;
  await Promise.allSettled([projects.loadOne(projectId.value), audit.load(projectId.value)]);
  const loaded = projects.byId(projectId.value);
  if (loaded) {
    description.value = loaded.description;
  }
}

function parseSettings(): Record<string, unknown> | null {
  const raw = settingsText.value.trim();
  if (raw === '') return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (cause) {
    settingsError.value = `Settings must be valid JSON: ${
      cause instanceof Error ? cause.message : 'parse error'
    }`;
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    settingsError.value = 'Settings must be a JSON object.';
    return null;
  }
  settingsError.value = '';
  return parsed as Record<string, unknown>;
}

async function save(): Promise<void> {
  const current = projects.byId(projectId.value);
  if (!current) return;
  const settings = parseSettings();
  if (settings === null) return;
  saveError.value = '';
  try {
    // The version sent is the one the server last returned; a mismatch fails
    // the whole save instead of overwriting the newer server state.
    await projects.update(projectId.value, {
      version: current.version,
      description: description.value,
      settings,
    });
  } catch (cause) {
    saveError.value = cause instanceof Error ? cause.message : 'Save failed.';
  }
}

function applyActionFilter(): void {
  if (projectId.value === '') return;
  void audit.load(projectId.value, actionFilter.value.trim());
}
</script>

<template>
  <div class="project-detail">
    <button
      class="back"
      type="button"
      data-testid="back-to-projects"
      @click="router.push('/projects')"
    >
      ← All projects
    </button>

    <p v-if="projects.error" class="banner banner-error" data-testid="detail-error">
      <strong>Could not load this project.</strong> {{ projects.error }}
      <button class="retry" type="button" data-testid="detail-retry" @click="reload()">
        Retry
      </button>
    </p>

    <section v-if="projects.loading" class="card" data-testid="detail-loading">
      <div class="skeleton" />
    </section>

    <template v-else-if="project">
      <header class="detail-header card">
        <div>
          <h2 class="detail-title">{{ project.name }}</h2>
          <p class="detail-id">id {{ project.id }}</p>
        </div>
        <div class="detail-facts">
          <span class="badge badge-version">version {{ project.version }}</span>
          <span class="muted detail-updated"
            >updated {{ new Date(project.updated_at).toLocaleString() }}</span
          >
        </div>
      </header>

      <p v-if="conflict" class="banner banner-conflict" data-testid="conflict-banner">
        <strong>This project was modified elsewhere — reload to see the latest version.</strong>
        <span v-if="conflict.currentVersion !== null">
          Server is now at version {{ conflict.currentVersion }}.
        </span>
        <button class="retry" type="button" data-testid="conflict-reload" @click="reload()">
          Reload
        </button>
      </p>

      <p v-if="saveError" class="banner banner-error" data-testid="save-error">
        <strong>Save rejected.</strong> {{ saveError }}
      </p>

      <section class="card editor" data-testid="project-editor">
        <h3 class="card-title">Settings</h3>
        <FormField
          label="Description"
          field-id="detail-description"
          hint="Sent with the next save. Version is required for every update."
        >
          <textarea id="detail-description" v-model="description" rows="3" maxlength="2000" />
        </FormField>
        <FormField
          label="Settings (JSON)"
          field-id="detail-settings"
          :error="settingsError"
          hint="Object of project settings. The API contract does not return stored settings yet, so start from {} and save what you change."
        >
          <textarea
            id="detail-settings"
            v-model="settingsText"
            rows="6"
            spellcheck="false"
            data-testid="settings-input"
          />
        </FormField>
        <div class="editor-actions">
          <button
            class="primary"
            type="button"
            data-testid="save-project"
            :disabled="projects.saving"
            @click="save()"
          >
            {{ projects.saving ? 'Saving…' : 'Save' }}
          </button>
          <span class="muted">Saving sends version {{ project.version }} with your changes.</span>
        </div>
      </section>

      <section class="card audit" data-testid="audit-trail">
        <header class="audit-head">
          <h3 class="card-title">Audit trail</h3>
          <form class="audit-filter" @submit.prevent="applyActionFilter">
            <input
              v-model="actionFilter"
              type="search"
              placeholder="Filter by action, e.g. project.created"
              aria-label="Filter audit events by action"
              data-testid="audit-filter"
            />
            <button class="ghost" type="submit" data-testid="audit-filter-submit">Apply</button>
          </form>
        </header>

        <p v-if="audit.error" class="banner banner-error" data-testid="audit-error">
          <strong>Could not load audit events.</strong> {{ audit.error }}
          <button class="retry" type="button" data-testid="audit-retry" @click="applyActionFilter">
            Retry
          </button>
        </p>

        <div v-if="audit.loading" data-testid="audit-loading">
          <div class="skeleton" />
        </div>

        <EmptyState
          v-else-if="audit.items.length === 0"
          title="No audit events yet"
          description="Every create, update and run appears here as soon as it happens."
          test-id="empty-audit"
        />

        <ul v-else class="audit-list" data-testid="audit-list">
          <AuditEventRow v-for="event in audit.items" :key="event.id" :event="event" />
        </ul>

        <div v-if="audit.nextCursor !== null" class="load-more">
          <button
            class="ghost"
            type="button"
            data-testid="audit-load-more"
            :disabled="audit.loadingMore"
            @click="audit.loadMore()"
          >
            {{ audit.loadingMore ? 'Loading…' : 'Load more' }}
          </button>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.project-detail {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.back {
  align-self: flex-start;
  border: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.3rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.back:hover {
  border-color: var(--color-navy);
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
}

.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.detail-title {
  font-size: 1.4rem;
}

.detail-id {
  margin: 0.2rem 0 0;
  font-size: 0.76rem;
  color: var(--color-text-muted);
  word-break: break-all;
}

.detail-facts {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.badge-version {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 35%);
}

.detail-updated {
  font-size: 0.78rem;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
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
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
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

.editor {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}

.card-title {
  font-size: 1rem;
}

textarea,
input {
  font: inherit;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
}

textarea:focus,
input:focus {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

#detail-settings {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.82rem;
}

.editor-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
  font-size: 0.8rem;
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

.audit-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
  margin-bottom: 0.9rem;
}

.audit-filter {
  display: flex;
  gap: 0.5rem;
}

.audit-filter input {
  min-width: 16rem;
}

.audit-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.load-more {
  display: flex;
  justify-content: center;
  margin-top: 1rem;
}

.skeleton {
  height: 6rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
  margin-bottom: 1rem;
}
</style>
