import type {
  CreateLLMProviderRequest,
  LLMModel,
  LLMDiagnosticResult,
  LLMProvider,
  LLMUsageAggregateReport,
  LLMUsageQuery,
  PutLLMModelRequest,
  UpdateLLMProviderRequest,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class LLMService extends ServiceBase {
  listProviders(signal?: AbortSignal): Promise<LLMProvider[]> {
    return this.transport.requestJSON<{ providers: LLMProvider[] }>("GET", "/v2/llm-providers", undefined, signal ? { signal } : {}).then((value) => value.providers);
  }

  getProvider(providerId: string, signal?: AbortSignal): Promise<LLMProvider> {
    return this.transport.requestJSON("GET", providerPath(providerId), undefined, signal ? { signal } : {});
  }

  createProvider(request: CreateLLMProviderRequest, signal?: AbortSignal): Promise<LLMProvider> {
    return this.transport.requestJSON("POST", "/v2/llm-providers", request, signal ? { signal } : {});
  }

  updateProvider(providerId: string, expectedRevision: number, request: UpdateLLMProviderRequest, signal?: AbortSignal): Promise<LLMProvider> {
    return this.transport.requestJSON("PATCH", providerPath(providerId), request, requestOptions(expectedRevision, signal));
  }

  setProviderEnabled(providerId: string, expectedRevision: number, enabled: boolean, signal?: AbortSignal): Promise<LLMProvider> {
    return this.transport.requestJSON("POST", `${providerPath(providerId)}/${enabled ? "enable" : "disable"}`, {}, requestOptions(expectedRevision, signal));
  }

  deleteProvider(providerId: string, expectedRevision: number, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", providerPath(providerId), undefined, requestOptions(expectedRevision, signal));
  }

  testProvider(providerId: string, signal?: AbortSignal): Promise<LLMDiagnosticResult> {
    return this.transport.requestJSON("POST", `${providerPath(providerId)}/test`, {}, signal ? { signal } : {});
  }

  listModels(providerId?: string, signal?: AbortSignal): Promise<LLMModel[]> {
    const path = withQuery("/v2/llm-models", { provider_id: providerId });
    return this.transport.requestJSON<{ models: LLMModel[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.models);
  }

  createModel(request: PutLLMModelRequest, signal?: AbortSignal): Promise<LLMModel> {
    return this.transport.requestJSON("POST", "/v2/llm-models", request, {
      headers: { "If-None-Match": "*" },
      ...(signal === undefined ? {} : { signal }),
    });
  }

  updateModel(expectedRevision: number, request: PutLLMModelRequest, signal?: AbortSignal): Promise<LLMModel> {
    return this.transport.requestJSON("POST", "/v2/llm-models", request, requestOptions(expectedRevision, signal));
  }

  deleteModel(providerId: string, model: string, expectedRevision: number, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", resourcePath("/v2/llm-models", providerId, model), undefined, requestOptions(expectedRevision, signal));
  }

  testModel(providerId: string, model: string, signal?: AbortSignal): Promise<LLMDiagnosticResult> {
    return this.transport.requestJSON("POST", `${resourcePath("/v2/llm-models", providerId, model)}/test`, {}, signal ? { signal } : {});
  }

  usage(query: LLMUsageQuery = {}, signal?: AbortSignal): Promise<LLMUsageAggregateReport> {
    const path = withQuery("/v2/llm-usage", {
      workspace_id: query.workspaceId,
      provider_id: query.providerId,
      model: query.model,
      status: query.status,
      group_by: query.groupBy,
      from: formatTime(query.from),
      to: formatTime(query.to),
    });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }
}

function providerPath(providerId: string): string {
  return resourcePath("/v2/llm-providers", providerId);
}

function requestOptions(expectedRevision: number, signal?: AbortSignal) {
  return {
    headers: { "If-Match": `"${expectedRevision}"` },
    ...(signal === undefined ? {} : { signal }),
  };
}

function formatTime(value?: Date | string): string | undefined {
  if (value === undefined) return undefined;
  return value instanceof Date ? value.toISOString() : value;
}
