/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_BIOGRAPHY_VOICE_GATEWAY_URL?: string;
  readonly VITE_BIOGRAPHY_VOICE_DEBUG_TEXT?: string;
  readonly VITE_BIOGRAPHY_FOLLOWUP_DELAY_MS?: string;
  readonly VITE_BIOGRAPHY_AUTH_BASE_URL?: string;
  readonly VITE_BIOGRAPHY_AUTH_LOGIN_URL?: string;
  readonly VITE_BIOGRAPHY_AUTH_REDIRECT_URL?: string;
  readonly VITE_BIOGRAPHY_AUTH_DEV_TOKEN_INPUT?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

declare module "*.vue" {
  import type { DefineComponent } from "vue";
  const component: DefineComponent<Record<string, never>, Record<string, never>, unknown>;
  export default component;
}
