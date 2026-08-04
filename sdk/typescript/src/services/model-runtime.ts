import type {
  ModelEmbeddingRequest,
  ModelEmbeddingResponse,
  ModelGenerateRequest,
  ModelGenerateResponse,
  ModelInvocationQuery,
  ModelInvocationReport,
  ModelRerankRequest,
  ModelRerankResponse,
} from "../types.js";
import { ServiceBase, withQuery } from "./base.js";
import { MULTIMODAL_REALTIME_PROTOCOL, MultimodalRealtimeSession, type MultimodalRealtimeOptions } from "../multimodal-realtime.js";

export class ModelRuntimeService extends ServiceBase {
  connectMultimodalRealtime(options: MultimodalRealtimeOptions = {}): MultimodalRealtimeSession {
    const socket = this.transport.openWebSocket("/v2/model-runtime/multimodal/realtime", MULTIMODAL_REALTIME_PROTOCOL);
    return new MultimodalRealtimeSession(socket, options);
  }

  generate(request: ModelGenerateRequest, signal?: AbortSignal): Promise<ModelGenerateResponse> {
    return this.transport.requestJSON("POST", "/v2/model-runtime/generate", request, signal ? { signal } : {});
  }

  embed(request: ModelEmbeddingRequest, signal?: AbortSignal): Promise<ModelEmbeddingResponse> {
    return this.transport.requestJSON("POST", "/v2/model-runtime/embeddings", request, signal ? { signal } : {});
  }

  rerank(request: ModelRerankRequest, signal?: AbortSignal): Promise<ModelRerankResponse> {
    return this.transport.requestJSON("POST", "/v2/model-runtime/rerank", request, signal ? { signal } : {});
  }

  invocations(query: ModelInvocationQuery = {}, signal?: AbortSignal): Promise<ModelInvocationReport> {
    const path = withQuery("/v2/model-runtime/invocations", {
      principal_id: query.principalId,
      service_identity_id: query.serviceIdentityId,
      capability: query.capability,
      provider_id: query.providerId,
      model: query.model,
      status: query.status,
      from: formatTime(query.from),
      to: formatTime(query.to),
      limit: query.limit,
    });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }
}

function formatTime(value?: Date | string): string | undefined {
  if (value === undefined) return undefined;
  return value instanceof Date ? value.toISOString() : value;
}
