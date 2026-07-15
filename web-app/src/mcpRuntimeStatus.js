const stateMeta = Object.freeze({
  open: { label: "已熔断", rank: 4 },
  half_open: { label: "恢复探测", rank: 3 },
  saturated: { label: "并发已满", rank: 2 },
  closed: { label: "正常", rank: 1 },
  untracked: { label: "未运行", rank: 0 }
});

const failureLabels = Object.freeze({
  authentication: "认证失败",
  canceled: "调用取消",
  protocol: "协议错误",
  rate_limited: "远端限流",
  timeout: "调用超时",
  transport: "传输失败",
  unavailable: "服务不可用",
  unknown: "未知失败"
});

export function groupMCPRuntimeStates(states = []) {
  return states.reduce((grouped, state) => {
    const serverID = String(state?.server_id || "").trim();
    if (!serverID || !stateMeta[state?.state]) return grouped;
    (grouped[serverID] ||= []).push(state);
    return grouped;
  }, {});
}

export function summarizeMCPRuntimeStates(states = []) {
  const normalized = states.filter((state) => stateMeta[state?.state]);
  if (!normalized.length) return { state: "untracked", label: stateMeta.untracked.label, count: 0 };
  const worst = normalized.reduce((current, state) => stateMeta[state.state].rank > stateMeta[current.state].rank ? state : current);
  return {
    state: worst.state,
    label: stateMeta[worst.state].label,
    count: normalized.length
  };
}

export function mcpRuntimeStateLabel(state) {
  return stateMeta[state]?.label || stateMeta.untracked.label;
}

export function mcpRuntimeFailureLabel(failureClass) {
  return failureLabels[failureClass] || "";
}
