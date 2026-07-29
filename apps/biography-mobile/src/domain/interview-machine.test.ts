import { describe, expect, it } from "vitest";
import { initialInterviewState, interviewStartLabel, isInterviewActive, reduceInterviewState } from "./interview-machine";

describe("interview state machine", () => {
  it("does not expose stop as the main interview control", () => {
    expect(isInterviewActive("ready", false)).toBe(false);
    expect(interviewStartLabel("ready", false)).toBe("开始采访");
    expect(interviewStartLabel("listening", true)).toBe("继续采访");
    expect(interviewStartLabel("thinking", true)).toBe("继续采访");
    expect(interviewStartLabel("speaking", true)).toBe("继续采访");
    expect(interviewStartLabel("paused", true)).toBe("继续采访");
    expect(interviewStartLabel("error", true)).toBe("继续采访");
  });

  it("moves through a complete interview turn", () => {
    const listening = reduceInterviewState(initialInterviewState, { type: "START_LISTENING" });
    const thinking = reduceInterviewState(listening, { type: "FINAL_TRANSCRIPT", text: "我十九岁去了上海。" });
    const speaking = reduceInterviewState(thinking, { type: "ASSISTANT_STARTED", text: "那是第一次离开家吗？" });
    const ready = reduceInterviewState(speaking, { type: "ASSISTANT_FINISHED" });

    expect(listening.status).toBe("listening");
    expect(thinking.status).toBe("thinking");
    expect(thinking.finalTranscript).toBe("我十九岁去了上海。");
    expect(speaking.status).toBe("speaking");
    expect(ready.status).toBe("ready");
  });

  it("shows a streaming interviewer draft while the final reply is still being generated", () => {
    const listening = reduceInterviewState(initialInterviewState, { type: "START_LISTENING" });
    const thinking = reduceInterviewState(listening, { type: "FINAL_TRANSCRIPT", text: "父亲背着我过水。" });
    const draft = reduceInterviewState(thinking, { type: "ASSISTANT_DRAFT", text: "当时趴在父亲背上" });
    const ignored = reduceInterviewState(initialInterviewState, { type: "ASSISTANT_DRAFT", text: "不应显示" });

    expect(draft.status).toBe("thinking");
    expect(draft.assistantText).toBe("当时趴在父亲背上");
    expect(ignored).toEqual(initialInterviewState);
  });

  it("can speak the opening prompt before the first listening turn", () => {
    const speaking = reduceInterviewState(initialInterviewState, {
      type: "ASSISTANT_STARTED",
      text: "我们从您最想讲的一段经历开始。",
    });

    expect(speaking.status).toBe("speaking");
    expect(speaking.assistantText).toBe("我们从您最想讲的一段经历开始。");
  });

  it("keeps a manually committed recording in thinking until the final transcript arrives", () => {
    const listening = reduceInterviewState(initialInterviewState, { type: "START_LISTENING" });
    const partial = reduceInterviewState(listening, { type: "PARTIAL_TRANSCRIPT", text: "那一年我十九岁" });
    const committed = reduceInterviewState(partial, { type: "STOP_LISTENING" });
    const final = reduceInterviewState(committed, { type: "FINAL_TRANSCRIPT", text: "那一年我十九岁，去了上海。" });

    expect(committed.status).toBe("thinking");
    expect(committed.finalTranscript).toBe("那一年我十九岁");
    expect(final.status).toBe("thinking");
    expect(final.finalTranscript).toBe("那一年我十九岁，去了上海。");
  });

  it("lets user speech interrupt playback immediately", () => {
    const speaking = { ...initialInterviewState, status: "speaking" as const, assistantText: "我们继续聊聊。" };
    const interrupted = reduceInterviewState(speaking, { type: "USER_INTERRUPTED" });

    expect(interrupted.status).toBe("listening");
    expect(interrupted.partialTranscript).toBe("");
  });

  it("returns to ready when a held recording is canceled", () => {
    const listening = reduceInterviewState(initialInterviewState, { type: "START_LISTENING" });
    const partial = reduceInterviewState(listening, { type: "PARTIAL_TRANSCRIPT", text: "这段不要发" });
    const ready = reduceInterviewState(partial, { type: "CANCEL_LISTENING" });

    expect(ready.status).toBe("ready");
    expect(ready.partialTranscript).toBe("");
    expect(ready.finalTranscript).toBe("");
  });

  it("does not accept partial transcripts outside listening", () => {
    const state = reduceInterviewState(initialInterviewState, { type: "PARTIAL_TRANSCRIPT", text: "不应该保存" });
    expect(state).toEqual(initialInterviewState);
  });

  it("recovers to a safe state when the network drops during playback", () => {
    const speaking = { ...initialInterviewState, status: "speaking" as const };
    const reconnecting = reduceInterviewState(speaking, { type: "NETWORK_LOST" });
    const recovered = reduceInterviewState(reconnecting, { type: "NETWORK_RESTORED" });

    expect(reconnecting.status).toBe("reconnecting");
    expect(recovered.status).toBe("ready");
  });

  it("can speak an idle reminder and then enter the paused state", () => {
    const listening = reduceInterviewState(initialInterviewState, { type: "START_LISTENING" });
    const thinking = reduceInterviewState(listening, { type: "STOP_LISTENING" });
    const reminding = reduceInterviewState(thinking, { type: "ASSISTANT_STARTED", text: "我们先休息一下。" });
    const ready = reduceInterviewState(reminding, { type: "ASSISTANT_FINISHED" });
    const paused = reduceInterviewState(ready, { type: "PAUSE" });

    expect(reminding.status).toBe("speaking");
    expect(paused.status).toBe("paused");
  });
});
