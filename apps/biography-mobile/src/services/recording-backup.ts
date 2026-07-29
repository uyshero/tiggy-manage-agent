import { biographyAuthBaseURL, currentBiographyAccessToken } from "./auth";
import { recordingAudioBlob, recordingFilePaths, type StoredRecording } from "./recordings";

interface RecordingBackupMetadata {
  projectID: string;
  chapterID: string;
  chapterTitle: string;
  transcript: string;
  durationMs: number;
  title: string;
  createdAt: number;
}

function uploadURL(recordingID: string): string {
  const baseURL = biographyAuthBaseURL();
  if (!baseURL) throw new Error("请先配置自传服务地址");
  return `${baseURL}/v1/recordings/${encodeURIComponent(recordingID)}/audio`;
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
  const audio = await recordingAudioBlob(recording);
  if (audio) {
    const form = new FormData();
    form.append("metadata", JSON.stringify(uploadMetadata(recording)));
    form.append("audio", audio, `${recording.id}.wav`);
    const response = await fetch(uploadURL(recording.id), {
      method: "PUT",
      headers: { Authorization: `Bearer ${token}` },
      body: form,
    });
    if (!response.ok) throw errorFromResponse(response.status, await response.json().catch(() => null));
    return;
  }

  const filePath = recordingFilePaths(recording)[0];
  if (!filePath) throw new Error("录音文件内容为空");
  const response = await uni.uploadFile({
    url: uploadURL(recording.id),
    filePath,
    name: "audio",
    fileType: "audio",
    header: { Authorization: `Bearer ${token}` },
    formData: { metadata: JSON.stringify(uploadMetadata(recording)) },
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
