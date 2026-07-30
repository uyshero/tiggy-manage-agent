import { describe, expect, it } from "vitest";
import { continueInterview, getEmptyProject, getInitialProject, openingInterviewPrompt, openingPromptForInterviewOrder } from "./interview";

describe("interview opening", () => {
  it("introduces a professional biography interview before asking one question", () => {
    expect(openingInterviewPrompt).toContain("传记采访者");
    expect(openingInterviewPrompt).toContain("整理成一本书");
    expect(openingInterviewPrompt).toContain("画面、关系、感受、选择");
    expect(openingInterviewPrompt).toContain("先确认写给谁、最想留下什么");
    expect(openingInterviewPrompt).toContain("想停、补充或跳过");
    expect(openingInterviewPrompt.length).toBeLessThanOrEqual(140);
  });

  it("starts a new biography without a preset chapter outline", () => {
    expect(getInitialProject().chapters).toEqual([]);
    expect(getEmptyProject().chapters).toEqual([]);
    expect(getEmptyProject().interviewOrder).toBeUndefined();
  });

  it("uses the selected order for the first spoken question without blocking later additions", () => {
    expect(openingPromptForInterviewOrder("chronological")).toContain("从小到大");
    expect(openingPromptForInterviewOrder("chronological")).toContain("跳去别的回忆");
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
