<script setup lang="ts">
import { useHealthStore } from '@/stores/health';

const health = useHealthStore();

const statusLabels: Record<string, string> = {
  ready: 'All systems operational',
  unavailable: 'Degraded — action required',
  loading: 'Checking dependencies…',
  error: 'Cannot reach the API',
  idle: 'Initialising',
};
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">RS</span>
        <div>
          <h1 class="brand-name">RepoSurgeon</h1>
          <p class="brand-subtitle">Autonomous software engineering workspace</p>
        </div>
      </div>
      <div class="header-meta">
        <span v-if="health.system" class="badge badge-env">{{ health.system.environment }}</span>
        <span v-if="health.system" class="version">v{{ health.system.version }}</span>
      </div>
    </header>

    <main class="app-main">
      <RouterView />
    </main>

    <footer class="app-footer">
      <span data-testid="footer-status">{{ statusLabels[health.status] }}</span>
      <span v-if="health.lastCheckedAt">
        last checked {{ new Date(health.lastCheckedAt).toLocaleTimeString() }}
      </span>
    </footer>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.75rem;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-gray-line);
}

.brand {
  display: flex;
  align-items: center;
  gap: 0.85rem;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 2.4rem;
  height: 2.4rem;
  border-radius: var(--radius-sm);
  background: var(--color-navy);
  color: var(--color-gold);
  font-weight: 800;
  letter-spacing: 0.04em;
}

.brand-name {
  font-size: 1.15rem;
}

.brand-subtitle {
  margin: 0.1rem 0 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.header-meta {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.version {
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.app-main {
  flex: 1;
  width: min(1120px, 100%);
  margin: 0 auto;
  padding: 2rem 1.75rem;
}

.app-footer {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.9rem 1.75rem;
  border-top: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

@media (max-width: 640px) {
  .app-header,
  .app-main,
  .app-footer {
    padding-left: 1rem;
    padding-right: 1rem;
  }
}
</style>
