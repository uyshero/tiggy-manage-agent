import type {
  ArtifactExchange,
  ArtifactExchangeGrant,
  ArtifactExchangeImportResult,
  CreateArtifactExportExchangeRequest,
  CreateArtifactImportExchangeRequest,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class ArtifactExchangesService extends ServiceBase {
  createImport(request: CreateArtifactImportExchangeRequest, signal?: AbortSignal): Promise<ArtifactExchangeGrant> {
    return this.transport.requestJSON("POST", "/v2/artifact-exchanges/imports", request, signal ? { signal } : {});
  }

  createExport(request: CreateArtifactExportExchangeRequest, signal?: AbortSignal): Promise<ArtifactExchangeGrant> {
    return this.transport.requestJSON("POST", "/v2/artifact-exchanges/exports", request, signal ? { signal } : {});
  }

  get(exchangeId: string, signal?: AbortSignal): Promise<ArtifactExchange> {
    return this.transport.requestJSON("GET", resourcePath("/v2/artifact-exchanges", exchangeId), undefined, signal ? { signal } : {});
  }

  async upload(grant: ArtifactExchangeGrant, body: BodyInit, contentType = "application/octet-stream", signal?: AbortSignal): Promise<ArtifactExchangeImportResult> {
    const response = await this.transport.request("PUT", grant.content_url, {
      body,
      headers: { "Content-Type": contentType },
      ...(signal ? { signal } : {}),
    });
    return await response.json() as ArtifactExchangeImportResult;
  }

  download(grant: ArtifactExchangeGrant, signal?: AbortSignal): Promise<Response> {
    return this.transport.request("GET", grant.content_url, signal ? { signal } : {});
  }
}
