<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import { createHealthClient, type HealthClient } from '@/api/client';
import type { ModelProviderStatus } from '@/api/types';

const client: HealthClient = createHealthClient();
const status = ref<ModelProviderStatus | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    status.value = await client.getModelProvider();
  } catch (cause) {
    status.value = null;
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

/** keyConfigured reports whether a provider key is present without ever
 * exposing it: the API returns only this flag. */
const keyConfigured = computed<boolean>(() => status.value?.key_configured ?? false);
const configured = computed<boolean>(() => status.value?.configured ?? false);
</script>

<template>
  <div class="models-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Models</h2>
        <p class="muted page-subtitle">
          The inference engine this deployment talks to. A credential is reported as present or
          absent only — the key itself is never returned to the browser.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="models-reload"
        :disabled="loading"
        @click="load()"
      >
        {{ loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <p v-if="error" class="banner banner-error" data-testid="models-error">
      <strong>Could not read the provider status.</strong>
      {{ error }}
      <button class="retry" type="button" data-testid="models-retry" @click="load()">Retry</button>
    </p>

    <div v-if="loading && status === null" data-testid="models-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="status === null"
      title="No provider status yet"
      description="The API has not reported a model provider configuration for this deployment."
      test-id="models-empty"
    />

    <section v-else class="card panel" data-testid="models-panel">
      <dl class="facts">
        <div>
          <dt>Provider</dt>
          <dd data-testid="models-provider">{{ status.provider }}</dd>
        </div>
        <div>
          <dt>Model</dt>
          <dd data-testid="models-model">{{ status.model }}</dd>
        </div>
        <div>
          <dt>Base URL</dt>
          <dd data-testid="models-base-url">{{ status.base_url }}</dd>
        </div>
        <div>
          <dt>Reasoning effort</dt>
          <dd data-testid="models-effort">{{ status.reasoning_effort }}</dd>
        </div>
        <div>
          <dt>Credential</dt>
          <dd data-testid="models-key">{{ keyConfigured ? 'present' : 'absent' }}</dd>
        </div>
        <div>
          <dt>Client wired</dt>
          <dd data-testid="models-configured">{{ configured ? 'yes' : 'no' }}</dd>
        </div>
      </dl>
    </section>
  </div>
</template>
