import { encodeWAVPCM16 } from "./browser-audio";
import { currentBiographyUserID } from "./auth";

export interface RecordingInput {
  projectID: string;
  chapterID: string;
  chapterTitle: string;
  transcript: string;
  durationMs: number;
  segmentDurationMs?: number;
  audio?: Blob;
  filePath?: string;
  sizeBytes?: number;
  /** Native plugins emit the cumulative interview file after appending a turn. */
  cumulative?: boolean;
  /** A native session may keep one full-session file alongside per-turn files. */
  segmentFilePath?: string;
}

export interface RecordingSegment {
  id: string;
  createdAt: number;
  transcript: string;
  durationMs: number;
  audio?: Blob;
  filePath?: string;
  sizeBytes: number;
  transcriptionStatus: "ready" | "needs_retry";
}

export interface StoredRecording extends RecordingInput {
  id: string;
  ownerID: string;
  title: string;
  createdAt: number;
  sizeBytes: number;
  segments?: RecordingSegment[];
  /** The top-level file is a native full-session recording, not just one segment. */
  cumulative?: boolean;
  /** Local storage remains the source of truth while the private backup is in flight. */
  backupStatus?: "pending" | "synced" | "failed";
  backupError?: string;
}

const databaseName = "tma-biography-recordings";
const databaseVersion = 1;
const storeName = "recordings";

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("录音存储暂时不可用"));
  });
}

function transactionFinished(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error || new Error("录音保存失败"));
    transaction.onabort = () => reject(transaction.error || new Error("录音保存已取消"));
  });
}

function openDatabase(): Promise<IDBDatabase> {
  if (!globalThis.indexedDB) return Promise.reject(new Error("当前环境暂不支持本地录音管理"));
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(storeName)) {
        const store = request.result.createObjectStore(storeName, { keyPath: "id" });
        store.createIndex("projectID", "projectID", { unique: false });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("无法打开录音存储"));
  });
}

export function createRecordingTitle(chapterTitle: string, existingCount: number): string {
  return `${chapterTitle || "未分类"} · 第 ${existingCount + 1} 次采访`;
}

export function formatRecordingDuration(durationMs: number): string {
  const seconds = Math.max(0, Math.round(durationMs / 1_000));
  const minutes = Math.floor(seconds / 60).toString().padStart(2, "0");
  return `${minutes}:${(seconds % 60).toString().padStart(2, "0")}`;
}

export function formatRecordingDate(createdAt: number): string {
  const date = new Date(createdAt);
  return `${date.getMonth() + 1}月${date.getDate()}日 ${date.getHours().toString().padStart(2, "0")}:${date.getMinutes().toString().padStart(2, "0")}`;
}

export function formatInterviewSessionMoment(createdAt: number): string {
  const date = new Date(createdAt);
  const hour = date.getHours();
  const period = hour < 6 ? "凌晨" : hour < 12 ? "上午" : hour < 18 ? "下午" : "晚上";
  return `${date.getMonth() + 1}月${date.getDate()}日${period}`;
}

export function recordingSegments(recording: StoredRecording): RecordingSegment[] {
  if (recording.segments?.length) {
    return recording.segments.map((segment, index) => ({
      ...segment,
      id: segment.id || `${recording.id}-segment-${index + 1}`,
      createdAt: segment.createdAt || recording.createdAt,
      transcriptionStatus: segment.transcriptionStatus || (segment.transcript.trim() ? "ready" : "needs_retry"),
    }));
  }
  return [
    {
      id: `${recording.id}-legacy`,
      createdAt: recording.createdAt,
      transcript: recording.transcript,
      durationMs: recording.durationMs,
      audio: recording.audio,
      filePath: recording.filePath,
      sizeBytes: recording.sizeBytes,
      transcriptionStatus: "ready",
    },
  ];
}

export async function listRecordings(projectID: string): Promise<StoredRecording[]> {
  const ownerID = currentBiographyUserID();
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readonly");
    const index = transaction.objectStore(storeName).index("projectID");
    const recordings = await requestResult(index.getAll(projectID) as IDBRequest<StoredRecording[]>);
    return recordings
      .filter((recording) => (recording.ownerID || "anonymous") === ownerID)
      .sort((left, right) => right.createdAt - left.createdAt);
  } finally {
    database.close();
  }
}

export async function saveRecording(input: RecordingInput): Promise<StoredRecording> {
  if (!input.audio && !input.filePath) throw new Error("录音文件内容为空");
  const existing = await listRecordings(input.projectID);
  const chapterCount = existing.filter((recording) => recording.chapterID === input.chapterID).length;
  const recording: StoredRecording = {
    ...input,
    id: `recording-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    ownerID: currentBiographyUserID(),
    title: createRecordingTitle(input.chapterTitle, chapterCount),
    createdAt: Date.now(),
    sizeBytes: input.audio?.size || input.sizeBytes || 0,
    segments: [recordingSegment(input)],
    cumulative: Boolean(input.cumulative),
    backupStatus: "pending",
    backupError: "",
  };
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    transaction.objectStore(storeName).put(recording);
    await transactionFinished(transaction);
    return recording;
  } finally {
    database.close();
  }
}

export async function appendRecordingSegment(existing: StoredRecording | undefined, input: RecordingInput): Promise<StoredRecording> {
  if (!existing) return saveRecording(input);
  if (!input.audio && !input.filePath) throw new Error("录音文件内容为空");
  const segment = recordingSegment(input);
  const previousSegments = recordingSegments(existing);
  const transcript = [existing.transcript.trim(), input.transcript.trim()].filter(Boolean).join("\n");
  const recording: StoredRecording = input.cumulative
    ? {
        ...existing,
        transcript,
        durationMs: input.durationMs,
        audio: input.audio,
        filePath: input.filePath,
        sizeBytes: input.audio?.size || input.sizeBytes || existing.sizeBytes,
        cumulative: true,
        segments: [...previousSegments, segment],
        backupStatus: "pending",
        backupError: "",
      }
    : {
        ...existing,
        transcript,
        durationMs: existing.durationMs + input.durationMs,
        sizeBytes: existing.sizeBytes + segment.sizeBytes,
        segments: [...previousSegments, segment],
        backupStatus: "pending",
        backupError: "",
      };
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    transaction.objectStore(storeName).put(recording);
    await transactionFinished(transaction);
    return recording;
  } finally {
    database.close();
  }
}

export async function recordingAudioBlob(recording: StoredRecording): Promise<Blob | undefined> {
  const segments = recording.segments?.filter((segment) => segment.audio);
  if (!segments?.length) return recording.audio;
  if (segments.length === 1) return segments[0].audio;
  const chunks: ArrayBuffer[] = [];
  for (const segment of segments) {
    const audio = segment.audio;
    if (!audio) continue;
    const wav = await audio.arrayBuffer();
    if (wav.byteLength < 44) throw new Error("录音文件格式不完整");
    chunks.push(wav.slice(44));
  }
  return new Blob([encodeWAVPCM16(chunks)], { type: "audio/wav" });
}

export async function recordingSegmentAudioBlob(segment: RecordingSegment): Promise<Blob | undefined> {
  return segment.audio;
}

export function recordingFilePaths(recording: StoredRecording): string[] {
  if (recording.cumulative && recording.filePath) return [recording.filePath];
  const paths = recording.segments?.map((segment) => segment.filePath || "").filter(Boolean) || [];
  if (paths.length === 0 && recording.filePath) paths.push(recording.filePath);
  return [...new Set(paths)];
}

export async function deleteRecordingSegment(recordingID: string, segmentID: string): Promise<StoredRecording | undefined> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    const store = transaction.objectStore(storeName);
    const recording = await requestResult(store.get(recordingID) as IDBRequest<StoredRecording | undefined>);
    if (!recording) throw new Error("没有找到这次采访");
    const remaining = recordingSegments(recording).filter((segment) => segment.id !== segmentID);
    if (remaining.length === 0) {
      store.delete(recordingID);
      await transactionFinished(transaction);
      return undefined;
    }
    const updated: StoredRecording = {
      ...recording,
      transcript: remaining.map((segment) => segment.transcript.trim()).filter(Boolean).join("\n"),
      durationMs: remaining.reduce((total, segment) => total + segment.durationMs, 0),
      sizeBytes: recording.cumulative
        ? recording.sizeBytes
        : remaining.reduce((total, segment) => total + segment.sizeBytes, 0),
      segments: remaining,
      backupStatus: "pending",
      backupError: "",
    };
    store.put(updated);
    await transactionFinished(transaction);
    return updated;
  } finally {
    database.close();
  }
}

function recordingSegment(input: RecordingInput): RecordingSegment {
  return {
    id: `segment-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    createdAt: Date.now(),
    transcript: input.transcript.trim(),
    durationMs: input.segmentDurationMs || input.durationMs,
    audio: input.audio,
    filePath: input.segmentFilePath || input.filePath,
    sizeBytes: input.audio?.size || input.sizeBytes || 0,
    transcriptionStatus: input.transcript.trim() ? "ready" : "needs_retry",
  };
}

export async function renameRecording(id: string, title: string): Promise<void> {
  const nextTitle = title.trim();
  if (!nextTitle) throw new Error("录音名称不能为空");
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    const store = transaction.objectStore(storeName);
    const recording = await requestResult(store.get(id) as IDBRequest<StoredRecording | undefined>);
    if (!recording) throw new Error("没有找到这段录音");
    store.put({ ...recording, title: nextTitle });
    await transactionFinished(transaction);
  } finally {
    database.close();
  }
}

export async function deleteRecording(id: string): Promise<void> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    transaction.objectStore(storeName).delete(id);
    await transactionFinished(transaction);
  } finally {
    database.close();
  }
}

export async function updateRecordingBackupStatus(
  id: string,
  status: NonNullable<StoredRecording["backupStatus"]>,
  error = "",
): Promise<StoredRecording | undefined> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readwrite");
    const store = transaction.objectStore(storeName);
    const recording = await requestResult(store.get(id) as IDBRequest<StoredRecording | undefined>);
    if (!recording) {
      await transactionFinished(transaction);
      return undefined;
    }
    const updated: StoredRecording = { ...recording, backupStatus: status, backupError: error };
    store.put(updated);
    await transactionFinished(transaction);
    return updated;
  } finally {
    database.close();
  }
}
