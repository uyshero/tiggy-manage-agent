import type {
  CreateRetrievalCollectionRequest,
  RetrievalCollection,
  RetrievalDocument,
  RetrievalDocumentUploadResult,
  RetrievalIngestionJob,
  RetrievalSearchRequest,
  RetrievalSearchResponse,
  UploadFile,
} from "../types.js";
import type { Transport } from "../transport.js";
import { ServiceBase, resourcePath } from "./base.js";

export class RetrievalCollectionsService extends ServiceBase {
  create(request: CreateRetrievalCollectionRequest, signal?: AbortSignal): Promise<RetrievalCollection> {
    return this.transport.requestJSON("POST", retrievalCollectionsPath(), request, signal ? { signal } : {});
  }

  list(signal?: AbortSignal): Promise<RetrievalCollection[]> {
    return this.transport.requestJSON<{ collections: RetrievalCollection[] }>("GET", retrievalCollectionsPath(), undefined, signal ? { signal } : {})
      .then((value) => value.collections);
  }

  async delete(collectionId: string, signal?: AbortSignal): Promise<void> {
    await this.transport.request("DELETE", retrievalCollectionPath(collectionId), signal ? { signal } : {});
  }
}

export class RetrievalDocumentsService extends ServiceBase {
  list(collectionId: string, signal?: AbortSignal): Promise<RetrievalDocument[]> {
    return this.transport.requestJSON<{ documents: RetrievalDocument[] }>("GET", `${retrievalCollectionPath(collectionId)}/documents`, undefined, signal ? { signal } : {})
      .then((value) => value.documents);
  }

  get(documentId: string, signal?: AbortSignal): Promise<RetrievalDocument> {
    return this.transport.requestJSON("GET", retrievalDocumentPath(documentId), undefined, signal ? { signal } : {});
  }

  async upload(collectionId: string, fields: Record<string, string>, file: UploadFile, signal?: AbortSignal): Promise<RetrievalDocumentUploadResult> {
    const form = new FormData();
    for (const [key, value] of Object.entries(fields)) form.set(key, value);
    const body = file.contentType && file.body.type !== file.contentType
      ? new Blob([file.body], { type: file.contentType })
      : file.body;
    form.set("file", body, file.filename);
    const response = await this.transport.request("POST", `${retrievalCollectionPath(collectionId)}/documents`, {
      body: form,
      ...(signal === undefined ? {} : { signal }),
    });
    return await response.json() as RetrievalDocumentUploadResult;
  }

  async delete(documentId: string, signal?: AbortSignal): Promise<void> {
    await this.transport.request("DELETE", retrievalDocumentPath(documentId), signal ? { signal } : {});
  }
}

export class RetrievalIngestionJobsService extends ServiceBase {
  get(jobId: string, signal?: AbortSignal): Promise<RetrievalIngestionJob> {
    return this.transport.requestJSON("GET", resourcePath("/v2/retrieval/ingestion-jobs", jobId), undefined, signal ? { signal } : {});
  }
}

export class RetrievalService extends ServiceBase {
  readonly collections: RetrievalCollectionsService;
  readonly documents: RetrievalDocumentsService;
  readonly ingestionJobs: RetrievalIngestionJobsService;

  constructor(transport: Transport) {
    super(transport);
    this.collections = new RetrievalCollectionsService(transport);
    this.documents = new RetrievalDocumentsService(transport);
    this.ingestionJobs = new RetrievalIngestionJobsService(transport);
  }

  search(request: RetrievalSearchRequest, signal?: AbortSignal): Promise<RetrievalSearchResponse> {
    return this.transport.requestJSON("POST", "/v2/retrieval/search", request, signal ? { signal } : {});
  }
}

function retrievalCollectionsPath(): string {
  return "/v2/retrieval/collections";
}

function retrievalCollectionPath(collectionId: string): string {
  return resourcePath(retrievalCollectionsPath(), collectionId);
}

function retrievalDocumentPath(documentId: string): string {
  return resourcePath("/v2/retrieval/documents", documentId);
}
