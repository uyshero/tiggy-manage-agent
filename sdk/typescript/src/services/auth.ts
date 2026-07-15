import type { AuthClientConfiguration, AuthState } from "../types.js";
import { ServiceBase } from "./base.js";

export class AuthService extends ServiceBase {
  configuration(signal?: AbortSignal): Promise<AuthClientConfiguration> {
    return this.transport.requestJSON("GET", "/v2/auth/config", undefined, signal ? { signal } : {});
  }

  me(signal?: AbortSignal): Promise<AuthState> {
    return this.transport.requestJSON("GET", "/v2/auth/me", undefined, signal ? { signal } : {});
  }
}
