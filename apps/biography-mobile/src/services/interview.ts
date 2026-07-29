export type ChapterStatus = "completed" | "confirm" | "collecting" | "not_started";
export type BookGoalType = "undecided" | "family_legacy" | "life_journey" | "era_witness" | "craft_legacy" | "literary_memoir" | "mixed";
export type NarrativeCoverageLevel = "missing" | "partial" | "sufficient";

export interface BiographyBookGoal {
  type: BookGoalType;
  audience: string;
  desiredImpact: string;
  confirmed: boolean;
}

export interface NarrativeCoverage {
  event: NarrativeCoverageLevel;
  scene: NarrativeCoverageLevel;
  emotion: NarrativeCoverageLevel;
  relationship: NarrativeCoverageLevel;
  choice: NarrativeCoverageLevel;
  impact: NarrativeCoverageLevel;
  reflection: NarrativeCoverageLevel;
}

export interface Chapter {
  id: string;
  title: string;
  status: ChapterStatus;
  statusLabel: string;
  progress: number;
  detail: string;
  narrative?: NarrativeCoverage;
  nextFocus?: string;
}

export interface BiographyProject {
  id: string;
  ownerName: string;
  title: string;
  bookGoal?: BiographyBookGoal;
  overallProgress: number;
  completedChapterCount: number;
  chapters: Chapter[];
  pendingConfirmation: string;
}

export interface InterviewReply {
  text: string;
  expression: string;
  project: BiographyProject;
}

export const openingInterviewPrompt = "您好，我是陪您整理人生故事的传记采访者。接下来我会像做人物专访一样，一次只问一个问题，帮您核对时间、地点、人物，也会多听当时的感受。您想停、想补充、想跳过都可以。我们先从这本书开始：您最想留给谁看，希望他们记住什么？";

const bookGoalSignals = ["留给", "孩子", "子女", "孙", "家里人", "后人", "读完", "记住", "这本书"];
const factCheckSignals = ["确认", "上一章", "父亲", "记不清", "大概", "好像", "应该是", "地点"];

function cloneProject(project: BiographyProject): BiographyProject {
  return JSON.parse(JSON.stringify(project)) as BiographyProject;
}

const initialProject: BiographyProject = {
  id: "bio_demo",
  ownerName: "王叔",
  title: "我的人生故事",
  bookGoal: { type: "family_legacy", audience: "子女和孙辈", desiredImpact: "记住家里怎样一步步走到今天", confirmed: true },
  overallProgress: 32,
  completedChapterCount: 1,
  pendingConfirmation: "父亲当年工作的具体地点",
  chapters: [
    { id: "childhood", title: "童年往事", status: "completed", statusLabel: "已完成", progress: 100, detail: "已经确认并整理成稿", narrative: narrativeCoverage("sufficient", "sufficient", "sufficient", "sufficient", "partial", "sufficient", "sufficient"), nextFocus: "有新回忆时再补充童年里的重要选择" },
    { id: "school", title: "求学岁月", status: "confirm", statusLabel: "待确认", progress: 72, detail: "还有 2 处细节需要确认", narrative: narrativeCoverage("sufficient", "partial", "partial", "partial", "missing", "partial", "missing"), nextFocus: "确认父亲工作的地点，再谈那段经历后来带来的影响" },
    { id: "craft", title: "学木工的日子", status: "collecting", statusLabel: "讲述中", progress: 46, detail: "正在收集师傅和第一次工作的故事", narrative: narrativeCoverage("partial", "partial", "partial", "sufficient", "partial", "missing", "missing"), nextFocus: "补充第一次独立完成木器时的感受和后来影响" },
    { id: "family", title: "成家以后", status: "not_started", statusLabel: "未开始", progress: 0, detail: "还没有开始讲述", narrative: missingNarrativeCoverage(), nextFocus: "等待开始讲述家庭生活中的重要记忆" },
  ],
};

export function getInitialProject(): BiographyProject {
  return cloneProject(initialProject);
}

export function getEmptyProject(): BiographyProject {
  return {
    id: "biography_new",
    ownerName: "",
    title: "我的人生故事",
    bookGoal: { type: "undecided", audience: "", desiredImpact: "", confirmed: false },
    overallProgress: 0,
    completedChapterCount: 0,
    pendingConfirmation: "",
    chapters: [
      emptyChapter("childhood", "童年往事"),
      emptyChapter("school", "求学岁月"),
      emptyChapter("work", "工作岁月"),
      emptyChapter("family", "家庭生活"),
    ],
  };
}

function narrativeCoverage(
  event: NarrativeCoverageLevel,
  scene: NarrativeCoverageLevel,
  emotion: NarrativeCoverageLevel,
  relationship: NarrativeCoverageLevel,
  choice: NarrativeCoverageLevel,
  impact: NarrativeCoverageLevel,
  reflection: NarrativeCoverageLevel,
): NarrativeCoverage {
  return { event, scene, emotion, relationship, choice, impact, reflection };
}

function missingNarrativeCoverage(): NarrativeCoverage {
  return narrativeCoverage("missing", "missing", "missing", "missing", "missing", "missing", "missing");
}

function emptyChapter(id: string, title: string): Chapter {
  return {
    id,
    title,
    status: "not_started",
    statusLabel: "未开始",
    progress: 0,
    detail: "等待您慢慢讲述",
    narrative: missingNarrativeCoverage(),
    nextFocus: "等待开始讲述这一阶段最想留下的记忆",
  };
}

export async function continueInterview(transcript: string, current: BiographyProject): Promise<InterviewReply> {
  await new Promise((resolve) => setTimeout(resolve, 650));
  const project = cloneProject(current);
  const craft = project.chapters.find((chapter) => chapter.id === "craft");
  if (craft) {
    craft.progress = Math.min(84, craft.progress + 12);
    craft.detail = "已经补充第一次离家和师傅的故事";
  }
  project.overallProgress = Math.min(48, project.overallProgress + 3);

  if (needsBookGoalConfirmation(current) && includesAny(transcript, bookGoalSignals)) {
    return {
      text: "这个方向很珍贵，是想让后人听见您亲口讲过的一生。那您最希望他们读到哪一段经历时，明白您是怎样一个人？",
      expression: "郑重、亲切，带肯定感，语速稍慢，句间停顿自然",
      project,
    };
  }

  if (includesAny(transcript, factCheckSignals)) {
    return {
      text: "好，这个细节先不急着定死，写成书时可以留得更稳。您还记得父亲工作的地方，附近有什么明显地名或建筑吗？",
      expression: "温和、清晰地确认事实，语速稍慢，不要催促",
      project,
    };
  }

  if (transcript.includes("师傅")) {
    return {
      text: "您把周师傅的严格和教会您的东西放在一起讲，这段关系很有分量。那时候您心里最强烈的感受是什么？",
      expression: "温暖、有兴趣，具体承接刚才内容后轻轻追问，语速稍慢",
      project,
    };
  }

  if (transcript.includes("十九岁") || transcript.includes("离开家") || transcript.includes("上海")) {
    return {
      text: "第一次离家去上海，这会是书里很有画面的一段。您还记得出门那天，家里或路上哪个画面最清楚吗？",
      expression: "真诚、共情，像认真听见了一个人生转折，语速稍慢",
      project,
    };
  }

  return {
    text: "您刚才讲的这段很重要，我先记下。回到那个时候，您心里最放不下的人或事是什么？",
    expression: "温和、肯定、感同身受，语速稍慢，停顿自然",
    project,
  };
}

function needsBookGoalConfirmation(project: BiographyProject): boolean {
  const goal = project.bookGoal;
  return !goal?.confirmed || !goal.audience.trim() || !goal.desiredImpact.trim();
}

function includesAny(text: string, signals: string[]): boolean {
  return signals.some((signal) => text.includes(signal));
}
