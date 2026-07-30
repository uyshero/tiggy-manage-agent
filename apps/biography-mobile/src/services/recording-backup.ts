import { biographyAuthBaseURL, currentBiographyAccessToken, currentBiographyUserID } from "./auth";
import { recordingSegmentAudioBlob, recordingSegments, type RecordingSegment, type StoredRecording } from "./recordings";

interface RecordingBackupMetadata {
  projectID: string;
  chapterID: string;
  chapterTitle: string;
  transcript: string;
  durationMs: number;
  title: string;
  createdAt: number;
  sizeBytes: number;
}

function recordingURL(recordingID: string): string {
  const baseURL = biographyAuthBaseURL();
  if (!baseURL) throw new Error("请先配置自传服务地址");
  return `${baseURL}/v1/recordings/${encodeURIComponent(recordingID)}`;
}

function segmentAudioURL(recordingID: string, segmentID: string): string {
  return `${recordingURL(recordingID)}/segments/${encodeURIComponent(segmentID)}/audio`;
}

function recordingsURL(projectID: string): string {
  const baseURL = biographyAuthBaseURL();
  if (!baseURL) throw new Error("请先配置自传服务地址");
  const query = projectID ? `?project_id=${encodeURIComponent(projectID)}` : "";
  return `${baseURL}/v1/recordings${query}`;
}

function uploadMetadata(recording: StoredRecording): RecordingBackupMetadata {
  return {
    projectID: recording.projectID,
    chapterID: recording.chapterID,
    chapterTitle: recording.chapterTitle,
    transcript: recording.transcript,
    durationMs: recording.durationMs,
    title: recording.title,
    createdAt: recording.createdAt,
    sizeBytes: recording.sizeBytes,
  };
}

function segmentMetadata(segment: RecordingSegment) {
  return {
    transcript: segment.transcript,
    durationMs: segment.durationMs,
    createdAt: segment.createdAt,
    transcriptionStatus: segment.transcriptionStatus,
  };
}

function errorFromResponse(status: number, payload: unknown): Error {
  const message = payload && typeof payload === "object" && "message" in payload
    ? String(payload.message || "").trim()
    : "";
  if (status === 401) return new Error("登录已过期，请重新登录后继续备份");
  return new Error(message || "录音备份暂时没有完成");
}

export async function uploadRecordingBackup(recording: StoredRecording): Promise<void> {
  const token = currentBiographyAccessToken();
  if (!token) throw new Error("登录已过期，请重新登录后继续备份");
  const metadataResponse = await fetch(recordingURL(recording.id), {
    method: "PUT",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify(uploadMetadata(recording)),
  });
  if (!metadataResponse.ok) throw errorFromResponse(metadataResponse.status, await metadataResponse.json().catch(() => null));
  for (const segment of recordingSegments(recording)) {
    await uploadRecordingSegment(recording.id, segment, token);
  }
}

export async function deleteRecordingSegmentBackup(recordingID: string, segmentID: string): Promise<void> {
  const token = currentBiographyAccessToken();
  if (!token) throw new Error("登录已过期，请重新登录后继续备份");
  const response = await fetch(segmentAudioURL(recordingID, segmentID), {
    method: "DELETE",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (response.status === 404) return;
  if (!response.ok) throw errorFromResponse(response.status, await response.json().catch(() => null));
}

export async function listRecordingBackups(projectID: string): Promise<StoredRecording[]> {
  const token = currentBiographyAccessToken();
  if (!token) throw new Error("登录已过期，请重新登录后查看录音");
  const response = await fetch(recordingsURL(projectID), { headers: { Authorization: `Bearer ${token}` } });
  if (!response.ok) throw errorFromResponse(response.status, await response.json().catch(() => null));
  const payload = await response.json() as { recordings?: Array<Record<string, unknown>> };
  return (payload.recordings || []).map((recording) => ({
    id: String(recording.id || ""),
    ownerID: currentBiographyUserID(),
    projectID: String(recording.projectID || ""),
    chapterID: String(recording.chapterID || ""),
    chapterTitle: String(recording.chapterTitle || ""),
    transcript: String(recording.transcript || ""),
    durationMs: Number(recording.durationMs || 0),
    title: String(recording.title || "本次采访"),
    createdAt: timestamp(recording.createdAt),
    sizeBytes: Number(recording.sizeBytes || 0),
    cumulative: false,
    backupStatus: "synced" as const,
    backupError: "",
    segments: Array.isArray(recording.segments)
      ? recording.segments.map((segment) => ({
          id: String((segment as Record<string, unknown>).id || ""),
          transcript: String((segment as Record<string, unknown>).transcript || ""),
          durationMs: Number((segment as Record<string, unknown>).durationMs || 0),
          createdAt: timestamp((segment as Record<string, unknown>).createdAt),
          sizeBytes: Number((segment as Record<string, unknown>).sizeBytes || 0),
          transcriptionStatus: String((segment as Record<string, unknown>).transcriptionStatus || "ready") === "needs_retry" ? "needs_retry" : "ready",
        }))
      : [],
  }));
}

export async function downloadRecordingSegmentBackup(recordingID: string, segmentID: string): Promise<Blob> {
  const token = currentBiographyAccessToken();
  if (!token) throw new Error("登录已过期，请重新登录后播放录音");
  const response = await fetch(segmentAudioURL(recordingID, segmentID), { headers: { Authorization: `Bearer ${token}` } });
  if (!response.ok) throw errorFromResponse(response.status, await response.json().catch(() => null));
  return response.blob();
}

function timestamp(value: unknown): number {
  if (typeof value === "number") return value;
  const parsed = new Date(String(value || "")).getTime();
  return Number.isFinite(parsed) ? parsed : Date.now();
}

async function uploadRecordingSegment(recordingID: string, segment: RecordingSegment, token: string): Promise<void> {
  const url = segmentAudioURL(recordingID, segment.id);
  const metadata = JSON.stringify(segmentMetadata(segment));
  const audio = await recordingSegmentAudioBlob(segment);
  if (audio) {
    const form = new FormData();
    form.append("metadata", metadata);
    form.append("audio", audio, `${segment.id}.wav`);
    const response = await fetch(url, {
      method: "PUT",
      headers: { Authorization: `Bearer ${token}` },
      body: form,
    });
    if (!response.ok) throw errorFromResponse(response.status, await response.json().catch(() => null));
    return;
  }

  const filePath = segment.filePath;
  if (!filePath) throw new Error("录音文件内容为空");
  const response = await uni.uploadFile({
    url,
    filePath,
    name: "audio",
    fileType: "audio",
    header: { Authorization: `Bearer ${token}` },
    formData: { metadata },
  });
  if (response.statusCode < 200 || response.statusCode >= 300) {
    let payload: unknown = null;
    try {
      payload = JSON.parse(response.data);
    } catch {
      // The user-facing error below is intentionally generic for non-JSON gateways.
    }
    throw errorFromResponse(response.statusCode, payload);
  }
}
