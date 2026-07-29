export type ChapterStatus = "completed" | "confirm" | "collecting" | "not_started";
export type BookGoalType = "undecided" | "family_legacy" | "life_journey" | "era_witness" | "craft_legacy" | "literary_memoir" | "mixed";
export type NarrativeCoverageLevel = "missing" | "partial" | "sufficient";
export type InterviewOrder = "chronological" | "key_moments" | "custom";

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
  interviewOrder?: InterviewOrder;
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

export function openingPromptForInterviewOrder(order?: InterviewOrder): string {
  switch (order) {
    case "chronological":
      return "好，我们就从小时候慢慢讲起。我会边听边帮您留下重要的人、地方、感受和转折，您想跳到别的经历也可以。您最早记得的一个家里画面是什么？";
    case "key_moments":
      return "好，我们先讲您最想留给家人的重点故事。我会把其中的画面、感受和关系慢慢问清楚，之后再补齐其他部分。您最想从哪件事说起？";
    case "custom":
      return "好，顺序由您来定。我会帮您把每段经历整理好，之后需要时再串成完整的人生故事。现在最想先讲哪一段？";
    default:
      return openingInterviewPrompt;
  }
}

const bookGoalSignals = ["留给", "孩子", "子女", "孙", "家里人", "后人", "读完", "记住", "这本书"];
const factCheckSignals = ["确认", "上一章", "父亲", "记不清", "大概", "好像", "应该是", "地点"];

function cloneProject(project: BiographyProject): BiographyProject {
  return JSON.parse(JSON.stringify(project)) as BiographyProject;
}

export function getInitialProject(): BiographyProject {
  return getEmptyProject();
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
    chapters: [],
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

function chapterFromTranscript(transcript: string, sequence: number): Chapter {
  const compact = transcript.trim().replace(/\s+/gu, "");
  const excerpt = [...compact].slice(0, 14).join("") || "刚才这段经历";
  return {
    id: `chapter-${Date.now()}-${sequence}`,
    title: `关于“${excerpt}${compact.length > excerpt.length ? "…" : ""}”的故事`,
    status: "collecting",
    statusLabel: "讲述中",
    progress: 15,
    detail: "正在收集这段经历里的场景和感受",
    narrative: narrativeCoverage("partial", "missing", "missing", "missing", "missing", "missing", "missing"),
    nextFocus: "补充当时最清楚的一个画面和内心感受",
  };
}

export async function continueInterview(transcript: string, current: BiographyProject): Promise<InterviewReply> {
  await new Promise((resolve) => setTimeout(resolve, 650));
  const project = cloneProject(current);
  const collecting = project.chapters.find((chapter) => chapter.status === "collecting");
  if (collecting) {
    collecting.progress = Math.min(84, collecting.progress + 12);
    collecting.detail = "已经补充这段经历里的新细节";
  } else if (transcript.trim() && !needsBookGoalConfirmation(current)) {
    project.chapters.push(chapterFromTranscript(transcript, project.chapters.length + 1));
  }
  if (project.chapters.length > 0) project.overallProgress = Math.min(48, project.overallProgress + 3);

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
