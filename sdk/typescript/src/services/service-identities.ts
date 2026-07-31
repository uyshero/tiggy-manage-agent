import type {
  CreateServiceCredentialRequest,
  CreateServiceIdentityRequest,
  CreatedServiceCredential,
  ServiceCredential,
  ServiceIdentity,
  UpdateServiceIdentityRequest,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class ServiceIdentitiesService extends ServiceBase {
  scopes(signal?: AbortSignal): Promise<string[]> {
    return this.transport.requestJSON<{ scopes: string[] }>("GET", "/v2/service-identities/scopes", undefined, signal ? { signal } : {}).then((value) => value.scopes);
  }

  list(signal?: AbortSignal): Promise<ServiceIdentity[]> {
    return this.transport.requestJSON<{ service_identities: ServiceIdentity[] }>("GET", "/v2/service-identities", undefined, signal ? { signal } : {}).then((value) => value.service_identities);
  }

  create(request: CreateServiceIdentityRequest, signal?: AbortSignal): Promise<ServiceIdentity> {
    return this.transport.requestJSON("POST", "/v2/service-identities", request, signal ? { signal } : {});
  }

  get(identityId: string, signal?: AbortSignal): Promise<ServiceIdentity> {
    return this.transport.requestJSON("GET", serviceIdentityPath(identityId), undefined, signal ? { signal } : {});
  }

  update(identityId: string, request: UpdateServiceIdentityRequest, signal?: AbortSignal): Promise<ServiceIdentity> {
    return this.transport.requestJSON("PATCH", serviceIdentityPath(identityId), request, signal ? { signal } : {});
  }

  credentials(identityId: string, signal?: AbortSignal): Promise<ServiceCredential[]> {
    return this.transport.requestJSON<{ credentials: ServiceCredential[] }>("GET", serviceIdentityPath(identityId) + "/credentials", undefined, signal ? { signal } : {}).then((value) => value.credentials);
  }

  createCredential(identityId: string, request: CreateServiceCredentialRequest, signal?: AbortSignal): Promise<CreatedServiceCredential> {
    return this.transport.requestJSON("POST", serviceIdentityPath(identityId) + "/credentials", request, signal ? { signal } : {});
  }

  revokeCredential(identityId: string, credentialId: string, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", serviceIdentityPath(identityId) + "/credentials/" + encodeURIComponent(credentialId), undefined, signal ? { signal } : {});
  }
}

function serviceIdentityPath(identityId: string): string {
  return resourcePath("/v2/service-identities", identityId);
}
