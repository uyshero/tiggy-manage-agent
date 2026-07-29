import { describe, expect, it } from "vitest";
import { getEmptyProject } from "./interview";
import { buildNextInterviewPrompt, buildPreviousInterviewGuidance, formatInterviewDuration, formatInterviewMoment } from "./interview-history";

describe("interview history", () => {
  it("formats the exact last interview time and duration", () => {
    expect(formatInterviewMoment(new Date(2026, 6, 28, 9, 5).getTime())).toBe("7月28日 09:05");
    expect(formatInterviewDuration(728)).toBe("12分08秒");
    expect(formatInterviewDuration(4_020)).toBe("1小时07分");
  });

  it("guides the narrator back to the chapter that was in progress", () => {
    const project = getEmptyProject();
    project.overallProgress = 32;
    project.pendingConfirmation = "父亲当年工作的具体地点";
    project.chapters = [
      { id: "shanghai", title: "第一次去上海", status: "confirm", statusLabel: "待确认", progress: 72, detail: "还有细节需要确认", nextFocus: "补充第一次独立生活时的感受和后来影响" },
      { id: "craft", title: "跟周师傅学手艺", status: "collecting", statusLabel: "讲述中", progress: 46, detail: "正在收集师傅和第一次工作的故事", nextFocus: "补充第一次独立完成木器时的感受和后来影响" },
    ];
    expect(buildPreviousInterviewGuidance(project)).toContain("上次我们讲到“跟周师傅学手艺”");
    expect(buildNextInterviewPrompt(project)).toContain("父亲当年工作的具体地点");

    project.pendingConfirmation = "";
    project.chapters.find((chapter) => chapter.status === "confirm")!.status = "completed";
    expect(buildNextInterviewPrompt(project)).toContain("第一次独立完成木器时的感受和后来影响");
  });

  it("does not invent progress for an empty biography", () => {
    expect(buildPreviousInterviewGuidance(getEmptyProject())).toContain("上次的内容已经保存好了");
  });
});
