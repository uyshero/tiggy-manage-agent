import { describe, expect, it } from "vitest";
import { getEmptyProject, getInitialProject } from "./interview";
import { buildNextInterviewPrompt, buildPreviousInterviewGuidance, formatInterviewDuration, formatInterviewMoment } from "./interview-history";

describe("interview history", () => {
  it("formats the exact last interview time and duration", () => {
    expect(formatInterviewMoment(new Date(2026, 6, 28, 9, 5).getTime())).toBe("7月28日 09:05");
    expect(formatInterviewDuration(728)).toBe("12分08秒");
    expect(formatInterviewDuration(4_020)).toBe("1小时07分");
  });

  it("guides the narrator back to the chapter that was in progress", () => {
    const project = getInitialProject();
    expect(buildPreviousInterviewGuidance(project)).toContain("上次我们讲到“学木工的日子”");
    expect(buildNextInterviewPrompt(project)).toContain("父亲当年工作的具体地点");

    project.pendingConfirmation = "";
    project.chapters.find((chapter) => chapter.status === "confirm")!.status = "completed";
    expect(buildNextInterviewPrompt(project)).toContain("第一次独立完成木器时的感受和后来影响");
  });

  it("does not invent progress for an empty biography", () => {
    expect(buildPreviousInterviewGuidance(getEmptyProject())).toContain("上次的内容已经保存好了");
  });
});
