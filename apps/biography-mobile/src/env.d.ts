/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_BIOGRAPHY_VOICE_GATEWAY_URL?: string;
  readonly VITE_BIOGRAPHY_VOICE_DEBUG_TEXT?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

declare module "*.vue" {
  import type { DefineComponent } from "vue";
  const component: DefineComponent<Record<string, never>, Record<string, never>, unknown>;
  export default component;
}
