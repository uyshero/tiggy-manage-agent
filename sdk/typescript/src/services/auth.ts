import type { AuthClientConfiguration, AuthState, TokenExchangeRequest, TokenExchangeResponse } from "../types.js";
import { ServiceBase } from "./base.js";

export class AuthService extends ServiceBase {
  configuration(signal?: AbortSignal): Promise<AuthClientConfiguration> {
    return this.transport.requestJSON("GET", "/v2/auth/config", undefined, signal ? { signal } : {});
  }

  me(signal?: AbortSignal): Promise<AuthState> {
    return this.transport.requestJSON("GET", "/v2/auth/me", undefined, signal ? { signal } : {});
  }

  exchange(request: TokenExchangeRequest, signal?: AbortSignal): Promise<TokenExchangeResponse> {
    return this.transport.requestJSON("POST", "/v2/auth/token-exchange", request, signal ? { signal } : {});
  }
}
