export type InterviewStatus =
  | "ready"
  | "listening"
  | "thinking"
  | "speaking"
  | "paused"
  | "reconnecting"
  | "error";

export interface InterviewState {
  status: InterviewStatus;
  partialTranscript: string;
  finalTranscript: string;
  assistantText: string;
  errorMessage: string;
  resumeStatus: InterviewStatus;
}

export type InterviewEvent =
  | { type: "START_LISTENING" }
  | { type: "STOP_LISTENING" }
  | { type: "CANCEL_LISTENING" }
  | { type: "PARTIAL_TRANSCRIPT"; text: string }
  | { type: "FINAL_TRANSCRIPT"; text: string }
  | { type: "ASSISTANT_DRAFT"; text: string }
  | { type: "ASSISTANT_STARTED"; text: string }
  | { type: "ASSISTANT_FINISHED" }
  | { type: "USER_INTERRUPTED" }
  | { type: "PAUSE" }
  | { type: "RESUME" }
  | { type: "NETWORK_LOST" }
  | { type: "NETWORK_RESTORED" }
  | { type: "FAIL"; message: string }
  | { type: "RETRY" };

export const initialInterviewState: InterviewState = {
  status: "ready",
  partialTranscript: "",
  finalTranscript: "",
  assistantText: "",
  errorMessage: "",
  resumeStatus: "ready",
};

export function isInterviewActive(status: InterviewStatus, sessionStarted: boolean): boolean {
  return sessionStarted && status !== "paused" && status !== "error";
}

export function interviewStartLabel(status: InterviewStatus, sessionStarted: boolean): "开始采访" | "继续采访" {
  return sessionStarted || status === "paused" || status === "error" ? "继续采访" : "开始采访";
}

export function reduceInterviewState(state: InterviewState, event: InterviewEvent): InterviewState {
  switch (event.type) {
    case "START_LISTENING":
      return { ...state, status: "listening", partialTranscript: "", finalTranscript: "", errorMessage: "" };
    case "STOP_LISTENING":
      if (state.status !== "listening") return state;
      return { ...state, status: "thinking", partialTranscript: "", finalTranscript: state.partialTranscript };
    case "CANCEL_LISTENING":
      if (state.status !== "listening") return state;
      return { ...state, status: "ready", partialTranscript: "", finalTranscript: "" };
    case "PARTIAL_TRANSCRIPT":
      if (state.status !== "listening") return state;
      return { ...state, partialTranscript: event.text };
    case "FINAL_TRANSCRIPT":
      if (state.status !== "listening" && state.status !== "thinking") return state;
      return { ...state, status: "thinking", partialTranscript: "", finalTranscript: event.text, assistantText: "" };
    case "ASSISTANT_DRAFT":
      if (state.status !== "thinking") return state;
      return { ...state, assistantText: event.text };
    case "ASSISTANT_STARTED":
      if (state.status !== "ready" && state.status !== "thinking") return state;
      return { ...state, status: "speaking", assistantText: event.text };
    case "ASSISTANT_FINISHED":
      if (state.status !== "speaking") return state;
      return { ...state, status: "ready" };
    case "USER_INTERRUPTED":
	  if (state.status !== "speaking" && state.status !== "thinking") return state;
      return { ...state, status: "listening", partialTranscript: "", finalTranscript: "" };
    case "PAUSE":
      if (state.status === "paused") return state;
      return { ...state, status: "paused", resumeStatus: state.status };
    case "RESUME":
      if (state.status !== "paused") return state;
      return { ...state, status: state.resumeStatus === "speaking" ? "ready" : state.resumeStatus };
    case "NETWORK_LOST":
      return { ...state, status: "reconnecting", resumeStatus: state.status };
    case "NETWORK_RESTORED":
      if (state.status !== "reconnecting") return state;
      return { ...state, status: state.resumeStatus === "speaking" ? "ready" : state.resumeStatus };
    case "FAIL":
      return { ...state, status: "error", errorMessage: event.message, resumeStatus: state.status };
    case "RETRY":
      if (state.status !== "error") return state;
      return { ...state, status: "ready", errorMessage: "" };
    default:
      return state;
  }
}

export const interviewStatusCopy: Record<InterviewStatus, { label: string; detail: string }> = {
  ready: { label: "可以继续了", detail: "准备好后，我们接着聊" },
  listening: { label: "我在听", detail: "您慢慢说，不着急" },
  thinking: { label: "我听到了", detail: "正在想接下来问什么" },
  speaking: { label: "正在和您说", detail: "按住话筒可补充或打断" },
  paused: { label: "已经暂停", detail: "今天的内容都保存好了" },
  reconnecting: { label: "正在恢复", detail: "网络回来后会接着保存" },
  error: { label: "暂时没有听清", detail: "可以再试一次" },
};
