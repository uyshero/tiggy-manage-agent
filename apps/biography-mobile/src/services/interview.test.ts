import { describe, expect, it } from "vitest";
import { continueInterview, getEmptyProject, getInitialProject, openingInterviewPrompt, openingPromptForInterviewOrder } from "./interview";

describe("interview opening", () => {
  it("introduces a professional biography interview before asking one question", () => {
    expect(openingInterviewPrompt).toContain("传记采访者");
    expect(openingInterviewPrompt).toContain("人物专访");
    expect(openingInterviewPrompt).toContain("核对时间、地点、人物");
    expect(openingInterviewPrompt).toContain("当时的感受");
    expect(openingInterviewPrompt).toContain("最想留给谁看");
    expect(openingInterviewPrompt).toContain("记住什么");
    expect(openingInterviewPrompt).toContain("想停");
    expect(openingInterviewPrompt.length).toBeLessThanOrEqual(120);
    expect(openingInterviewPrompt.endsWith("？")).toBe(true);
  });

  it("starts a new biography without a preset chapter outline", () => {
    expect(getInitialProject().chapters).toEqual([]);
    expect(getEmptyProject().chapters).toEqual([]);
    expect(getEmptyProject().interviewOrder).toBeUndefined();
  });

  it("uses the selected order for the first spoken question without blocking later additions", () => {
    expect(openingPromptForInterviewOrder("chronological")).toContain("小时候");
    expect(openingPromptForInterviewOrder("chronological")).toContain("跳到别的经历");
    expect(openingPromptForInterviewOrder("key_moments")).toContain("重点故事");
    expect(openingPromptForInterviewOrder("custom")).toContain("顺序由您来定");
  });

  it("answers with empathy before asking about feelings", async () => {
    const reply = await continueInterview("师傅姓周，对我很严格。", getInitialProject());

    expect(reply.text).toContain("这段关系很有分量");
    expect(reply.text).toContain("感受");
    expect(reply.expression).toContain("具体承接");
  });

  it("uses the book goal before choosing the next memory", async () => {
    const reply = await continueInterview("这本书我想留给孩子看，希望他们记住家里怎么走过来的。", getEmptyProject());

    expect(reply.text).toContain("后人");
    expect(reply.text).toContain("怎样一个人");
    expect(reply.text.endsWith("？")).toBe(true);
  });
});
