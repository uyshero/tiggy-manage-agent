import React, { useState } from "react";

export const plugin = {
  id: "com.tma.workbench-diagnostics",
  activate(context) {
    context.commands.register("com.tma.workbench-diagnostics.refresh", async () => ({
      checkedAt: new Date().toISOString(),
      pluginID: context.plugin.id,
      workspaceID: context.scope.workspaceId
    }));
  }
};

function metric(label, value, detail) {
  return React.createElement("article", { className: "plugin-diagnostic-metric", key: label },
    React.createElement("span", null, label),
    React.createElement("strong", null, value),
    React.createElement("small", null, detail)
  );
}

export function DiagnosticsPage({ context }) {
  const [state, setState] = useState({ loading: false, checkedAt: "尚未检查", error: "" });

  async function refresh() {
    setState((current) => ({ ...current, loading: true, error: "" }));
    try {
      const result = await context.commands.execute("com.tma.workbench-diagnostics.refresh", {});
      const checkedAt = new Date(result.checkedAt).toLocaleString("zh-CN", { hour12: false });
      setState({ loading: false, checkedAt, error: "" });
      context.notifications.show({
        level: "success",
        title: "扩展状态已刷新",
        message: context.plugin.id,
        dedupeKey: "plugin.diagnostics.refresh"
      });
    } catch (error) {
      setState((current) => ({ ...current, loading: false, error: error.message || String(error) }));
    }
  }

  return React.createElement("div", { className: "plugin-diagnostics-page" },
    React.createElement("div", { className: "plugin-page-heading" },
      React.createElement("div", null,
        React.createElement("span", null, "Workbench Runtime"),
        React.createElement("h1", null, "扩展诊断"),
        React.createElement("p", null, context.plugin.id)
      ),
      React.createElement("button", { type: "button", disabled: state.loading, onClick: refresh }, state.loading ? "检查中..." : "刷新状态")
    ),
    React.createElement("div", { className: "plugin-diagnostic-grid" }, [
      metric("协议", "v1", context.plugin.version),
      metric("工作区", context.scope.workspaceId, context.scope.organizationId || "默认组织"),
      metric("终端", "4", "Desktop · Shell · Tablet · Mobile")
    ]),
    React.createElement("section", { className: "plugin-diagnostic-status" },
      React.createElement("h2", null, "运行状态"),
      React.createElement("dl", null,
        React.createElement("div", null, React.createElement("dt", null, "Manifest"), React.createElement("dd", null, "已校验")),
        React.createElement("div", null, React.createElement("dt", null, "Route Guard"), React.createElement("dd", null, "已通过")),
        React.createElement("div", null, React.createElement("dt", null, "最近检查"), React.createElement("dd", null, state.checkedAt))
      ),
      state.error ? React.createElement("div", { className: "plugin-inline-error", role: "alert" }, state.error) : null
    )
  );
}
