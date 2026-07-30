<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { interviewStartLabel, interviewStatusCopy, isInterviewActive, initialInterviewState, reduceInterviewState, type InterviewEvent } from "@/domain/interview-machine";
import { shouldPauseAfterNoSpeech } from "@/domain/no-speech-policy";
import { resolveFollowupDelayMs } from "@/domain/response-timing";
import { currentBiographyAccessToken, currentBiographyUser, ensureBiographyAuthenticated, fetchBiographyProgress, logoutBiography, type BiographyUser } from "@/services/auth";
import {
  continueInterview,
  getEmptyProject,
  getInitialProject,
  openingPromptForInterviewOrder,
  type Chapter,
  type InterviewOrder,
} from "@/services/interview";
import {
  buildPreviousInterviewGuidance,
  formatInterviewDuration,
  formatInterviewMoment,
  loadLastInterviewSession,
  saveLastInterviewSession,
  type LastInterviewSession,
} from "@/services/interview-history";
import {
  appendRecordingSegment,
  deleteRecording,
  deleteRecordingSegment,
  formatInterviewSessionMoment,
  formatRecordingDuration,
  listRecordings,
  recordingAudioBlob,
  recordingFilePaths,
  recordingSegments,
  updateRecordingBackupStatus,
  type RecordingSegment,
  type StoredRecording,
} from "@/services/recordings";
import { deleteRecordingSegmentBackup, downloadRecordingSegmentBackup, listRecordingBackups, uploadRecordingBackup } from "@/services/recording-backup";
import { createVoiceAdapter, type VoiceAdapter, type VoiceEvent } from "@/services/voice";

const state = ref({ ...initialInterviewState });
const project = ref(getInitialProject());
const voice = ref<VoiceAdapter | null>(null);
const elapsedSeconds = ref(0);
const showChapters = ref(false);
const selectedChapterID = ref("");
const sessionStarted = ref(false);
const voiceOperationPending = ref(false);
const recordings = ref<StoredRecording[]>([]);
const showRecordingManager = ref(false);
const playingRecordingID = ref("");
const playingSegmentID = ref("");
const expandedRecordingID = ref("");
const pendingTranscriptCount = ref(0);
const lastInterviewSession = ref<LastInterviewSession | null>(loadLastInterviewSession());
const currentUser = ref<BiographyUser | null>(currentBiographyUser());
const talkButtonHeld = ref(false);
const talkButtonCanceling = ref(false);
const pendingTranscriptBuffer = ref("");
const followupCountdown = ref(0);
const latestNarrationReceipt = ref<{
  transcript: string;
  durationMs: number;
  recordingID: string;
  recordingSaveFailed: boolean;
  needsRetry: boolean;
} | null>(null);
const pendingChapterConfirmation = ref<{
  text: string;
  expression: string;
  chapterID: string;
} | null>(null);
const chapterConfirmationSubmitting = ref(false);
type ServiceStatusState = "ready" | "working" | "retry" | "waiting";
interface ServiceStatus {
  state: ServiceStatusState;
  label: string;
  detail: string;
}
const serviceStatus = ref<Record<"network" | "transcription" | "upload" | "organization", ServiceStatus>>({
  network: { state: "working", label: "连接采访服务", detail: "正在准备" },
  transcription: { state: "waiting", label: "语音转写", detail: "等待您讲述" },
  upload: { state: "waiting", label: "录音保存", detail: "还没有需要保存的录音" },
  organization: { state: "waiting", label: "章节整理", detail: "会在讲述后后台整理" },
});
let elapsedTimer: ReturnType<typeof setInterval> | undefined;
let unsubscribeVoice: (() => void) | undefined;
let pauseAfterTurn = false;
let pauseAfterPlayback = false;
let consecutiveNoSpeechCount = 0;
let followupTimer: ReturnType<typeof setTimeout> | undefined;
let followupCountdownTimer: ReturnType<typeof setInterval> | undefined;
let holdStartPromise: Promise<void> | undefined;
let holdEnding = false;
let talkStartY = 0;
let talkCanceledBySlide = false;
const followupDelayMs = resolveFollowupDelayMs(import.meta.env.VITE_BIOGRAPHY_FOLLOWUP_DELAY_MS);
const slideCancelDistance = 54;
let activeRecordingID = "";
let recordingSegmentExpected = false;
let recordingStorePending = false;
let finishRecordingAfterNextSegment = false;
let recordingPlayer: ReturnType<typeof uni.createInnerAudioContext> | null = null;
let recordingPlaybackQueue: string[] = [];
const recordingURLs = new Map<string, string>();
const recordingBackupQueues = new Map<string, Promise<void>>();

const interviewOrderOptions: Array<{ value: InterviewOrder; label: string; description: string }> = [
  { value: "chronological", label: "从小到大", description: "顺着人生阶段，慢慢往前讲" },
  { value: "key_moments", label: "重点故事", description: "先讲最想留给家人的经历" },
  { value: "custom", label: "自己定顺序", description: "现在最想讲哪段，就从哪段开始" },
];

const statusCopy = computed(() => interviewStatusCopy[state.value.status]);
const elapsedLabel = computed(() => {
  const minutes = Math.floor(elapsedSeconds.value / 60).toString().padStart(2, "0");
  const seconds = (elapsedSeconds.value % 60).toString().padStart(2, "0");
  return `${minutes}:${seconds}`;
});
const activeChapterCount = computed(() => project.value.chapters.filter((chapter) => chapter.status === "collecting" || chapter.status === "confirm").length);
const selectedChapter = computed(() => project.value.chapters.find((chapter) => chapter.id === selectedChapterID.value));
const progressSummary = computed(() => {
  if (pendingTranscriptCount.value > 0) {
    return `已暂存 ${pendingTranscriptCount.value} 段讲述，正在整理`;
  }
  if (project.value.completedChapterCount === 0 && activeChapterCount.value === 0) return "故事刚刚开始，章节会随着讲述逐步整理";
  if (activeChapterCount.value === 0) return `已完成 ${project.value.completedChapterCount} 章`;
  return `已完成 ${project.value.completedChapterCount} 章，${activeChapterCount.value} 章正在讲述和确认`;
});
const currentCaption = computed(() => {
  if (state.value.status === "error") return state.value.errorMessage || "我们可以再试一次";
  if (state.value.status === "listening") return state.value.partialTranscript || "您可以开始说了";
  if (pendingTranscriptBuffer.value && followupCountdown.value > 0) return `我收到这段了。您还可以继续按住话筒补充刚才这个问题；如果不补充，我会在 ${followupCountdown.value} 秒后继续提问。`;
  if (state.value.status === "thinking") return state.value.assistantText || state.value.finalTranscript || "我听到了，正在想接下来问什么";
  if (state.value.status === "paused") {
    return project.value.overallProgress > 0
      ? buildPreviousInterviewGuidance(project.value)
      : "本次采访已经保存。准备好后，我们可以从刚才的话题继续。";
  }
  if (pendingChapterConfirmation.value) return pendingChapterConfirmation.value.text;
  if (selectedChapter.value) {
    return sessionStarted.value
      ? `现在可以补充“${selectedChapter.value.title}”。按住话筒，说说想补上或更正的内容。`
      : `已选“${selectedChapter.value.title}”。开始采访后，您可以补上或更正这一段。`;
  }
  if (state.value.assistantText) return state.value.assistantText;
  if (!sessionStarted.value && !project.value.interviewOrder) {
    return "这本人生书不会预先排好章节。先选一种讲述方式；之后随时可以跳到别的回忆补充。";
  }
  if (project.value.overallProgress === 0) return openingPromptForInterviewOrder(project.value.interviewOrder);
  if (!sessionStarted.value) {
    const activeChapter = project.value.chapters.find((chapter) => chapter.status === "collecting")
      || project.value.chapters.find((chapter) => chapter.status === "confirm");
    return `上次我们讲到“${activeChapter?.title || "刚才的话题"}”。这次可以从这里接着讲，也可以补充其他内容。`;
  }
  return buildPreviousInterviewGuidance(project.value);
});
const interviewActive = computed(() => isInterviewActive(state.value.status, sessionStarted.value));
const showOrderChooser = computed(() => !sessionStarted.value && !project.value.interviewOrder);
const selectedInterviewOrderLabel = computed(() => interviewOrderOptions.find((option) => option.value === project.value.interviewOrder)?.label || "");
const showStartButton = computed(() => !showOrderChooser.value && (!sessionStarted.value || state.value.status === "paused" || state.value.status === "error" || state.value.status === "reconnecting"));
const showHoldTalkButton = computed(() => sessionStarted.value && (
  talkButtonHeld.value ||
  Boolean(pendingTranscriptBuffer.value) ||
  state.value.status === "ready" ||
  state.value.status === "speaking"
));
const primaryLabel = computed(() => interviewStartLabel(state.value.status, sessionStarted.value));
const sessionTimeTitle = computed(() => {
  if (interviewActive.value) return "本次采访";
  if (lastInterviewSession.value) return `上次 ${formatInterviewMoment(lastInterviewSession.value.endedAt)}`;
  return "尚未采访";
});
const sessionTimeValue = computed(() => {
  if (interviewActive.value) return elapsedLabel.value;
  if (lastInterviewSession.value) return `采访 ${formatInterviewDuration(lastInterviewSession.value.durationSeconds)}`;
  return "还没有记录";
});
const recordingSummary = computed(() => recordings.value.length === 0 ? "还没有采访场次" : `已保存 ${recordings.value.length} 场采访`);
const recordingEntrySummary = computed(() => recordings.value.length === 0 ? "未保存" : `${recordings.value.length} 场`);
const showUserEntry = computed(() => Boolean(currentUser.value || currentBiographyAccessToken()));
const currentUserName = computed(() => currentUser.value?.display_name || currentUser.value?.subject || "已登录");
const currentUserInitial = computed(() => currentUserName.value.trim().slice(0, 1) || "我");
const canHoldToTalk = computed(() => {
  const inHoldWindow = state.value.status === "ready" || state.value.status === "speaking" || Boolean(pendingTranscriptBuffer.value);
  if (!sessionStarted.value || !inHoldWindow) return false;
  if (state.value.status === "speaking") return true;
  return !voiceOperationPending.value;
});
const holdTalkDisabled = computed(() => !talkButtonHeld.value && !canHoldToTalk.value);
const holdButtonLabel = computed(() => {
  if (talkButtonHeld.value) return talkButtonCanceling.value ? "取消发送" : "松开发送";
  if (pendingTranscriptBuffer.value) return "补充刚才这段";
  return "按住说话";
});
const narrationReceiptRecording = computed(() => {
  const receipt = latestNarrationReceipt.value;
  if (!receipt) return "";
  if (receipt.recordingSaveFailed) return "录音暂未保存";
  const recording = recordings.value.find((item) => item.id === receipt.recordingID);
  if (!recording) return "正在保存录音";
  if (recording.backupStatus === "synced") return "录音已保存并备份";
  if (recording.backupStatus === "failed") return "录音已保存，备份稍后重试";
  return "录音已保存，正在备份";
});
const narrationReceiptOrganization = computed(() => {
  const receipt = latestNarrationReceipt.value;
  if (!receipt) return "";
  if (receipt.needsRetry) return "暂未写入自传，请重新说一遍";
  if (pendingTranscriptBuffer.value) return "等待确认后整理";
  if (pendingTranscriptCount.value > 0) return "已交给后台整理";
  if (state.value.status === "thinking") return "正在整理采访内容";
  return "已纳入本次采访";
});
const holdHint = computed(() => {
  if (talkButtonCanceling.value) return "松手取消本次讲述";
  if (talkButtonHeld.value) return "松开发送，下滑取消";
  if (pendingTranscriptBuffer.value) return followupCountdown.value > 0 ? `已发送，可补充，${followupCountdown.value} 秒后提问` : "已发送，马上继续提问";
  if (!sessionStarted.value) return "先点击开始采访";
  if (state.value.status === "ready") return "按住话筒说话";
  if (state.value.status === "thinking") return "正在整理您的这段讲述";
  if (state.value.status === "speaking") return "可按住话筒补充，按下会先暂停声音";
  return "准备好后再继续";
});

function dispatch(event: InterviewEvent) {
  state.value = reduceInterviewState(state.value, event);
}

function applyProject(nextProject: typeof project.value) {
  project.value = nextProject;
  if (selectedChapterID.value && !nextProject.chapters.some((chapter) => chapter.id === selectedChapterID.value)) {
    selectedChapterID.value = "";
  }
}

function setServiceStatus(
  service: keyof typeof serviceStatus.value,
  state: ServiceStatusState,
  detail: string,
) {
  const current = serviceStatus.value[service];
  serviceStatus.value = {
    ...serviceStatus.value,
    [service]: { ...current, state, detail },
  };
}

async function handleVoiceEvent(event: VoiceEvent) {
  if (event.type === "project_loaded") {
    applyProject(event.project);
    setServiceStatus("organization", event.project.pendingConfirmation ? "ready" : "waiting", event.project.pendingConfirmation ? "等待您确认这一段" : "已同步最新进度");
    void refreshRecordings();
    void refreshPendingTranscriptCount();
    return;
  }
  if (event.type === "chapter_confirmation") {
    applyProject(event.project);
    setServiceStatus("organization", "ready", "已整理，等待您确认");
    pendingChapterConfirmation.value = { text: event.text, expression: event.expression, chapterID: event.chapterID };
    void playPendingChapterConfirmation();
    return;
  }
  if (event.type === "partial_transcript") {
    if (event.text.trim()) consecutiveNoSpeechCount = 0;
    setServiceStatus("transcription", "working", event.text.trim() ? "正在听清这段讲述" : "正在听您说话");
    dispatch({ type: "PARTIAL_TRANSCRIPT", text: event.text });
    return;
  }
  if (event.type === "input_committed") {
    dispatch({ type: "STOP_LISTENING" });
    return;
  }
  if (event.type === "final_transcript") {
    if (event.text.trim()) consecutiveNoSpeechCount = 0;
    setServiceStatus("transcription", event.text.trim() ? "ready" : "retry", event.text.trim() ? "已得到转写，您可以核对下方文字" : "没有听清这次讲述");
    dispatch({ type: "FINAL_TRANSCRIPT", text: event.text });
    recordingSegmentExpected = voice.value?.mode !== "mock" && Boolean(event.text.trim());
    if (event.text.trim()) {
      latestNarrationReceipt.value = {
        transcript: event.text.trim(),
        durationMs: 0,
        recordingID: "",
        recordingSaveFailed: false,
        needsRetry: false,
      };
      queueTranscriptForFollowup(event.text);
      return;
    }
    if (voice.value?.mode !== "mock") return;
    try {
      const reply = await continueInterview(event.text, project.value);
      await handleAssistantReply(reply.text, reply.expression, reply.project);
    } catch (error) {
      dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "采访暂时无法继续" });
    }
    return;
  }
  if (event.type === "assistant_reply") {
    consecutiveNoSpeechCount = 0;
    chapterConfirmationSubmitting.value = false;
    setServiceStatus("organization", event.needsRetry ? "retry" : "working", event.needsRetry ? "这段暂未写入自传，可重新提交" : "正在整理这段讲述");
    if (event.needsRetry && latestNarrationReceipt.value) {
      latestNarrationReceipt.value = { ...latestNarrationReceipt.value, needsRetry: true };
    }
    await handleAssistantReply(event.text, event.expression, event.project, event.speechStarted);
    return;
  }
  if (event.type === "assistant_reply_delta") {
    dispatch({ type: "ASSISTANT_DRAFT", text: event.text });
    return;
  }
  if (event.type === "recording_ready") {
    recordingSegmentExpected = false;
    recordingStorePending = true;
    try {
      await storeVoiceRecording(event);
    } finally {
      recordingStorePending = false;
      if (finishRecordingAfterNextSegment) {
        finishRecordingAfterNextSegment = false;
        activeRecordingID = "";
      }
    }
    return;
  }
  if (event.type === "playback_finished") {
    dispatch({ type: "ASSISTANT_FINISHED" });
    if (pauseAfterPlayback) {
      pauseAfterPlayback = false;
      dispatch({ type: "PAUSE" });
      saveCurrentInterviewSession();
      return;
    }
    await playPendingChapterConfirmation();
    return;
  }
  if (event.type === "speech_detected" && (state.value.status === "speaking" || state.value.status === "thinking")) {
    await interruptAssistant();
    return;
  }
  if (event.type === "network_lost") dispatch({ type: "NETWORK_LOST" });
  if (event.type === "network_lost") setServiceStatus("network", "retry", "网络连接中断，可重新连接");
  if (event.type === "network_restored") {
    dispatch({ type: "NETWORK_RESTORED" });
    setServiceStatus("network", "ready", "连接已恢复");
  }
  if (event.type === "error") {
    chapterConfirmationSubmitting.value = false;
    if (event.code === "invalid_chapter") {
      selectedChapterID.value = "";
      uni.showToast({ title: "这章刚刚更新了，请重新选择", icon: "none" });
      return;
    }
    const userRequestedPause = pauseAfterTurn;
    pauseAfterTurn = false;
    if (event.code === "no_speech" && voice.value?.mode !== "mock") {
      setServiceStatus("transcription", "retry", "没有听清，请放慢些，用普通话再说一次");
      recordingSegmentExpected = false;
      finishRecordingAfterNextSegment = false;
      if (userRequestedPause) {
        consecutiveNoSpeechCount = 0;
        activeRecordingID = "";
        await voice.value?.finishRecordingSession();
        dispatch({ type: "PAUSE" });
        return;
      }
      consecutiveNoSpeechCount += 1;
      const shouldPause = shouldPauseAfterNoSpeech(consecutiveNoSpeechCount);
      if (shouldPause) {
        activeRecordingID = "";
        await voice.value?.finishRecordingSession();
      }
      const message = shouldPause
        ? "没关系，我们先休息一下，刚才的内容已经保存好了。"
        : event.message;
      dispatch({ type: "STOP_LISTENING" });
      dispatch({ type: "ASSISTANT_STARTED", text: message });
      pauseAfterPlayback = shouldPause;
      try {
        await voice.value?.playText(message, shouldPause
          ? "温和、体贴，语速稍慢，像熟人自然结束一次谈话"
          : "温和、耐心，语速稍慢，像没有听清时自然请对方再说一次");
      } catch (error) {
        if (pauseAfterPlayback) {
          pauseAfterPlayback = false;
          dispatch({ type: "PAUSE" });
        } else {
          dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "语音暂时无法播放" });
        }
      }
      return;
    }
    recordingSegmentExpected = false;
    finishRecordingAfterNextSegment = false;
    activeRecordingID = "";
    void voice.value?.finishRecordingSession();
    pauseAfterPlayback = false;
    if (event.code?.startsWith("asr") || event.code === "transcript_unreliable") {
      setServiceStatus("transcription", "retry", event.message || "转写没有完成，可重新讲这一段");
    } else if (event.code === "interview_busy") {
      setServiceStatus("organization", "retry", event.message || "这本书正在另一台设备上采访");
    } else {
      setServiceStatus("network", "retry", event.message || "采访服务暂时不可用");
    }
    dispatch({ type: "FAIL", message: event.message });
  }
}

async function playPendingChapterConfirmation() {
  const confirmation = pendingChapterConfirmation.value;
  if (!confirmation || state.value.status !== "ready" || talkButtonHeld.value || voiceOperationPending.value) return;
  pendingChapterConfirmation.value = null;
  await handleAssistantReply(confirmation.text, confirmation.expression, project.value);
}

async function respondToChapterConfirmation(response: "对" | "补充" | "改一下") {
  if (chapterConfirmationSubmitting.value || voiceOperationPending.value || !project.value.pendingConfirmation) return;
  chapterConfirmationSubmitting.value = true;
  setServiceStatus("organization", "working", "正在保存您的确认");
  try {
    await voice.value?.requestFollowup(response);
  } catch (error) {
    chapterConfirmationSubmitting.value = false;
    setServiceStatus("organization", "retry", error instanceof Error ? error.message : "确认暂时没有保存");
    uni.showToast({ title: "确认暂时没有保存，请再试一次", icon: "none" });
  }
}

async function refreshRecordings() {
  try {
    const localRecordings = await listRecordings(project.value.id);
    let cloudRecordings: StoredRecording[] = [];
    try {
      cloudRecordings = await listRecordingBackups(project.value.id);
    } catch {
      // Local copies remain available while the cloud record list reconnects.
    }
    const localByID = new Map(localRecordings.map((recording) => [recording.id, recording]));
    recordings.value = [
      ...localRecordings,
      ...cloudRecordings.filter((recording) => !localByID.has(recording.id)),
    ].sort((left, right) => right.createdAt - left.createdAt);
    recordings.value
      .filter((recording) => recording.backupStatus !== "synced")
      .forEach((recording) => void queueRecordingBackup(recording));
  } catch {
    recordings.value = [];
  }
}

async function refreshPendingTranscriptCount() {
  try {
    const progress = await fetchBiographyProgress();
    pendingTranscriptCount.value = progress?.pendingTranscripts?.length || 0;
  } catch {
    // Progress polling is informational; it must not interrupt the interview.
  }
}

function replaceRecording(updated: StoredRecording | undefined) {
  if (!updated) return;
  recordings.value = recordings.value.map((recording) => recording.id === updated.id ? updated : recording);
}

function queueRecordingBackup(recording: StoredRecording) {
  const previous = recordingBackupQueues.get(recording.id) || Promise.resolve();
  const next = previous
    .catch(() => undefined)
    .then(async () => {
      setServiceStatus("upload", "working", "正在安全保存录音");
      const pending = await updateRecordingBackupStatus(recording.id, "pending");
      replaceRecording(pending);
      await uploadRecordingBackup(recording);
      const synced = await updateRecordingBackupStatus(recording.id, "synced");
      replaceRecording(synced);
      setServiceStatus("upload", "ready", "录音已安全保存");
    })
    .catch(async (error) => {
      const message = error instanceof Error ? error.message : "录音备份暂时没有完成";
      const failed = await updateRecordingBackupStatus(recording.id, "failed", message);
      replaceRecording(failed);
      setServiceStatus("upload", "retry", message);
    })
    .finally(() => {
      if (recordingBackupQueues.get(recording.id) === next) recordingBackupQueues.delete(recording.id);
    });
  recordingBackupQueues.set(recording.id, next);
}

function recordingBackupLabel(recording: StoredRecording): string {
  if (recording.backupStatus === "synced") return "已安全备份";
  if (recording.backupStatus === "failed") return "备份未完成";
  return "正在备份";
}

function retryRecordingBackup(recording: StoredRecording) {
  void queueRecordingBackup(recording);
}

async function storeVoiceRecording(event: Extract<VoiceEvent, { type: "recording_ready" }>) {
  const chapter = selectedChapter.value
    || project.value.chapters.find((item) => item.status === "collecting")
    || project.value.chapters.find((item) => item.status === "confirm");
  try {
    const existing = recordings.value.find((item) => item.id === activeRecordingID);
    const recording = await appendRecordingSegment(existing, {
      projectID: project.value.id,
      chapterID: chapter?.id || "interview",
      chapterTitle: chapter?.title || "本次采访",
      transcript: event.transcript,
      durationMs: event.durationMs,
      audio: event.audio,
      filePath: event.filePath,
      sizeBytes: event.sizeBytes,
      cumulative: event.cumulative,
      segmentFilePath: event.segmentFilePath,
      segmentDurationMs: event.segmentDurationMs,
    });
    const cachedURL = recordingURLs.get(recording.id);
    if (cachedURL) URL.revokeObjectURL(cachedURL);
    recordingURLs.delete(recording.id);
    if (existing) recordings.value = recordings.value.map((item) => item.id === recording.id ? recording : item);
    else {
      activeRecordingID = recording.id;
      recordings.value = [recording, ...recordings.value];
      uni.showToast({ title: "正在记录本次采访", icon: "none" });
    }
    if (latestNarrationReceipt.value?.transcript === event.transcript.trim()) {
      latestNarrationReceipt.value = {
        ...latestNarrationReceipt.value,
        durationMs: event.durationMs,
        recordingID: recording.id,
        recordingSaveFailed: false,
      };
    }
    void queueRecordingBackup(recording);
  } catch (error) {
    if (latestNarrationReceipt.value?.transcript === event.transcript.trim()) {
      latestNarrationReceipt.value = { ...latestNarrationReceipt.value, recordingSaveFailed: true };
    }
    setServiceStatus("upload", "retry", error instanceof Error ? error.message : "录音保存失败");
    uni.showToast({ title: error instanceof Error ? error.message : "录音保存失败", icon: "none" });
  }
}

async function recordingURL(recording: StoredRecording): Promise<string> {
  const existing = recordingURLs.get(recording.id);
  if (existing) return existing;
  const audio = await recordingAudioBlob(recording);
  if (!audio) return "";
  const created = URL.createObjectURL(audio);
  recordingURLs.set(recording.id, created);
  return created;
}

function stopRecordingPlayback() {
  recordingPlayer?.stop();
  recordingPlayer?.destroy();
  recordingPlayer = null;
  recordingPlaybackQueue = [];
  playingRecordingID.value = "";
  playingSegmentID.value = "";
}

async function toggleRecordingPlayback(recording: StoredRecording) {
  if (playingRecordingID.value === recording.id) {
    stopRecordingPlayback();
    return;
  }
  stopRecordingPlayback();
  const nativeSources = recordingFilePaths(recording);
  const source = nativeSources.length > 0 ? "" : await recordingURL(recording);
  if (nativeSources.length > 0) {
    recordingPlaybackQueue = nativeSources;
  } else if (source) {
    recordingPlaybackQueue = [source];
  } else {
    recordingPlaybackQueue = (await Promise.all(
      recordingSegments(recording).map(async (segment) => segmentPlaybackSource(recording, segment).catch(() => "")),
    )).filter(Boolean);
  }
  if (recordingPlaybackQueue.length === 0) return uni.showToast({ title: "没有找到录音文件", icon: "none" });
  playingRecordingID.value = recording.id;
  playNextRecordingSource();
}

function playNextRecordingSource() {
  const source = recordingPlaybackQueue.shift();
  if (!source) return stopRecordingPlayback();
  const player = uni.createInnerAudioContext();
  player.src = source;
  recordingPlayer = player;
  player.onEnded(playNextRecordingSource);
  player.onError(() => {
    stopRecordingPlayback();
    uni.showToast({ title: "这段录音暂时无法播放", icon: "none" });
  });
  player.play();
}

function interviewSessionTitle(recording: StoredRecording): string {
  return `${formatInterviewSessionMoment(recording.createdAt)}的采访`;
}

function interviewSessionMeta(recording: StoredRecording): string {
  const segments = recordingSegments(recording);
  return `${formatRecordingDuration(recording.durationMs)} · ${segments.length} 段讲述 · ${recordingBackupLabel(recording)}`;
}

async function segmentPlaybackSource(recording: StoredRecording, segment: RecordingSegment): Promise<string> {
  if (segment.filePath) return segment.filePath;
  const key = `segment:${recording.id}:${segment.id}`;
  const existing = recordingURLs.get(key);
  if (existing) return existing;
  const audio = segment.audio || await downloadRecordingSegmentBackup(recording.id, segment.id);
  const created = URL.createObjectURL(audio);
  recordingURLs.set(key, created);
  return created;
}

async function toggleSegmentPlayback(recording: StoredRecording, segment: RecordingSegment) {
  if (playingSegmentID.value === segment.id) {
    stopRecordingPlayback();
    return;
  }
  stopRecordingPlayback();
  const source = await segmentPlaybackSource(recording, segment);
  if (!source) return uni.showToast({ title: "没有找到这段讲述的录音", icon: "none" });
  playingSegmentID.value = segment.id;
  recordingPlaybackQueue = [source];
  playNextRecordingSource();
}

function toggleRecordingDetails(recording: StoredRecording) {
  expandedRecordingID.value = expandedRecordingID.value === recording.id ? "" : recording.id;
}

function confirmDeleteRecordingSegment(recording: StoredRecording, segment: RecordingSegment) {
  uni.showModal({
    title: "删除这段讲述？",
    content: "这段录音和文字会从本次采访中删除，已经整理出的章节不会自动删除。",
    confirmText: "删除",
    confirmColor: "#b24932",
    success: async (result) => {
      if (!result.confirm) return;
      try {
        if (playingSegmentID.value === segment.id) stopRecordingPlayback();
        try {
          await deleteRecordingSegmentBackup(recording.id, segment.id);
        } catch (error) {
          setServiceStatus("upload", "retry", error instanceof Error ? error.message : "云端删除将稍后重试");
        }
        if (segment.filePath) await voice.value?.deleteRecordingFile(segment.filePath);
        const updated = await deleteRecordingSegment(recording.id, segment.id);
        if (!updated) {
          if (recording.filePath && recording.filePath !== segment.filePath) await voice.value?.deleteRecordingFile(recording.filePath);
          await deleteRecording(recording.id);
          recordings.value = recordings.value.filter((item) => item.id !== recording.id);
          expandedRecordingID.value = "";
          return;
        }
        replaceRecording(updated);
        void queueRecordingBackup(updated);
      } catch (error) {
        uni.showToast({ title: error instanceof Error ? error.message : "删除失败", icon: "none" });
      }
    },
  });
}

function retrySegmentTranscription(segment: RecordingSegment) {
  uni.showModal({
    title: "重新转写这段讲述？",
    content: "为了避免把不准确的内容写入书里，请重新按住话筒，用普通话、稍慢一些讲这段内容。",
    confirmText: "去重新讲",
    success: (result) => {
      if (!result.confirm) return;
      uni.showToast({ title: "重新讲完后会单独保存为一段", icon: "none" });
    },
  });
}

async function retryService(service: keyof typeof serviceStatus.value) {
  if (service === "network") {
    setServiceStatus("network", "working", "正在重新连接");
    try {
      await voice.value?.prepare();
      setServiceStatus("network", "ready", "连接已恢复");
    } catch (error) {
      setServiceStatus("network", "retry", error instanceof Error ? error.message : "仍然无法连接");
    }
    return;
  }
  if (service === "upload") {
    const failed = recordings.value.filter((recording) => recording.backupStatus === "failed");
    if (failed.length === 0) return;
    failed.forEach((recording) => void queueRecordingBackup(recording));
    return;
  }
  if (service === "organization" && latestNarrationReceipt.value?.transcript) {
    try {
      setServiceStatus("organization", "working", "正在重新整理这段讲述");
      await voice.value?.requestFollowup(latestNarrationReceipt.value.transcript);
    } catch (error) {
      setServiceStatus("organization", "retry", error instanceof Error ? error.message : "暂时无法重新整理");
    }
    return;
  }
  uni.showModal({
    title: "重新转写",
    content: "请按住话筒，用普通话、稍慢一些重新讲这段内容。这样不会把可能有误的文字写入书里。",
    showCancel: false,
  });
}

function confirmDeleteRecording(recording: StoredRecording) {
  uni.showModal({
    title: "删除这段录音？",
    content: "删除后将无法恢复，已经整理的文字不会受影响。",
    confirmText: "删除",
    confirmColor: "#b24932",
    success: async (result) => {
      if (!result.confirm) return;
      try {
        if (playingRecordingID.value === recording.id) stopRecordingPlayback();
        for (const filePath of recordingFilePaths(recording)) await voice.value?.deleteRecordingFile(filePath);
        await deleteRecording(recording.id);
        recordings.value = recordings.value.filter((item) => item.id !== recording.id);
        const url = recordingURLs.get(recording.id);
        if (url) URL.revokeObjectURL(url);
        recordingURLs.delete(recording.id);
      } catch (error) {
        uni.showToast({ title: error instanceof Error ? error.message : "删除失败", icon: "none" });
      }
    },
  });
}

async function exportRecording(recording: StoredRecording) {
  if (typeof document === "undefined" || recordingFilePaths(recording).length > 0) {
    uni.showToast({ title: "App 导出将在系统分享能力接入后提供", icon: "none" });
    return;
  }
  const anchor = document.createElement("a");
  anchor.href = await recordingURL(recording);
  anchor.download = `${recording.title.replace(/[\\/:*?"<>|]/g, "-")}.wav`;
  anchor.click();
}

function confirmLogout() {
  uni.showModal({
    title: "退出登录？",
    content: interviewActive.value
      ? "会先保存并结束当前采访，再回到登录页。已保存的进度和录音不会删除。"
      : "会回到登录页。已保存的进度和录音不会删除。",
    confirmText: "退出",
    confirmColor: "#b24932",
    success: (result) => {
      if (result.confirm) void performLogout();
    },
  });
}

async function performLogout() {
  clearFollowupTimer();
  if (interviewActive.value) saveCurrentInterviewSession();
  try {
    await voice.value?.finishRecordingSession();
  } catch {
    // 退出登录不应被录音收尾失败卡住。
  }
  unsubscribeVoice?.();
  await voice.value?.dispose().catch(() => undefined);
  logoutBiography();
  currentUser.value = null;
  uni.redirectTo({ url: "/pages/login/index" });
}

function closeRecordingManager() {
  stopRecordingPlayback();
  showRecordingManager.value = false;
}

async function handleAssistantReply(text: string, expression: string, nextProject: typeof project.value, speechStarted = false) {
  applyProject(nextProject);
  void refreshPendingTranscriptCount();
  if (pauseAfterTurn) {
    pauseAfterTurn = false;
    if (speechStarted) await voice.value?.cancelPlayback();
    return;
  }
  dispatch({ type: "ASSISTANT_STARTED", text });
  if (speechStarted) return;
  try {
    await voice.value?.playText(text, expression);
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "语音暂时无法播放" });
  }
}

function clearFollowupTimer() {
  if (followupTimer) clearTimeout(followupTimer);
  if (followupCountdownTimer) clearInterval(followupCountdownTimer);
  followupTimer = undefined;
  followupCountdownTimer = undefined;
  followupCountdown.value = 0;
}

function queueTranscriptForFollowup(text: string) {
  const transcript = text.trim();
  if (!transcript) return;
  pendingTranscriptBuffer.value = [pendingTranscriptBuffer.value, transcript].filter(Boolean).join("\n");
  clearFollowupTimer();
  followupCountdown.value = Math.ceil(followupDelayMs / 1000);
  followupCountdownTimer = setInterval(() => {
    followupCountdown.value = Math.max(0, followupCountdown.value - 1);
  }, 1000);
  followupTimer = setTimeout(() => void requestFollowupForPendingTranscript(), followupDelayMs);
}

async function requestFollowupForPendingTranscript() {
  const transcript = pendingTranscriptBuffer.value.trim();
  clearFollowupTimer();
  pendingTranscriptBuffer.value = "";
  if (!transcript) {
    dispatch({ type: "ASSISTANT_FINISHED" });
    return;
  }
  dispatch({ type: "ASSISTANT_DRAFT", text: "我听到了，正在想接下来问什么。" });
  if (voice.value?.mode !== "mock") {
    try {
      await voice.value?.requestFollowup(transcript);
    } catch (error) {
      dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "采访暂时无法继续" });
    }
    return;
  }
  try {
    const reply = await continueInterview(transcript, project.value);
    await handleAssistantReply(reply.text, reply.expression, reply.project);
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "采访暂时无法继续" });
  }
}

function touchY(event: TouchEvent | MouseEvent): number {
  if ("changedTouches" in event && event.changedTouches.length > 0) return event.changedTouches[0].clientY;
  if ("touches" in event && event.touches.length > 0) return event.touches[0].clientY;
  return "clientY" in event ? event.clientY : 0;
}

async function startHoldToTalk(event: TouchEvent | MouseEvent) {
  if (!canHoldToTalk.value || talkButtonHeld.value) return;
  clearFollowupTimer();
  talkStartY = touchY(event);
  talkCanceledBySlide = false;
  talkButtonCanceling.value = false;
  talkButtonHeld.value = true;
  holdStartPromise = (async () => {
    if (state.value.status === "speaking") {
      pauseAfterPlayback = false;
      await voice.value?.cancelPlayback();
    }
    await beginListening({ manualCommit: true }, { force: state.value.status === "speaking" });
  })().finally(() => {
    holdStartPromise = undefined;
  });
  await holdStartPromise;
}

function moveHoldToTalk(event: TouchEvent | MouseEvent) {
  if (!talkButtonHeld.value) return;
  talkCanceledBySlide = touchY(event) - talkStartY > slideCancelDistance;
  talkButtonCanceling.value = talkCanceledBySlide;
}

async function endHoldToTalk() {
  if ((!talkButtonHeld.value && !holdStartPromise) || holdEnding) return;
  holdEnding = true;
  const shouldCancel = talkCanceledBySlide;
  talkButtonHeld.value = false;
  talkButtonCanceling.value = false;
  talkCanceledBySlide = false;
  const starting = holdStartPromise;
  if (starting) await starting.catch(() => undefined);
  if (state.value.status !== "listening") {
    holdEnding = false;
    return;
  }
  if (shouldCancel) {
    await cancelHeldRecording();
  } else {
    await sendHeldRecording();
  }
  holdEnding = false;
}

async function cancelHoldToTalk() {
  if (!talkButtonHeld.value && !holdStartPromise) return;
  talkCanceledBySlide = true;
  talkButtonCanceling.value = true;
  await endHoldToTalk();
}

async function cancelHeldRecording() {
  talkButtonHeld.value = false;
  talkButtonCanceling.value = false;
  talkCanceledBySlide = false;
  if (voiceOperationPending.value) return;
  voiceOperationPending.value = true;
  try {
    await voice.value?.cancelListening();
    recordingSegmentExpected = false;
    dispatch({ type: "CANCEL_LISTENING" });
    uni.showToast({ title: "已取消本次讲述", icon: "none" });
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "暂时无法取消录音" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function sendHeldRecording() {
  if (voiceOperationPending.value) return;
  voiceOperationPending.value = true;
  dispatch({ type: "STOP_LISTENING" });
  try {
    await voice.value?.stopListening({ deferInterview: true });
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "暂时无法发送这段讲述" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function beginListening(options?: { manualCommit?: boolean }, controls?: { force?: boolean }) {
  if (voiceOperationPending.value && !controls?.force) return;
  voiceOperationPending.value = true;
  try {
    setServiceStatus("transcription", "working", "正在打开麦克风");
    await voice.value?.startListening(options);
    sessionStarted.value = true;
    dispatch({ type: "START_LISTENING" });
  } catch (error) {
    setServiceStatus("transcription", "retry", error instanceof Error ? error.message : "麦克风暂时无法使用");
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "麦克风暂时无法使用" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function beginOpeningPrompt() {
  if (voiceOperationPending.value) return;
  voiceOperationPending.value = true;
  sessionStarted.value = true;
  const prompt = currentCaption.value;
  dispatch({ type: "ASSISTANT_STARTED", text: prompt });
  try {
    await voice.value?.playText(prompt, "温和、亲切，语速稍慢，像熟人开始聊天，停顿自然");
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "语音暂时无法播放" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function selectInterviewOrder(order: InterviewOrder) {
  if (voiceOperationPending.value) return;
  if (!voice.value) {
    dispatch({ type: "FAIL", message: "语音服务正在准备，请稍后再试" });
    return;
  }
  const previousOrder = project.value.interviewOrder;
  project.value = { ...project.value, interviewOrder: order };
  voiceOperationPending.value = true;
  try {
    await voice.value.setInterviewOrder(order);
  } catch (error) {
    project.value = { ...project.value, interviewOrder: previousOrder };
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "采访方式暂时无法保存" });
    uni.showToast({ title: "暂时没保存成功，请再点一次", icon: "none" });
    return;
  } finally {
    voiceOperationPending.value = false;
  }
  await beginOpeningPrompt();
}

async function selectChapterFocus(chapter: Chapter) {
  if (voiceOperationPending.value) return;
  if (state.value.status === "listening" || state.value.status === "thinking" || state.value.status === "speaking") {
    uni.showToast({ title: "等这一段结束后，再切换章节", icon: "none" });
    return;
  }
  if (!voice.value) {
    uni.showToast({ title: "语音服务正在准备，请稍后再试", icon: "none" });
    return;
  }
  const previousChapterID = selectedChapterID.value;
  const nextChapterID = previousChapterID === chapter.id ? "" : chapter.id;
  selectedChapterID.value = nextChapterID;
  voiceOperationPending.value = true;
  try {
    await voice.value.setChapterFocus(nextChapterID);
    uni.showToast({ title: nextChapterID ? `将补充“${chapter.title}”` : "已回到默认话题", icon: "none" });
  } catch (error) {
    selectedChapterID.value = previousChapterID;
    uni.showToast({ title: error instanceof Error ? error.message : "章节暂时无法切换", icon: "none" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function interruptAssistant() {
  if (voiceOperationPending.value) return;
  voiceOperationPending.value = true;
  pauseAfterPlayback = false;
  consecutiveNoSpeechCount = 0;
  try {
    await voice.value?.cancelPlayback();
    await voice.value?.startListening();
    dispatch({ type: "USER_INTERRUPTED" });
  } catch (error) {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "暂时无法切换到倾听" });
  } finally {
    voiceOperationPending.value = false;
  }
}

async function primaryAction() {
  if (state.value.status === "ready") {
    if (sessionStarted.value) return;
    if (!sessionStarted.value && voice.value?.mode !== "mock") return beginOpeningPrompt();
    return beginListening();
  }
  if (state.value.status === "paused") {
    consecutiveNoSpeechCount = 0;
    pauseAfterPlayback = false;
    dispatch({ type: "RESUME" });
    return;
  }
  if (state.value.status === "error" || state.value.status === "reconnecting") {
    dispatch({ type: "RETRY" });
    if (!sessionStarted.value && voice.value?.mode !== "mock") return beginOpeningPrompt();
    return;
  }
}

function confirmEndInterview() {
  uni.showModal({
    title: "结束今天的采访？",
    content: "今天的讲述会保存下来。下次可以从刚才的进度继续。",
    confirmText: "结束今天",
    cancelText: "继续采访",
    confirmColor: "#a4493d",
    success: (result) => {
      if (result.confirm) void stopInterview();
    },
  });
}

async function stopInterview() {
  if (voiceOperationPending.value) return;
  clearFollowupTimer();
  pendingTranscriptBuffer.value = "";
  selectedChapterID.value = "";
  try {
    await voice.value?.setChapterFocus("");
  } catch {
    // 已清除本地选择；连接恢复后会以默认话题继续。
  }
  consecutiveNoSpeechCount = 0;
  pauseAfterPlayback = false;
  const shouldCommit = state.value.status === "listening";
  const waitsForRecording = shouldCommit || recordingSegmentExpected || recordingStorePending;
  finishRecordingAfterNextSegment = waitsForRecording;
  pauseAfterTurn = shouldCommit || state.value.status === "thinking";
  dispatch({ type: "PAUSE" });
  saveCurrentInterviewSession();
  if (shouldCommit) {
    voiceOperationPending.value = true;
    try {
      await voice.value?.stopListening();
      await voice.value?.finishRecordingSession();
    } catch (error) {
      pauseAfterTurn = false;
      dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "暂时无法保存这段讲述" });
    } finally {
      voiceOperationPending.value = false;
    }
    return;
  }
  await voice.value?.stopListening();
  await voice.value?.cancelPlayback();
  await voice.value?.finishRecordingSession();
  if (!waitsForRecording) {
    finishRecordingAfterNextSegment = false;
    activeRecordingID = "";
  }
}

function saveCurrentInterviewSession() {
  if (!sessionStarted.value) return;
  const session: LastInterviewSession = {
    projectID: project.value.id,
    endedAt: Date.now(),
    durationSeconds: elapsedSeconds.value,
  };
  lastInterviewSession.value = session;
  saveLastInterviewSession(session);
}

function chapterClass(chapter: Chapter) {
  return `chapter-status ${chapter.status}`;
}

onMounted(() => {
  void initializeInterviewPage();
});

async function initializeInterviewPage() {
  const authenticated = await ensureBiographyAuthenticated();
  if (!authenticated) {
    uni.redirectTo({ url: "/pages/login/index" });
    return;
  }
  currentUser.value = currentBiographyUser();
  const progress = await fetchBiographyProgress();
  if (progress?.project) {
    project.value = progress.project as typeof project.value;
    pendingTranscriptCount.value = progress.pendingTranscripts?.length || 0;
    if (progress.lastInterview) {
      const endedAt = progress.lastInterview.endedAt
        ? new Date(progress.lastInterview.endedAt).getTime()
        : new Date(progress.updatedAt).getTime();
      const session = {
        projectID: project.value.id,
        endedAt: Number.isFinite(endedAt) ? endedAt : Date.now(),
        durationSeconds: progress.lastInterview.durationSeconds,
      };
      lastInterviewSession.value = session;
      saveLastInterviewSession(session);
    }
  }
  voice.value = createVoiceAdapter();
  if (voice.value.mode !== "mock" && !progress?.project) project.value = getEmptyProject();
  unsubscribeVoice = voice.value.subscribe((event) => void handleVoiceEvent(event));
  void voice.value.prepare()
    .then(() => setServiceStatus("network", "ready", "采访服务已连接"))
    .catch((error) => {
      const message = error instanceof Error ? error.message : "语音服务暂时无法连接";
      setServiceStatus("network", "retry", message);
      dispatch({ type: "FAIL", message });
    });
  void refreshRecordings();
  elapsedTimer = setInterval(() => {
    if (sessionStarted.value && state.value.status !== "paused") elapsedSeconds.value += 1;
  }, 1000);
}

onBeforeUnmount(() => {
  if (interviewActive.value) saveCurrentInterviewSession();
  if (elapsedTimer) clearInterval(elapsedTimer);
  unsubscribeVoice?.();
  void voice.value?.dispose();
  stopRecordingPlayback();
  recordingURLs.forEach((url) => URL.revokeObjectURL(url));
  recordingURLs.clear();
});
</script>

<template>
  <view class="page-shell">
    <view class="safe-top" />
    <view class="topbar">
      <view class="brand-block">
        <view class="brand-mark"><view /><view /><view /></view>
        <view>
          <text class="brand">人生书</text>
          <text class="book-title">{{ project.title }}</text>
        </view>
      </view>
      <view class="topbar-actions">
        <button class="recordings-entry" aria-label="打开录音记录" @click="showRecordingManager = true">
          <view class="recording-entry-icon" aria-hidden="true">
            <view class="recording-entry-core" />
            <view class="recording-entry-wave" />
          </view>
          <view class="recording-entry-copy">
            <text class="recording-entry-title">录音</text>
            <text class="recording-entry-count">{{ recordingEntrySummary }}</text>
          </view>
        </button>
        <view class="session-time" :aria-label="`${sessionTimeTitle}，${sessionTimeValue}`" role="img">
          <view class="session-time-icon" aria-hidden="true" />
        </view>
        <button v-if="showUserEntry" class="user-entry" aria-label="账号与退出登录" @click="confirmLogout">
          <text class="user-avatar" aria-hidden="true">{{ currentUserInitial }}</text>
          <text class="user-name">{{ currentUserName }}</text>
        </button>
      </view>
    </view>

    <view class="interview-main">
      <view class="conversation-stage">
        <view :class="['voice-presence', state.status, { recording: talkButtonHeld }]" aria-hidden="true">
          <view class="voice-core">
            <view v-for="bar in 7" :key="bar" class="voice-bar" :style="{ height: `${18 + ((bar * 11) % 38)}px` }" />
          </view>
        </view>

        <view class="status-copy">
          <text class="status-label">{{ statusCopy.label }}</text>
          <text class="status-detail">{{ statusCopy.detail }}</text>
        </view>

        <scroll-view scroll-y :class="['conversation-scroll', { compact: !sessionStarted && currentCaption.length < 120 }]" :show-scrollbar="false">
          <text class="conversation-text">{{ currentCaption }}</text>
        </scroll-view>

        <view v-if="showOrderChooser" class="interview-order-chooser">
          <text class="order-chooser-title">想先怎么讲？</text>
          <text class="order-chooser-detail">这只决定默认采访方向，之后随时都能补充别的经历。</text>
          <view class="interview-order-options">
            <view
              v-for="option in interviewOrderOptions"
              :key="option.value"
              :class="['interview-order-option', { selected: project.interviewOrder === option.value, disabled: voiceOperationPending }]"
              role="button"
              :aria-disabled="voiceOperationPending"
              @tap="selectInterviewOrder(option.value)"
            >
              <text class="interview-order-label">{{ option.label }}</text>
              <text class="interview-order-description">{{ option.description }}</text>
            </view>
          </view>
        </view>
        <text v-else-if="selectedInterviewOrderLabel" class="interview-order-summary">采访方式：{{ selectedInterviewOrderLabel }}</text>

        <view class="primary-zone">
          <button v-if="showStartButton" class="primary-voice-button" :disabled="voiceOperationPending" @click="primaryAction">
            <image class="mic-icon" src="/static/mic.svg" mode="aspectFit" aria-hidden="true" />
            <text>{{ primaryLabel }}</text>
          </button>
          <view
            v-if="showHoldTalkButton"
            :class="['hold-talk-button', { active: talkButtonHeld, canceling: talkButtonCanceling, disabled: holdTalkDisabled }]"
            role="button"
            :aria-label="holdButtonLabel"
            @touchstart.prevent="startHoldToTalk"
            @touchmove.prevent="moveHoldToTalk"
            @touchend.prevent="endHoldToTalk"
            @touchcancel.prevent="cancelHoldToTalk"
            @mousedown.prevent="startHoldToTalk"
            @mousemove.prevent="moveHoldToTalk"
            @mouseup.prevent="endHoldToTalk"
            @mouseleave="endHoldToTalk"
          >
            <image
              class="hold-mic-icon"
              :src="talkButtonHeld ? '/static/mic.svg' : '/static/mic-green.svg'"
              mode="aspectFit"
              aria-hidden="true"
            />
            <text>{{ holdButtonLabel }}</text>
          </view>
          <text v-if="showHoldTalkButton" class="hold-talk-hint">{{ holdHint }}</text>
          <button v-if="sessionStarted && state.status !== 'paused'" class="end-session-button" :disabled="voiceOperationPending" @click="confirmEndInterview">
            <text class="end-session-icon" aria-hidden="true">✓</text>
            <text>结束今天</text>
          </button>
        </view>

        <view v-if="latestNarrationReceipt" class="narration-receipt" aria-live="polite">
          <view class="narration-receipt-heading">
            <view class="narration-receipt-status">
              <view :class="['narration-receipt-check', { retry: latestNarrationReceipt.needsRetry }]" aria-hidden="true">
                {{ latestNarrationReceipt.needsRetry ? "!" : "✓" }}
              </view>
              <text>{{ latestNarrationReceipt.needsRetry ? "这段需要再确认" : "这段讲述已收到" }}</text>
            </view>
            <text v-if="latestNarrationReceipt.durationMs > 0" class="narration-receipt-duration">
              {{ formatRecordingDuration(latestNarrationReceipt.durationMs) }}
            </text>
          </view>
          <text class="narration-receipt-transcript">{{ latestNarrationReceipt.transcript }}</text>
          <text class="narration-receipt-meta">
            {{ latestNarrationReceipt.needsRetry ? "转写可能不准确" : "已收到转写" }} · {{ narrationReceiptRecording }} · {{ narrationReceiptOrganization }}
          </text>
        </view>
      </view>

      <view class="progress-band">
        <view class="progress-heading">
          <view>
            <text class="section-kicker">这本书已经完成</text>
            <text class="progress-value">{{ project.overallProgress }}%</text>
          </view>
          <button v-if="project.chapters.length > 0" class="progress-toggle" @click="showChapters = !showChapters">{{ showChapters ? "收起章节" : "查看章节" }}</button>
          <text v-else class="progress-empty-label">章节会随讲述整理</text>
        </view>
        <view class="progress-track"><view :style="{ width: `${project.overallProgress}%` }" /></view>
        <text class="progress-summary">{{ progressSummary }}</text>
      </view>

      <view v-if="project.pendingConfirmation" class="chapter-confirmation" aria-live="polite">
        <text class="chapter-confirmation-kicker">等待您确认</text>
        <text class="chapter-confirmation-text">{{ project.pendingConfirmation }}</text>
        <view class="chapter-confirmation-actions">
          <button :disabled="chapterConfirmationSubmitting || voiceOperationPending" @click="respondToChapterConfirmation('对')">对</button>
          <button :disabled="chapterConfirmationSubmitting || voiceOperationPending" @click="respondToChapterConfirmation('补充')">补充</button>
          <button :disabled="chapterConfirmationSubmitting || voiceOperationPending" @click="respondToChapterConfirmation('改一下')">改一下</button>
        </view>
      </view>

      <view class="service-status-list" aria-live="polite">
        <view v-for="(service, key) in serviceStatus" :key="key" :class="['service-status-row', service.state]">
          <view :class="['service-status-mark', service.state]" aria-hidden="true" />
          <view class="service-status-copy">
            <text>{{ service.label }}</text>
            <text>{{ service.detail }}</text>
          </view>
          <button v-if="service.state === 'retry'" class="service-status-retry" @click="retryService(key)">重试</button>
        </view>
      </view>

      <view v-if="showChapters && project.chapters.length > 0" class="chapter-list">
        <view
          v-for="chapter in project.chapters"
          :key="chapter.id"
          :class="['chapter-row', { focused: selectedChapterID === chapter.id }]"
          role="button"
          :aria-label="selectedChapterID === chapter.id ? `取消补充${chapter.title}` : `补充${chapter.title}`"
          @click="selectChapterFocus(chapter)"
        >
          <view class="chapter-index">{{ project.chapters.indexOf(chapter) + 1 }}</view>
          <view class="chapter-copy">
            <view class="chapter-title-line">
              <text class="chapter-name">{{ chapter.title }}</text>
              <text v-if="selectedChapterID === chapter.id" class="chapter-focus-label">正在补充</text>
              <text v-else :class="chapterClass(chapter)">{{ chapter.statusLabel }}</text>
            </view>
            <text>{{ chapter.detail }}</text>
            <text class="chapter-action-hint">{{ selectedChapterID === chapter.id ? "再点一次回到默认话题" : "点按补充或更正这一章" }}</text>
            <view class="chapter-track"><view :style="{ width: `${chapter.progress}%` }" /></view>
          </view>
        </view>
      </view>
    </view>
    <view class="safe-bottom" />

    <view v-if="showRecordingManager" class="recording-overlay" @click.self="closeRecordingManager">
      <view class="recording-sheet">
        <view class="recording-sheet-header">
          <view>
            <text class="recording-sheet-title">采访记录</text>
            <text class="recording-sheet-summary">{{ recordingSummary }}，每一场内按讲述段保存</text>
          </view>
          <button class="sheet-close" aria-label="关闭" @click="closeRecordingManager">×</button>
        </view>

        <scroll-view scroll-y class="recording-scroll">
          <view v-if="recordings.length === 0" class="recording-empty">
            <text class="empty-record-mark" aria-hidden="true" />
            <text>一次采访会保存为一场，里面可以逐段回听和管理</text>
          </view>
          <view v-for="recording in recordings" :key="recording.id" class="recording-session">
            <view class="recording-row">
            <button class="recording-play" :aria-label="playingRecordingID === recording.id ? '暂停录音' : '播放录音'" @click="toggleRecordingPlayback(recording)">
              {{ playingRecordingID === recording.id ? "Ⅱ" : "▶" }}
            </button>
            <view class="recording-copy">
              <text class="recording-title">{{ interviewSessionTitle(recording) }}</text>
              <text class="recording-meta">{{ interviewSessionMeta(recording) }}</text>
              <text class="recording-transcript">{{ recording.transcript }}</text>
              <view class="recording-actions">
                <button v-if="recording.backupStatus === 'failed'" class="retry-recording" @click="retryRecordingBackup(recording)">重新备份</button>
                <button @click="toggleRecordingDetails(recording)">{{ expandedRecordingID === recording.id ? "收起分段" : "查看分段" }}</button>
                <button @click="exportRecording(recording)">导出</button>
                <button class="delete-recording" @click="confirmDeleteRecording(recording)">删除</button>
              </view>
            </view>
          </view>
          <view v-if="expandedRecordingID === recording.id" class="recording-segments">
            <view v-for="(segment, index) in recordingSegments(recording)" :key="segment.id" class="recording-segment">
              <button class="segment-play" :aria-label="playingSegmentID === segment.id ? `暂停第${index + 1}段讲述` : `播放第${index + 1}段讲述`" @click="toggleSegmentPlayback(recording, segment)">
                {{ playingSegmentID === segment.id ? "Ⅱ" : "▶" }}
              </button>
              <view class="segment-copy">
                <text class="segment-title">第 {{ index + 1 }} 段讲述 · {{ formatRecordingDuration(segment.durationMs) }}</text>
                <text class="segment-transcript">{{ segment.transcript || "这段文字需要重新转写" }}</text>
                <view class="segment-actions">
                  <button v-if="segment.transcriptionStatus === 'needs_retry'" @click="retrySegmentTranscription(segment)">补转写</button>
                  <button class="delete-recording" @click="confirmDeleteRecordingSegment(recording, segment)">删除这段</button>
                </view>
              </view>
            </view>
          </view>
          </view>
        </scroll-view>
      </view>
    </view>
  </view>
</template>

<style scoped>
.page-shell {
  min-height: 100vh;
  min-height: 100dvh;
  background: #f6f8f5;
}

.safe-top { height: env(safe-area-inset-top); }
.safe-bottom { height: max(18px, env(safe-area-inset-bottom)); }

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 72px;
  padding: 10px 20px;
  border-bottom: 1px solid #dfe5df;
  background: rgba(255, 255, 255, 0.92);
}

.brand-block,
.progress-heading,
.chapter-title-line {
  display: flex;
  align-items: center;
}

.brand-block { min-width: 0; gap: 11px; }
.brand-block > view:last-child { display: grid; gap: 2px; }
.brand { font-size: 21px; font-weight: 800; line-height: 1.1; }
.book-title { overflow: hidden; color: #68756f; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }

.brand-mark {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  width: 40px;
  height: 40px;
  border-radius: 8px;
  background: #1f7257;
}

.brand-mark view { width: 4px; border-radius: 2px; background: white; }
.brand-mark view:nth-child(1) { height: 13px; }
.brand-mark view:nth-child(2) { height: 24px; }
.brand-mark view:nth-child(3) { height: 17px; }

.topbar-actions { display: flex; flex: 0 0 auto; align-items: center; gap: 12px; }
.user-entry {
  display: flex;
  align-items: center;
  gap: 7px;
  max-width: 132px;
  min-height: 48px;
  margin: 0;
  padding: 6px 12px 6px 7px;
  border: 1px solid #e2e8e4;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.72);
  color: #31463d;
}
.user-entry::after { border: 0; }
.user-entry:active { transform: scale(0.98); }
.user-avatar {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: #e2f1ea;
  color: #1f7257;
  font-size: 14px;
  font-weight: 900;
}
.user-name {
  overflow: hidden;
  min-width: 0;
  color: #31463d;
  font-size: 13px;
  font-weight: 850;
  line-height: 1.1;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.session-time {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 48px;
  min-height: 48px;
  padding: 6px;
  border: 1px solid #e2e8e4;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.72);
}
.session-time-icon {
  position: relative;
  flex: 0 0 auto;
  width: 30px;
  height: 30px;
  border: 2px solid #8fa299;
  border-radius: 50%;
  background: #f6f8f5;
}
.session-time-icon::before,
.session-time-icon::after {
  content: "";
  position: absolute;
  left: 13px;
  top: 6px;
  width: 2px;
  border-radius: 2px;
  background: #5f7168;
  transform-origin: bottom center;
}
.session-time-icon::before { height: 8px; }
.session-time-icon::after { height: 6px; transform: rotate(52deg); }

.recordings-entry {
  display: flex;
  align-items: center;
  gap: 9px;
  min-height: 48px;
  margin: 0;
  padding: 6px 13px 6px 7px;
  border: 1px solid #cfe1d8;
  border-radius: 999px;
  background: linear-gradient(180deg, #ffffff 0%, #f4faf7 100%);
  color: #1f7257;
  box-shadow: 0 8px 18px rgba(31, 114, 87, 0.1);
}
.recordings-entry::after { border: 0; }
.recordings-entry:active { transform: scale(0.98); box-shadow: 0 4px 12px rgba(31, 114, 87, 0.12); }

.recording-entry-icon {
  position: relative;
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  background: #e2f1ea;
  box-shadow: inset 0 0 0 1px #c6ddd3;
}
.recording-entry-core {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #d26949;
  box-shadow: 0 0 0 4px #f0d8d0;
}
.recording-entry-wave {
  position: absolute;
  right: 7px;
  bottom: 7px;
  width: 9px;
  height: 9px;
  border-right: 2px solid #1f7257;
  border-bottom: 2px solid #1f7257;
  border-radius: 0 0 9px 0;
}
.recording-entry-copy { display: grid; gap: 1px; text-align: left; }
.recording-entry-title { color: #1f7257; font-size: 14px; font-weight: 850; line-height: 1.15; }
.recording-entry-count { color: #6f7f77; font-size: 10px; font-weight: 700; line-height: 1.2; }

.interview-main {
  width: min(100%, 760px);
  margin: 0 auto;
  padding: 24px 18px 0;
}

.conversation-stage {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-height: 400px;
  padding: 16px 0 22px;
  text-align: center;
}

.voice-presence {
  display: grid;
  place-items: center;
  width: 152px;
  height: 152px;
  margin-bottom: 18px;
  border: 1px solid #cad9d1;
  border-radius: 50%;
  background: #e4efe9;
  box-shadow: 0 14px 34px rgba(31, 78, 62, 0.12);
}

.voice-presence.listening,
.voice-presence.recording { background: #dcefe5; border-color: #62a889; }
.voice-presence.thinking { background: #f4ead2; border-color: #d7ad54; }
.voice-presence.speaking { background: #e8e4f4; border-color: #8175aa; }
.voice-presence.paused { background: #ecefed; border-color: #aeb8b2; box-shadow: none; }
.voice-presence.error { background: #f7e4df; border-color: #cb7a64; }
.voice-presence.recording { background: #dcefe5; border-color: #1f7257; box-shadow: 0 16px 38px rgba(31, 114, 87, 0.2); }

.voice-core { display: flex; align-items: center; justify-content: center; gap: 5px; width: 96px; height: 70px; }
.voice-bar { width: 6px; max-height: 54px; border-radius: 4px; background: #25795d; transform-origin: center; }
.speaking .voice-bar { background: #65578d; animation: speak 820ms ease-in-out infinite alternate; }
.listening .voice-bar,
.recording .voice-bar { animation: listen 980ms ease-in-out infinite alternate; }
.recording .voice-bar { background: #25795d; }
.thinking .voice-bar { background: #ad7924; animation: think 1.2s ease-in-out infinite; }
.voice-bar:nth-child(2n) { animation-delay: 130ms; }
.voice-bar:nth-child(3n) { animation-delay: 260ms; }

.status-copy { display: grid; gap: 3px; margin-bottom: 20px; }
.status-label { color: #206e54; font-size: 19px; font-weight: 800; }
.status-detail { color: #75817b; font-size: 13px; }

.conversation-scroll {
  width: min(100%, 620px);
  height: 152px;
  margin-bottom: 2px;
}
.conversation-scroll.compact { height: 106px; }

.conversation-text {
  display: block;
  width: 100%;
  color: #263630;
  font-size: 21px;
  font-weight: 650;
  line-height: 1.56;
  text-align: center;
  overflow-wrap: anywhere;
}

.interview-order-chooser {
  display: grid;
  width: min(100%, 620px);
  gap: 6px;
  margin-top: 12px;
  text-align: left;
}
.order-chooser-title { color: #30443a; font-size: 18px; font-weight: 850; }
.order-chooser-detail { color: #748078; font-size: 13px; line-height: 1.45; }
.interview-order-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin-top: 8px; }
.interview-order-option {
  display: grid;
  align-content: center;
  gap: 5px;
  min-height: 84px;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid #c8dad0;
  border-radius: 8px;
  background: #fff;
  color: #29463a;
  text-align: left;
  cursor: pointer;
  transition: border-color 120ms ease, background 120ms ease, box-shadow 120ms ease, transform 120ms ease;
}
.interview-order-option:active,
.interview-order-option.selected {
  border-color: #1f7257;
  background: #edf7f1;
  box-shadow: 0 8px 20px rgba(31, 114, 87, 0.12);
  transform: translateY(-1px);
}
.interview-order-option.disabled { opacity: 0.55; pointer-events: none; }
.interview-order-label { font-size: 16px; font-weight: 850; line-height: 1.2; }
.interview-order-description { color: #718078; font-size: 12px; line-height: 1.4; }
.interview-order-summary { min-height: 20px; margin-top: 12px; color: #60786c; font-size: 13px; font-weight: 750; }

.primary-zone { display: grid; justify-items: center; gap: 12px; margin-top: 18px; }

.primary-voice-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-width: 226px;
  min-height: 64px;
  padding: 0 28px;
  border: 0;
  border-radius: 8px;
  background: #1f7257;
  color: #fff;
  font-size: 19px;
  font-weight: 750;
  box-shadow: 0 8px 20px rgba(31, 114, 87, 0.2);
}

.primary-voice-button::after,
.progress-toggle::after,
.end-session-button::after { border: 0; }
.primary-voice-button[disabled] { opacity: 0.58; box-shadow: none; }

.primary-voice-button text { display: block; line-height: 1; }
.mic-icon {
  flex: 0 0 24px;
  width: 24px;
  height: 24px;
  transform: translateY(-1px);
}

.hold-talk-button {
  display: grid;
  place-items: center;
  gap: 8px;
  width: 156px;
  height: 156px;
  margin-top: 6px;
  border: 1px solid #b9d1c6;
  border-radius: 50%;
  background: #ffffff;
  color: #1f7257;
  box-shadow: 0 12px 28px rgba(31, 114, 87, 0.16);
  user-select: none;
  touch-action: none;
}

.hold-talk-button.active {
  background: #1f7257;
  color: #fff;
  transform: scale(0.98);
  animation: holdPulse 1s ease-in-out infinite;
}
.hold-talk-button.canceling { background: #a4493d; border-color: #a4493d; color: #fff; }
.hold-talk-button.disabled { opacity: 0.44; box-shadow: none; pointer-events: none; }
.hold-talk-button text { font-size: 18px; font-weight: 800; line-height: 1; }

.hold-mic-icon {
  width: 54px;
  height: 54px;
}

.hold-talk-hint {
  min-height: 20px;
  color: #6c7a73;
  font-size: 14px;
  font-weight: 700;
  line-height: 1.35;
  text-align: center;
}

.end-session-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-width: 156px;
  min-height: 52px;
  margin: 6px 0 0;
  padding: 0 22px;
  border: 1px solid #e1b7aa;
  border-radius: 999px;
  background: linear-gradient(180deg, #fffaf8 0%, #fff1ec 100%);
  color: #a4493d;
  font-size: 16px;
  font-weight: 850;
  box-shadow: 0 8px 18px rgba(164, 73, 61, 0.1);
}
.end-session-button:active { transform: scale(0.98); box-shadow: 0 4px 12px rgba(164, 73, 61, 0.12); }
.end-session-icon {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #a4493d;
  color: #fff;
  font-size: 14px;
  font-weight: 900;
  line-height: 1;
}

.end-session-button[disabled] { opacity: 0.5; }

.narration-receipt {
  display: grid;
  width: min(100%, 620px);
  gap: 8px;
  margin-top: 18px;
  padding: 15px 16px;
  border: 1px solid #cfe1d8;
  border-left: 4px solid #2f8a68;
  border-radius: 8px;
  background: #f4faf7;
  text-align: left;
}
.narration-receipt-heading,
.narration-receipt-status {
  display: flex;
  align-items: center;
}
.narration-receipt-heading { justify-content: space-between; gap: 12px; }
.narration-receipt-status { min-width: 0; gap: 8px; color: #23664f; font-size: 14px; font-weight: 850; }
.narration-receipt-check {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 21px;
  height: 21px;
  border-radius: 50%;
  background: #2f8a68;
  color: #fff;
  font-size: 13px;
  font-weight: 900;
  line-height: 1;
}
.narration-receipt-check.retry { background: #b66b24; }
.narration-receipt-duration { flex: 0 0 auto; color: #61746b; font-size: 13px; font-weight: 750; }
.narration-receipt-transcript {
  display: -webkit-box;
  overflow: hidden;
  color: #30443a;
  font-size: 16px;
  font-weight: 700;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}
.narration-receipt-meta { color: #60766c; font-size: 12px; font-weight: 700; line-height: 1.45; }

.chapter-confirmation {
  display: grid;
  width: min(100%, 620px);
  gap: 6px;
  margin: 16px auto 0;
  padding: 14px 16px;
  border-left: 4px solid #b66b24;
  background: #fff9f1;
  text-align: left;
}
.chapter-confirmation-kicker { color: #91571e; font-size: 12px; font-weight: 800; }
.chapter-confirmation-text { color: #4f4030; font-size: 16px; font-weight: 700; line-height: 1.55; }
.chapter-confirmation-actions { display: grid; grid-template-columns: 1fr 1fr 1.3fr; gap: 8px; margin-top: 5px; }
.chapter-confirmation-actions button {
  min-height: 48px;
  margin: 0;
  padding: 0 10px;
  border: 1px solid #d9c29d;
  border-radius: 6px;
  background: #fffdf8;
  color: #81501e;
  font-size: 15px;
  font-weight: 850;
}
.chapter-confirmation-actions button::after { border: 0; }
.chapter-confirmation-actions button:first-child { border-color: #72a88e; background: #edf7f1; color: #216a4d; }
.chapter-confirmation-actions button[disabled] { opacity: 0.52; }

.service-status-list {
  display: grid;
  width: min(100%, 620px);
  margin: 14px auto 0;
  border-top: 1px solid #dfe5df;
  background: #fff;
}
.service-status-row {
  display: grid;
  grid-template-columns: 12px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  min-height: 58px;
  padding: 9px 14px;
  border-right: 1px solid #dfe5df;
  border-bottom: 1px solid #dfe5df;
  border-left: 1px solid #dfe5df;
}
.service-status-mark { width: 9px; height: 9px; border-radius: 50%; background: #aab7b0; }
.service-status-mark.working { background: #b66b24; animation: statusPulse 1.2s ease-in-out infinite; }
.service-status-mark.ready { background: #2f8a68; }
.service-status-mark.retry { background: #b24932; }
.service-status-copy { display: grid; min-width: 0; gap: 2px; }
.service-status-copy text:first-child { color: #33483d; font-size: 14px; font-weight: 850; }
.service-status-copy text:last-child { overflow: hidden; color: #718078; font-size: 12px; line-height: 1.35; text-overflow: ellipsis; white-space: nowrap; }
.service-status-retry {
  min-width: 58px;
  min-height: 40px;
  margin: 0;
  padding: 0 10px;
  border: 1px solid #c7d9d0;
  border-radius: 6px;
  background: #f4faf7;
  color: #1f7257;
  font-size: 13px;
  font-weight: 800;
}
.service-status-retry::after { border: 0; }

.progress-toggle {
  min-height: 44px;
  padding: 0 12px;
  border: 0;
  background: transparent;
  color: #53645d;
  font-size: 14px;
}

.progress-band {
  overflow: hidden;
  margin-top: 8px;
  padding: 22px 20px 20px;
  border: 1px solid #dfe5df;
  border-radius: 18px 18px 0 0;
  background: #fff;
  box-shadow: 0 10px 26px rgba(38, 54, 48, 0.06);
}

.progress-heading { justify-content: space-between; gap: 14px; }
.progress-heading > view { display: flex; align-items: baseline; gap: 9px; }
.progress-value { color: #1f7257; font-size: 30px; font-weight: 800; }
.section-kicker { color: #69766f; font-size: 13px; font-weight: 700; }
.progress-toggle {
  min-width: 88px;
  border-radius: 999px;
  background: #edf6f1;
  color: #1f7257;
  font-weight: 800;
}
.progress-empty-label { color: #74827a; font-size: 13px; font-weight: 700; }
.progress-track,
.chapter-track { overflow: hidden; background: #e5eae6; }
.progress-track { height: 8px; margin: 14px 0 10px; border-radius: 4px; }
.progress-track view { height: 100%; border-radius: inherit; background: #d26949; }
.progress-summary { color: #68756f; font-size: 13px; }

.chapter-list {
  overflow: hidden;
  border-right: 1px solid #dfe5df;
  border-bottom: 1px solid #dfe5df;
  border-left: 1px solid #dfe5df;
  border-radius: 0 0 18px 18px;
  background: #fff;
  box-shadow: 0 10px 26px rgba(38, 54, 48, 0.05);
}
.chapter-row { display: grid; grid-template-columns: 42px minmax(0, 1fr); gap: 12px; min-height: 76px; padding: 18px 20px; border-bottom: 1px solid #e6eae7; transition: background-color 160ms ease, box-shadow 160ms ease; }
.chapter-row:active { background: #f2f5f2; }
.chapter-row.focused { background: #edf6ee; box-shadow: inset 4px 0 0 #418267; }
.chapter-index { display: grid; place-items: center; width: 38px; height: 38px; border-radius: 50%; background: #eef2ef; color: #53635c; font-weight: 800; }
.chapter-row.focused .chapter-index { background: #d4ead9; color: #246b4d; }
.chapter-copy { min-width: 0; }
.chapter-title-line { justify-content: space-between; gap: 12px; margin-bottom: 5px; }
.chapter-name { font-size: 16px; font-weight: 800; }
.chapter-copy > text { color: #75817b; font-size: 12px; line-height: 1.5; }
.chapter-status { flex: 0 0 auto; font-size: 11px; font-weight: 800; }
.chapter-status.completed { color: #237457; }
.chapter-status.confirm { color: #a15c35; }
.chapter-status.collecting { color: #65578d; }
.chapter-status.not_started { color: #89938e; }
.chapter-focus-label { flex: 0 0 auto; color: #246b4d; font-size: 11px; font-weight: 800; }
.chapter-action-hint { display: block; margin-top: 5px; color: #587866; font-size: 12px; line-height: 1.45; }
.chapter-track { height: 4px; margin-top: 10px; border-radius: 2px; }
.chapter-track view { height: 100%; border-radius: inherit; background: #6f9887; }

.recording-overlay {
  position: fixed;
  inset: 0;
  z-index: 20;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding-top: max(30px, env(safe-area-inset-top));
  background: rgba(28, 39, 34, 0.48);
}

.recording-sheet {
  display: flex;
  flex-direction: column;
  width: min(100%, 760px);
  max-height: 88vh;
  max-height: 88dvh;
  padding-bottom: env(safe-area-inset-bottom);
  border-radius: 8px 8px 0 0;
  background: #f7f9f7;
  box-shadow: 0 -10px 36px rgba(21, 40, 32, 0.2);
}

.recording-sheet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 20px;
  border-bottom: 1px solid #dfe5df;
  background: #fff;
}
.recording-sheet-header > view { display: grid; gap: 4px; min-width: 0; }
.recording-sheet-title { color: #263630; font-size: 21px; font-weight: 800; }
.recording-sheet-summary { color: #738079; font-size: 12px; line-height: 1.45; }
.sheet-close {
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: #edf1ee;
  color: #46564f;
  font-size: 29px;
  line-height: 42px;
}
.sheet-close::after { border: 0; }
.recording-scroll { flex: 1; min-height: 260px; max-height: calc(88dvh - 86px); }
.recording-empty { display: grid; justify-items: center; gap: 16px; padding: 64px 28px; color: #738079; font-size: 15px; text-align: center; }
.empty-record-mark { width: 42px; height: 42px; border: 4px solid #8ba497; border-radius: 50%; box-shadow: inset 0 0 0 8px #f7f9f7; background: #c95f44; }

.recording-session { border-bottom: 1px solid #e0e6e1; background: #fff; }
.recording-row {
  display: grid;
  grid-template-columns: 52px minmax(0, 1fr);
  gap: 14px;
  padding: 20px;
  background: #fff;
}
.recording-play {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  padding: 0 0 0 2px;
  border: 0;
  border-radius: 50%;
  background: #1f7257;
  color: #fff;
  font-size: 18px;
  line-height: 1;
}
.recording-play::after { border: 0; }
.recording-copy { display: grid; min-width: 0; }
.recording-title { color: #263630; font-size: 16px; font-weight: 800; line-height: 1.4; overflow-wrap: anywhere; }
.recording-meta { margin-top: 3px; color: #7b8781; font-size: 11px; }
.recording-transcript { display: -webkit-box; overflow: hidden; margin-top: 9px; color: #54645d; font-size: 13px; line-height: 1.55; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.recording-actions { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 10px; }
.recording-actions button {
  min-width: 54px;
  min-height: 40px;
  margin: 0;
  padding: 0 11px;
  border: 0;
  background: transparent;
  color: #1f7257;
  font-size: 13px;
  font-weight: 700;
}
.recording-actions button::after { border: 0; }
.recording-actions .delete-recording { color: #aa4b36; }

.recording-segments { margin: 0 16px 16px 86px; border-top: 1px solid #e4e9e5; }
.recording-segment { display: grid; grid-template-columns: 40px minmax(0, 1fr); gap: 10px; padding: 14px 0; border-bottom: 1px solid #e8ece9; }
.recording-segment:last-child { border-bottom: 0; }
.segment-play {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  padding: 0 0 0 2px;
  border: 0;
  border-radius: 50%;
  background: #e2f1ea;
  color: #1f7257;
  font-size: 14px;
  font-weight: 900;
}
.segment-play::after { border: 0; }
.segment-copy { display: grid; min-width: 0; gap: 4px; }
.segment-title { color: #40544a; font-size: 13px; font-weight: 850; }
.segment-transcript { display: -webkit-box; overflow: hidden; color: #617168; font-size: 13px; line-height: 1.48; -webkit-box-orient: vertical; -webkit-line-clamp: 3; }
.segment-actions { display: flex; gap: 4px; margin-top: 2px; }
.segment-actions button { min-height: 36px; margin: 0; padding: 0 8px; border: 0; background: transparent; color: #1f7257; font-size: 12px; font-weight: 800; }
.segment-actions button::after { border: 0; }
.segment-actions .delete-recording { color: #aa4b36; }

@keyframes listen { from { transform: scaleY(0.5); } to { transform: scaleY(1); } }
@keyframes speak { from { transform: scaleY(0.45); } to { transform: scaleY(1.08); } }
@keyframes think { 0%, 100% { opacity: 0.38; } 50% { opacity: 1; } }
@keyframes holdPulse { 0%, 100% { box-shadow: 0 12px 28px rgba(31, 114, 87, 0.16); } 50% { box-shadow: 0 14px 34px rgba(31, 114, 87, 0.3); } }
@keyframes statusPulse { 0%, 100% { opacity: 0.55; } 50% { opacity: 1; } }

@media (min-width: 700px) {
  .interview-main { padding-top: 32px; }
  .conversation-stage { min-height: 450px; }
  .progress-band,
  .chapter-list { margin-left: 12px; margin-right: 12px; }
}

@media (max-width: 520px) {
  .topbar { padding-left: 14px; padding-right: 14px; }
  .topbar-actions { gap: 6px; }
  .recordings-entry { min-width: 86px; min-height: 46px; padding: 6px 10px 6px 6px; gap: 7px; }
  .recording-entry-icon { width: 32px; height: 32px; }
  .recording-entry-count { display: none; }
  .user-entry { width: 46px; min-height: 46px; padding: 6px; justify-content: center; }
  .user-name { display: none; }
  .session-time { width: 46px; min-height: 46px; padding: 6px; }
  .session-time-icon { width: 28px; height: 28px; }
  .conversation-scroll { height: 142px; }
  .conversation-scroll.compact { height: 100px; }
  .conversation-text { font-size: 20px; line-height: 1.5; }
  .interview-order-options { grid-template-columns: 1fr; gap: 8px; }
  .interview-order-option { min-height: 60px; grid-template-columns: 108px minmax(0, 1fr); align-items: center; }
  .recording-sheet { max-height: 92dvh; }
  .recording-scroll { max-height: calc(92dvh - 86px); }
  .recording-segments { margin-left: 16px; }
}

@media (prefers-reduced-motion: reduce) {
  .voice-bar { animation: none !important; }
}
</style>
