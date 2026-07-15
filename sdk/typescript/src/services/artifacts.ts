import type { Artifact, ArtifactUpload, CreateArtifactRequest, UploadFile } from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";
import { sessionPath } from "./sessions.js";

export class ArtifactsService extends ServiceBase {
  create(sessionId: string, request: CreateArtifactRequest, signal?: AbortSignal): Promise<Artifact> {
    return this.transport.requestJSON("POST", artifactsPath(sessionId), request, signal ? { signal } : {});
  }

  list(sessionId: string, signal?: AbortSignal): Promise<Artifact[]> {
    return this.transport.requestJSON<{ artifacts: Artifact[] }>("GET", artifactsPath(sessionId), undefined, signal ? { signal } : {}).then((value) => value.artifacts);
  }

  async upload(sessionId: string, fields: Record<string, string>, file: UploadFile, signal?: AbortSignal): Promise<ArtifactUpload> {
    const form = new FormData();
    for (const [key, value] of Object.entries(fields)) form.set(key, value);
    const body = file.contentType && file.body.type !== file.contentType
      ? new Blob([file.body], { type: file.contentType })
      : file.body;
    form.set("file", body, file.filename);
    const response = await this.transport.request("POST", `${artifactsPath(sessionId)}/upload`, {
      body: form,
      ...(signal === undefined ? {} : { signal }),
    });
    return await response.json() as ArtifactUpload;
  }

  download(sessionId: string, artifactId: string, signal?: AbortSignal): Promise<Response> {
    return this.transport.request("GET", `${artifactPath(sessionId, artifactId)}/download`, signal ? { signal } : {});
  }

  async delete(sessionId: string, artifactId: string, signal?: AbortSignal): Promise<void> {
    await this.transport.request("DELETE", artifactPath(sessionId, artifactId), signal ? { signal } : {});
  }
}

function artifactsPath(sessionId: string): string {
  return `${sessionPath(sessionId)}/artifacts`;
}

function artifactPath(sessionId: string, artifactId: string): string {
  return resourcePath(artifactsPath(sessionId), artifactId);
}
