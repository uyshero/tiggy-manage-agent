export const marketplaceEntryStages = [
  { status: "draft", label: "草稿" },
  { status: "pending_review", label: "待审核" },
  { status: "published", label: "已发布" },
  { status: "withdrawn", label: "已下架" }
];

export function marketplaceEntryStatusLabel(status) {
  return marketplaceEntryStages.find((stage) => stage.status === status)?.label || status || "未知";
}

export function marketplaceEntryStatusTone(status) {
  switch (status) {
    case "published":
      return "ok";
    case "pending_review":
      return "warn";
    case "withdrawn":
      return "danger";
    default:
      return "neutral";
  }
}

export function marketplaceEntryNextAction(status) {
  switch (status) {
    case "draft":
      return { action: "submit", label: "提交审核", targetStatus: "pending_review" };
    case "pending_review":
      return { action: "publish", label: "审核并发布", targetStatus: "published" };
    case "published":
      return { action: "withdraw", label: "下架", targetStatus: "withdrawn" };
    default:
      return null;
  }
}

export function marketplaceEntryStageState(currentStatus, stageStatus) {
  const currentIndex = marketplaceEntryStages.findIndex((stage) => stage.status === currentStatus);
  const stageIndex = marketplaceEntryStages.findIndex((stage) => stage.status === stageStatus);
  if (currentIndex < 0 || stageIndex < 0) return "upcoming";
  if (stageIndex === currentIndex) return "current";
  return stageIndex < currentIndex ? "complete" : "upcoming";
}

export function marketplaceInstallStateMeta(state) {
  switch (state) {
    case "new_install":
      return { label: "可安装", tone: "ok", actionLabel: "安全预览" };
    case "upgrade":
      return { label: "有新版本", tone: "warn", actionLabel: "查看更新" };
    case "unchanged":
      return { label: "已安装", tone: "neutral", actionLabel: "查看详情" };
    case "blocked":
      return { label: "不可安装", tone: "danger", actionLabel: "查看原因" };
    default:
      return { label: "内部已发布", tone: "ok", actionLabel: "安全预览" };
  }
}

export function marketplaceUpgradeVersions(preview) {
  if (preview?.install_state !== "upgrade") return null;
  const current = Number(preview?.existing?.version || 0);
  if (!Number.isInteger(current) || current < 1) return null;
  return { current, target: current + 1 };
}
