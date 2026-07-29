<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { interviewStartLabel, interviewStatusCopy, isInterviewActive, initialInterviewState, reduceInterviewState, type InterviewEvent } from "@/domain/interview-machine";
import { shouldPauseAfterNoSpeech } from "@/domain/no-speech-policy";
import { continueInterview, getEmptyProject, getInitialProject, openingInterviewPrompt, type Chapter } from "@/services/interview";
import {
  buildNextInterviewPrompt,
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
  formatRecordingDate,
  formatRecordingDuration,
  listRecordings,
  recordingAudioBlob,
  recordingFilePaths,
  renameRecording,
  type StoredRecording,
} from "@/services/recordings";
import { createVoiceAdapter, type VoiceAdapter, type VoiceEvent } from "@/services/voice";

const state = ref({ ...initialInterviewState });
const project = ref(getInitialProject());
const voice = ref<VoiceAdapter | null>(null);
const elapsedSeconds = ref(0);
const showChapters = ref(true);
const sessionStarted = ref(false);
const voiceOperationPending = ref(false);
const recordings = ref<StoredRecording[]>([]);
const showRecordingManager = ref(false);
const playingRecordingID = ref("");
const lastInterviewSession = ref<LastInterviewSession | null>(loadLastInterviewSession());
const talkButtonHeld = ref(false);
const talkButtonCanceling = ref(false);
const pendingTranscriptBuffer = ref("");
const followupCountdown = ref(0);
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
const followupDelayMs = 3_000;
const slideCancelDistance = 54;
let activeRecordingID = "";
let recordingSegmentExpected = false;
let recordingStorePending = false;
let finishRecordingAfterNextSegment = false;
let recordingPlayer: ReturnType<typeof uni.createInnerAudioContext> | null = null;
let recordingPlaybackQueue: string[] = [];
const recordingURLs = new Map<string, string>();

const statusCopy = computed(() => interviewStatusCopy[state.value.status]);
const elapsedLabel = computed(() => {
  const minutes = Math.floor(elapsedSeconds.value / 60).toString().padStart(2, "0");
  const seconds = (elapsedSeconds.value % 60).toString().padStart(2, "0");
  return `${minutes}:${seconds}`;
});
const activeChapterCount = computed(() => project.value.chapters.filter((chapter) => chapter.status === "collecting" || chapter.status === "confirm").length);
const progressSummary = computed(() => {
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
  if (state.value.assistantText) return state.value.assistantText;
  if (project.value.overallProgress === 0) return openingInterviewPrompt;
  return buildPreviousInterviewGuidance(project.value);
});
const interviewActive = computed(() => isInterviewActive(state.value.status, sessionStarted.value));
const showStartButton = computed(() => !sessionStarted.value || state.value.status === "paused" || state.value.status === "error");
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
const nextInterviewPrompt = computed(() => buildNextInterviewPrompt(project.value));
const recordingSummary = computed(() => recordings.value.length === 0 ? "还没有录音" : `已保存 ${recordings.value.length} 次采访`);
const recordingEntrySummary = computed(() => recordings.value.length === 0 ? "未保存" : `${recordings.value.length} 次`);
const canHoldToTalk = computed(() => {
  const inHoldWindow = state.value.status === "ready" || state.value.status === "speaking" || Boolean(pendingTranscriptBuffer.value);
  if (!sessionStarted.value || !inHoldWindow) return false;
  if (state.value.status === "speaking") return true;
  return !voiceOperationPending.value;
});
const holdTalkDisabled = computed(() => !talkButtonHeld.value && !canHoldToTalk.value);
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

async function handleVoiceEvent(event: VoiceEvent) {
  if (event.type === "project_loaded") {
    project.value = event.project;
    void refreshRecordings();
    return;
  }
  if (event.type === "partial_transcript") {
    if (event.text.trim()) consecutiveNoSpeechCount = 0;
    dispatch({ type: "PARTIAL_TRANSCRIPT", text: event.text });
    return;
  }
  if (event.type === "input_committed") {
    dispatch({ type: "STOP_LISTENING" });
    return;
  }
  if (event.type === "final_transcript") {
    if (event.text.trim()) consecutiveNoSpeechCount = 0;
    dispatch({ type: "FINAL_TRANSCRIPT", text: event.text });
    recordingSegmentExpected = voice.value?.mode !== "mock" && Boolean(event.text.trim());
    if (event.text.trim()) {
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
    await handleAssistantReply(event.text, event.expression, event.project);
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
    return;
  }
  if (event.type === "speech_detected" && (state.value.status === "speaking" || state.value.status === "thinking")) {
    await interruptAssistant();
    return;
  }
  if (event.type === "network_lost") dispatch({ type: "NETWORK_LOST" });
  if (event.type === "network_restored") {
    dispatch({ type: "NETWORK_RESTORED" });
  }
  if (event.type === "error") {
    const userRequestedPause = pauseAfterTurn;
    pauseAfterTurn = false;
    if (event.code === "no_speech" && voice.value?.mode !== "mock") {
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
    dispatch({ type: "FAIL", message: event.message });
  }
}

async function refreshRecordings() {
  try {
    recordings.value = await listRecordings(project.value.id);
  } catch {
    recordings.value = [];
  }
}

async function storeVoiceRecording(event: Extract<VoiceEvent, { type: "recording_ready" }>) {
  const chapter = project.value.chapters.find((item) => item.status === "collecting")
    || project.value.chapters.find((item) => item.status === "confirm")
    || project.value.chapters[0];
  try {
    const existing = recordings.value.find((item) => item.id === activeRecordingID);
    const recording = await appendRecordingSegment(existing, {
      projectID: project.value.id,
      chapterID: chapter?.id || "uncategorized",
      chapterTitle: chapter?.title || "未分类",
      transcript: event.transcript,
      durationMs: event.durationMs,
      audio: event.audio,
      filePath: event.filePath,
      sizeBytes: event.sizeBytes,
      cumulative: event.cumulative,
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
  } catch (error) {
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
}

async function toggleRecordingPlayback(recording: StoredRecording) {
  if (playingRecordingID.value === recording.id) {
    stopRecordingPlayback();
    return;
  }
  stopRecordingPlayback();
  const nativeSources = recordingFilePaths(recording);
  const source = nativeSources.length > 0 ? "" : await recordingURL(recording);
  recordingPlaybackQueue = nativeSources.length > 0 ? nativeSources : source ? [source] : [];
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

function editRecordingTitle(recording: StoredRecording) {
  uni.showModal({
    title: "修改录音名称",
    content: recording.title,
    editable: true,
    placeholderText: "请输入名称",
    success: async (result) => {
      if (!result.confirm) return;
      const title = String(result.content || "").trim();
      if (!title) return uni.showToast({ title: "名称不能为空", icon: "none" });
      try {
        await renameRecording(recording.id, title);
        recording.title = title;
      } catch (error) {
        uni.showToast({ title: error instanceof Error ? error.message : "修改失败", icon: "none" });
      }
    },
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

function closeRecordingManager() {
  stopRecordingPlayback();
  showRecordingManager.value = false;
}

async function handleAssistantReply(text: string, expression: string, nextProject: typeof project.value) {
  project.value = nextProject;
  if (pauseAfterTurn) {
    pauseAfterTurn = false;
    return;
  }
  dispatch({ type: "ASSISTANT_STARTED", text });
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
    await voice.value?.startListening(options);
    sessionStarted.value = true;
    dispatch({ type: "START_LISTENING" });
  } catch (error) {
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
  if (state.value.status === "error") {
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
  voice.value = createVoiceAdapter();
  if (voice.value.mode !== "mock") project.value = getEmptyProject();
  unsubscribeVoice = voice.value.subscribe((event) => void handleVoiceEvent(event));
  void voice.value.prepare().catch((error) => {
    dispatch({ type: "FAIL", message: error instanceof Error ? error.message : "语音服务暂时无法连接" });
  });
  void refreshRecordings();
  elapsedTimer = setInterval(() => {
    if (sessionStarted.value && state.value.status !== "paused") elapsedSeconds.value += 1;
  }, 1000);
});

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
        <view class="session-time">
          <view class="session-time-icon" aria-hidden="true" />
          <view class="session-time-copy">
            <text>{{ sessionTimeTitle }}</text>
            <text class="session-time-value">{{ sessionTimeValue }}</text>
          </view>
        </view>
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

        <scroll-view scroll-y class="conversation-scroll" :show-scrollbar="false">
          <text class="conversation-text">{{ currentCaption }}</text>
        </scroll-view>

        <view class="primary-zone">
          <button v-if="showStartButton" class="primary-voice-button" :disabled="voiceOperationPending" @click="primaryAction">
            <image class="mic-icon" src="/static/mic.svg" mode="aspectFit" aria-hidden="true" />
            <text>{{ primaryLabel }}</text>
          </button>
          <view
            :class="['hold-talk-button', { active: talkButtonHeld, canceling: talkButtonCanceling, disabled: holdTalkDisabled }]"
            role="button"
            aria-label="按住话筒说话"
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
            <text>{{ talkButtonHeld ? (talkButtonCanceling ? "取消发送" : "松开发送") : "按住说话" }}</text>
          </view>
          <text class="hold-talk-hint">{{ holdHint }}</text>
          <button v-if="sessionStarted && state.status !== 'paused'" class="end-session-button" :disabled="voiceOperationPending" @click="confirmEndInterview">
            <text class="end-session-icon" aria-hidden="true">✓</text>
            <text>结束今天</text>
          </button>
        </view>
      </view>

      <view class="progress-band">
        <view class="progress-heading">
          <view>
            <text class="section-kicker">这本书已经完成</text>
            <text class="progress-value">{{ project.overallProgress }}%</text>
          </view>
          <button class="progress-toggle" @click="showChapters = !showChapters">{{ showChapters ? "收起章节" : "查看章节" }}</button>
        </view>
        <view class="progress-track"><view :style="{ width: `${project.overallProgress}%` }" /></view>
        <text class="progress-summary">{{ progressSummary }}</text>
      </view>

      <view v-if="showChapters" class="chapter-list">
        <view v-for="chapter in project.chapters" :key="chapter.id" class="chapter-row">
          <view class="chapter-index">{{ project.chapters.indexOf(chapter) + 1 }}</view>
          <view class="chapter-copy">
            <view class="chapter-title-line">
              <text class="chapter-name">{{ chapter.title }}</text>
              <text :class="chapterClass(chapter)">{{ chapter.statusLabel }}</text>
            </view>
            <text>{{ chapter.detail }}</text>
            <view class="chapter-track"><view :style="{ width: `${chapter.progress}%` }" /></view>
          </view>
        </view>
      </view>

      <view class="next-choice">
        <text class="section-kicker">接下来</text>
        <text class="next-prompt">{{ nextInterviewPrompt }}</text>
      </view>
    </view>
    <view class="safe-bottom" />

    <view v-if="showRecordingManager" class="recording-overlay" @click.self="closeRecordingManager">
      <view class="recording-sheet">
        <view class="recording-sheet-header">
          <view>
            <text class="recording-sheet-title">录音记录</text>
            <text class="recording-sheet-summary">{{ recordingSummary }}，只保存您的正式讲述</text>
          </view>
          <button class="sheet-close" aria-label="关闭" @click="closeRecordingManager">×</button>
        </view>

        <scroll-view scroll-y class="recording-scroll">
          <view v-if="recordings.length === 0" class="recording-empty">
            <text class="empty-record-mark" aria-hidden="true" />
            <text>结束一次采访后，完整录音会自动保存在这里</text>
          </view>
          <view v-for="recording in recordings" :key="recording.id" class="recording-row">
            <button class="recording-play" :aria-label="playingRecordingID === recording.id ? '暂停录音' : '播放录音'" @click="toggleRecordingPlayback(recording)">
              {{ playingRecordingID === recording.id ? "Ⅱ" : "▶" }}
            </button>
            <view class="recording-copy">
              <text class="recording-title">{{ recording.title }}</text>
              <text class="recording-meta">{{ formatRecordingDate(recording.createdAt) }} · {{ formatRecordingDuration(recording.durationMs) }}</text>
              <text class="recording-transcript">{{ recording.transcript }}</text>
              <view class="recording-actions">
                <button @click="editRecordingTitle(recording)">改名</button>
                <button @click="exportRecording(recording)">导出</button>
                <button class="delete-recording" @click="confirmDeleteRecording(recording)">删除</button>
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
.session-time {
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 172px;
  min-height: 48px;
  padding: 6px 12px 6px 8px;
  border: 1px solid #e2e8e4;
  border-radius: 999px;
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
.session-time-copy { display: grid; min-width: 0; gap: 2px; text-align: left; }
.session-time text { overflow: hidden; color: #7b8781; font-size: 11px; line-height: 1.15; text-overflow: ellipsis; white-space: nowrap; }
.session-time-value { color: #31463d; font-size: 14px; font-weight: 850; }

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
  min-height: 445px;
  padding: 16px 0 28px;
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
  height: 186px;
  margin-bottom: 2px;
}

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

.primary-zone { display: grid; justify-items: center; gap: 12px; margin-top: 22px; }

.primary-voice-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-width: 190px;
  min-height: 58px;
  padding: 0 24px;
  border: 0;
  border-radius: 8px;
  background: #1f7257;
  color: #fff;
  font-size: 18px;
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
  width: 142px;
  height: 142px;
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
.hold-talk-button text { font-size: 17px; font-weight: 800; line-height: 1; }

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
.chapter-row { display: grid; grid-template-columns: 42px minmax(0, 1fr); gap: 12px; padding: 18px 20px; border-bottom: 1px solid #e6eae7; }
.chapter-index { display: grid; place-items: center; width: 38px; height: 38px; border-radius: 50%; background: #eef2ef; color: #53635c; font-weight: 800; }
.chapter-copy { min-width: 0; }
.chapter-title-line { justify-content: space-between; gap: 12px; margin-bottom: 5px; }
.chapter-name { font-size: 16px; font-weight: 800; }
.chapter-copy > text { color: #75817b; font-size: 12px; line-height: 1.5; }
.chapter-status { flex: 0 0 auto; font-size: 11px; font-weight: 800; }
.chapter-status.completed { color: #237457; }
.chapter-status.confirm { color: #a15c35; }
.chapter-status.collecting { color: #65578d; }
.chapter-status.not_started { color: #89938e; }
.chapter-track { height: 4px; margin-top: 10px; border-radius: 2px; }
.chapter-track view { height: 100%; border-radius: inherit; background: #6f9887; }

.next-choice {
  display: grid;
  gap: 7px;
  margin-top: 18px;
  padding: 22px 20px;
  border: 1px solid #e4ded0;
  border-left: 5px solid #d26949;
  border-radius: 18px;
  background: #fffaf0;
  box-shadow: 0 10px 24px rgba(151, 103, 54, 0.08);
}
.next-prompt { font-size: 17px; font-weight: 800; line-height: 1.5; }

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

.recording-row {
  display: grid;
  grid-template-columns: 52px minmax(0, 1fr);
  gap: 14px;
  padding: 20px;
  border-bottom: 1px solid #e0e6e1;
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

@keyframes listen { from { transform: scaleY(0.5); } to { transform: scaleY(1); } }
@keyframes speak { from { transform: scaleY(0.45); } to { transform: scaleY(1.08); } }
@keyframes think { 0%, 100% { opacity: 0.38; } 50% { opacity: 1; } }
@keyframes holdPulse { 0%, 100% { box-shadow: 0 12px 28px rgba(31, 114, 87, 0.16); } 50% { box-shadow: 0 14px 34px rgba(31, 114, 87, 0.3); } }

@media (min-width: 700px) {
  .interview-main { padding-top: 32px; }
  .conversation-stage { min-height: 485px; }
  .progress-band,
  .chapter-list,
  .next-choice { margin-left: 12px; margin-right: 12px; }
  .next-choice { margin-bottom: 24px; }
}

@media (max-width: 520px) {
  .topbar { padding-left: 14px; padding-right: 14px; }
  .topbar-actions { gap: 6px; }
  .recordings-entry { min-width: 86px; min-height: 46px; padding: 6px 10px 6px 6px; gap: 7px; }
  .recording-entry-icon { width: 32px; height: 32px; }
  .recording-entry-count { display: none; }
  .session-time { max-width: 104px; min-height: 46px; padding: 6px 9px 6px 6px; gap: 6px; }
  .session-time-icon { width: 28px; height: 28px; }
  .session-time-copy text:first-child { display: none; }
  .session-time-value { font-size: 13px; }
  .conversation-scroll { height: 168px; }
  .conversation-text { font-size: 20px; line-height: 1.5; }
  .recording-sheet { max-height: 92dvh; }
  .recording-scroll { max-height: calc(92dvh - 86px); }
}

@media (prefers-reduced-motion: reduce) {
  .voice-bar { animation: none !important; }
}
</style>
