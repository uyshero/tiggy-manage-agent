import type { BiographyProject } from "@/services/interview";

export interface LastInterviewSession {
  projectID: string;
  endedAt: number;
  durationSeconds: number;
}

const storageKey = "tma.biography.last_interview_session";

export function loadLastInterviewSession(): LastInterviewSession | null {
  try {
    const value = uni.getStorageSync(storageKey) as LastInterviewSession | string | null;
    const parsed = typeof value === "string" ? JSON.parse(value) as LastInterviewSession : value;
    if (!parsed || !parsed.projectID || !Number.isFinite(parsed.endedAt) || !Number.isFinite(parsed.durationSeconds)) return null;
    return { ...parsed, durationSeconds: Math.max(0, Math.round(parsed.durationSeconds)) };
  } catch {
    return null;
  }
}

export function saveLastInterviewSession(session: LastInterviewSession): void {
  try {
    uni.setStorageSync(storageKey, session);
  } catch {
    // Interviewing must continue even when local metadata cannot be cached.
  }
}

export function formatInterviewMoment(timestamp: number): string {
  const date = new Date(timestamp);
  return `${date.getMonth() + 1}月${date.getDate()}日 ${date.getHours().toString().padStart(2, "0")}:${date.getMinutes().toString().padStart(2, "0")}`;
}

export function formatInterviewDuration(durationSeconds: number): string {
  const seconds = Math.max(0, Math.round(durationSeconds));
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor(seconds % 3_600 / 60);
  const remainingSeconds = seconds % 60;
  if (hours > 0) return `${hours}小时${minutes.toString().padStart(2, "0")}分`;
  return `${minutes}分${remainingSeconds.toString().padStart(2, "0")}秒`;
}

export function buildPreviousInterviewGuidance(project: BiographyProject): string {
  const collecting = project.chapters.find((chapter) => chapter.status === "collecting");
  if (collecting) {
    return `上次我们讲到“${collecting.title}”，${withoutTerminalPunctuation(collecting.detail)}。这次可以从这里接着讲，也可以补充刚刚想到的内容。`;
  }
  const confirming = project.chapters.find((chapter) => chapter.status === "confirm");
  if (confirming) {
    const confirmation = project.pendingConfirmation.trim();
    return confirmation
      ? `上次我们整理到“${confirming.title}”，还有一个细节想和您确认：${withoutTerminalPunctuation(confirmation)}。您也可以先继续讲别的。`
      : `上次我们整理到“${confirming.title}”，还有一些细节需要确认。您想从这里继续，还是先讲别的经历？`;
  }
  if (project.completedChapterCount > 0) {
    const next = project.chapters.find((chapter) => chapter.status === "not_started");
    return next
      ? `上次已经整理完成 ${project.completedChapterCount} 章。这次可以从“${next.title}”继续，也可以回到前面的章节补充。`
      : `上次已经整理完成 ${project.completedChapterCount} 章。这次可以回到任何一章，补充您新想起来的内容。`;
  }
  return "上次的内容已经保存好了。今天可以接着讲，也可以先确认以前的内容。";
}

export function buildNextInterviewPrompt(project: BiographyProject): string {
  const confirming = project.chapters.find((chapter) => chapter.status === "confirm");
  if (confirming && project.pendingConfirmation.trim()) {
    return `确认“${confirming.title}”里的${project.pendingConfirmation.trim()}，或者继续讲新的故事`;
  }
  const collecting = project.chapters.find((chapter) => chapter.status === "collecting");
  if (collecting) {
    return collecting.nextFocus?.trim()
      ? `${withoutTerminalPunctuation(collecting.nextFocus)}，或者回看以前的内容`
      : `继续讲“${collecting.title}”，或者回看以前的内容`;
  }
  const next = project.chapters.find((chapter) => chapter.status === "not_started");
  if (next && project.completedChapterCount > 0) return `从“${next.title}”继续，或者补充已经完成的章节`;
  return "继续讲新故事，或者核对以前的内容";
}

function withoutTerminalPunctuation(value: string): string {
  return value.trim().replace(/[。！？；]+$/u, "");
}
