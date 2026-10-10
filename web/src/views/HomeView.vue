<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue';

import CheckRow from '@/components/CheckRow.vue';
import StatusCard from '@/components/StatusCard.vue';
import { useHealthStore } from '@/stores/health';

const health = useHealthStore();
const refreshing = computed(() => health.status === 'loading');

// The view owns its data: probe immediately, then poll until unmounted.
onMounted(() => {
  void health.refresh();
  health.startPolling();
});

onUnmounted(() => {
  health.stopPolling();
});

function statusTone(): 'up' | 'down' | 'unknown' {
  if (health.status === 'ready') return 'up';
  if (health.status === 'unavailable' || health.status === 'error') return 'down';
  return 'unknown';
}

function uptime(): string {
  if (!health.system) return '—';
  const seconds = Math.round(health.system.uptime_seconds);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${seconds % 60}s`;
}
</script>

<template>
  <div class="home">
    <div class="home-head">
      <div>
        <h2 class="page-title">Service status</h2>
        <p class="muted page-subtitle">
          Live probes against the RepoSurgeon API. Values are measured, never simulated.
        </p>
      </div>
      <button class="refresh" type="button" :disabled="refreshing" @click="health.refresh()">
        {{ refreshing ? 'Checking…' : 'Refresh now' }}
      </button>
    </div>

    <p v-if="health.error" class="banner banner-error" data-testid="error-banner">
      <strong>API unreachable.</strong> {{ health.error }} Start the stack with
      <code>make dev</code> or see docs for the exact prerequisites.
    </p>

    <div class="grid">
      <StatusCard
        title="Liveness"
        :status="health.live.ok ? 'up' : 'down'"
        :detail="health.live.error || 'The API process is serving requests.'"
        test-id="liveness-card"
      />

      <StatusCard
        title="Readiness"
        :status="statusTone()"
        :detail="
          health.readiness
            ? `Reported by the API at ${new Date(health.readiness.generated_at).toLocaleTimeString()}`
            : 'Waiting for the first readiness report…'
        "
        test-id="readiness-card"
      >
        <ul v-if="health.readiness" class="checks">
          <CheckRow v-for="check in health.readiness.checks" :key="check.name" :check="check" />
        </ul>
      </StatusCard>

      <StatusCard
        title="Service build"
        status="unknown"
        :detail="health.system ? 'Reported by /api/v1/system/info' : 'Build metadata unavailable'"
        test-id="system-card"
      >
        <dl v-if="health.system" class="facts">
          <div>
            <dt>Service</dt>
            <dd data-testid="fact-name">{{ health.system.name }}</dd>
          </div>
          <div>
            <dt>Version</dt>
            <dd data-testid="fact-version">{{ health.system.version }}</dd>
          </div>
          <div>
            <dt>Commit</dt>
            <dd>{{ health.system.git_commit }}</dd>
          </div>
          <div>
            <dt>Go runtime</dt>
            <dd>{{ health.system.go_version }}</dd>
          </div>
          <div>
            <dt>Environment</dt>
            <dd>{{ health.system.environment }}</dd>
          </div>
          <div>
            <dt>Uptime</dt>
            <dd data-testid="fact-uptime">{{ uptime() }}</dd>
          </div>
        </dl>
      </StatusCard>
    </div>

    <section
      v-if="health.failingChecks.length > 0"
      class="remediation card"
      data-testid="remediation"
    >
      <h3>Action required</h3>
      <p class="muted">The following dependencies are failing. Each detail explains the fix.</p>
      <ul>
        <li v-for="check in health.failingChecks" :key="check.name">
          <strong>{{ check.name }}</strong>
          <span>{{ check.detail }}</span>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.home-head {
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
  max-width: 60ch;
}

.refresh {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  font-weight: 600;
}

.refresh:hover:not(:disabled) {
  background: var(--color-navy-soft);
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

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 1rem;
  align-items: start;
}

.checks {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.facts {
  margin: 0;
  display: grid;
  gap: 0.5rem;
}

.facts div {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 1px dashed var(--color-gray-line);
  padding-bottom: 0.4rem;
}

.facts dt {
  color: var(--color-text-muted);
  font-size: 0.82rem;
}

.facts dd {
  margin: 0;
  font-weight: 600;
  font-size: 0.85rem;
  text-align: right;
}

.remediation h3 {
  font-size: 1rem;
  color: var(--color-danger);
}

.remediation ul {
  margin: 0.5rem 0 0;
  padding-left: 1.1rem;
  display: grid;
  gap: 0.5rem;
}

.remediation li {
  display: flex;
  flex-direction: column;
  font-size: 0.85rem;
}

.remediation strong {
  color: var(--color-navy);
}

.remediation span {
  color: var(--color-text-muted);
}

@media (max-width: 640px) {
  .home-head {
    align-items: flex-start;
  }
}
</style>
