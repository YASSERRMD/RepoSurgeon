/// <reference types="vite/client" />

// Vue 3 SFC modules. vue-tsc understands .vue natively; this declaration lets
// the ESLint TypeScript service type SFC imports too.
declare module '*.vue' {
  import type { DefineComponent } from 'vue';

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>;
  export default component;
}
