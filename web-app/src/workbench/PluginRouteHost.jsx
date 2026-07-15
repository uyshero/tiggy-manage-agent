import React, { useEffect, useState } from "react";

import { PermissionDeniedError } from "./permissionService.js";

class PluginErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidUpdate(previousProps) {
    if (previousProps.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  componentDidCatch(error, info) {
    this.props.onError?.(error, info);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="plugin-route-state error" role="alert">
        <strong>插件页面暂时不可用</strong>
        <span>{this.state.error.message || String(this.state.error)}</span>
        <button type="button" onClick={() => this.setState({ error: null })}>重试</button>
      </div>
    );
  }
}

function PermissionGate({ route, children }) {
  const [state, setState] = useState({ status: "checking", error: null });

  useEffect(() => {
    let active = true;
    const required = route.requiredPermissions || [];
    route.context.permissions.require(required).then(() => {
      if (active) setState({ status: "allowed", error: null });
    }).catch((error) => {
      if (active) setState({ status: "denied", error });
    });
    return () => { active = false; };
  }, [route]);

  if (state.status === "checking") {
    return <div className="plugin-route-state"><span>正在检查访问权限...</span></div>;
  }
  if (state.status === "denied") {
    const denied = state.error instanceof PermissionDeniedError ? state.error.permissions.join("、") : "当前页面";
    return (
      <div className="plugin-route-state denied" role="alert">
        <strong>无权访问</strong>
        <span>缺少权限：{denied}</span>
      </div>
    );
  }
  return children;
}

export default function PluginRouteHost({ route, path, loading, onBack, onError }) {
  if (loading) {
    return <div className="plugin-route-state"><span>正在加载扩展...</span></div>;
  }
  if (!route) {
    return (
      <div className="plugin-route-state error" role="alert">
        <strong>扩展页面不存在或未启用</strong>
        <span>{path}</span>
        <button type="button" onClick={onBack}>返回工作台</button>
      </div>
    );
  }
  const Component = route.component;
  return (
    <section className="plugin-route-shell" aria-label={route.title || route.id}>
      <header className="plugin-route-header">
        <div>
          <span>扩展工作区</span>
          <strong>{route.title || route.id}</strong>
        </div>
        <button className="secondary" type="button" onClick={onBack}>返回工作台</button>
      </header>
      <div className="plugin-route-body">
        <PluginErrorBoundary resetKey={path} onError={onError}>
          <PermissionGate route={route}>
            <Component context={route.context} route={route} />
          </PermissionGate>
        </PluginErrorBoundary>
      </div>
    </section>
  );
}

export { PermissionGate, PluginErrorBoundary };
