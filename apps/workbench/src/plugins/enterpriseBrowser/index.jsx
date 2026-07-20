import React, { useEffect, useMemo, useRef, useState } from "react";

import { createBrowserAPI } from "./browserApi.js";
import "./styles.css";

function requestedSessionID() {
  const query = String(window.location.hash || "").split("?", 2)[1] || "";
  return new URLSearchParams(query).get("session") || "";
}

function taskLabel(task) {
  return task.title || task.name || task.id;
}

function IconButton({ label, children, ...props }) {
  return <button aria-label={label} className="enterprise-browser-icon" title={label} type="button" {...props}>{children}</button>;
}

export const plugin = {
  id: "com.tma.enterprise-browser",
  activate() {}
};

export function EnterpriseBrowserPage({ context }) {
  const api = useMemo(() => createBrowserAPI(context.http), [context]);
  const [tasks, setTasks] = useState([]);
  const [selectedTaskID, setSelectedTaskID] = useState(requestedSessionID);
  const [browserSessionID, setBrowserSessionID] = useState("");
  const [state, setState] = useState(null);
  const [address, setAddress] = useState("https://example.com");
  const [inputText, setInputText] = useState("");
  const [frameRevision, setFrameRevision] = useState(0);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const viewportRef = useRef(null);

  useEffect(() => {
    let active = true;
    context.tasks.list({ limit: 100, includeArchived: false }).then((items) => {
      if (!active) return;
      setTasks(items);
      setSelectedTaskID((current) => current || items[0]?.id || "");
    }).catch((reason) => active && setError(String(reason?.message || reason)));
    return () => { active = false; };
  }, [context]);

  useEffect(() => {
    if (!browserSessionID) return undefined;
    let active = true;
    const refresh = () => {
      api.read(browserSessionID).then((next) => {
        if (!active) return;
        setState(next);
        if (next.url && document.activeElement?.name !== "browser-address") setAddress(next.url);
      }).catch((reason) => active && setError(String(reason?.message || reason)));
    };
    refresh();
    const stateTimer = window.setInterval(refresh, 1500);
    const frameTimer = window.setInterval(() => setFrameRevision(Date.now()), 800);
    return () => {
      active = false;
      window.clearInterval(stateTimer);
      window.clearInterval(frameTimer);
    };
  }, [api, browserSessionID]);

  async function run(name, operation) {
    setBusy(name);
    setError("");
    try {
      const next = await operation();
      if (next?.browser_session_id) setBrowserSessionID(next.browser_session_id);
      if (next?.url !== undefined) {
        setState(next);
        setAddress(next.url || address);
      }
      setFrameRevision(Date.now());
      return next;
    } catch (reason) {
      setError(String(reason?.message || reason));
      return null;
    } finally {
      setBusy("");
    }
  }

  async function connect() {
    if (!selectedTaskID) return;
    await run("connect", () => api.create({
      browser_session_id: selectedTaskID,
      tma_session_id: selectedTaskID,
      url: address,
      viewport: { width: 1280, height: 720 }
    }));
  }

  async function navigate(event) {
    event.preventDefault();
    if (!browserSessionID || !address.trim()) return;
    await run("open", () => api.action(browserSessionID, "open", { url: address.trim() }));
  }

  async function pageAction(action, input = {}) {
    if (!browserSessionID) return;
    await run(action, () => api.action(browserSessionID, action, input));
  }

  function clickPage(event) {
    if (!state?.viewport || !viewportRef.current) return;
    const bounds = viewportRef.current.getBoundingClientRect();
    const x = Math.max(0, Math.min(state.viewport.width, (event.clientX - bounds.left) / bounds.width * state.viewport.width));
    const y = Math.max(0, Math.min(state.viewport.height, (event.clientY - bounds.top) / bounds.height * state.viewport.height));
    viewportRef.current.focus();
    pageAction("mouse", { x, y });
  }

  function keyPage(event) {
    const supported = new Set(["Enter", "Tab", "Backspace", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "PageUp", "PageDown"]);
    if (!supported.has(event.key)) return;
    event.preventDefault();
    pageAction("key", { key: event.key });
  }

  async function sendText(event) {
    event.preventDefault();
    if (!inputText) return;
    await pageAction("insert_text", { text: inputText });
    setInputText("");
    viewportRef.current?.focus();
  }

  async function closeBrowser() {
    if (!browserSessionID) return;
    await run("close", () => api.close(browserSessionID));
    setBrowserSessionID("");
    setState(null);
  }

  const connected = Boolean(browserSessionID);
  return (
    <main className="enterprise-browser-page">
      <header className="enterprise-browser-header">
        <div>
          <h1>浏览器</h1>
          <span>{connected ? `${state?.title || "正在连接"} · ${browserSessionID}` : "选择任务并连接隔离浏览器会话"}</span>
        </div>
        <div className={`enterprise-browser-status ${connected ? "connected" : ""}`}>
          <span aria-hidden="true" />{connected ? "已连接" : "未连接"}
        </div>
      </header>

      <section className="enterprise-browser-toolbar" aria-label="浏览器工具栏">
        <select aria-label="关联任务" disabled={connected || busy} onChange={(event) => setSelectedTaskID(event.target.value)} value={selectedTaskID}>
          <option value="">选择任务</option>
          {tasks.map((task) => <option key={task.id} value={task.id}>{taskLabel(task)}</option>)}
        </select>
        {!connected ? (
          <button className="enterprise-browser-connect" disabled={!selectedTaskID || busy} onClick={connect} type="button">连接</button>
        ) : (
          <>
            <IconButton disabled={Boolean(busy)} label="后退" onClick={() => pageAction("back")}>←</IconButton>
            <IconButton disabled={Boolean(busy)} label="前进" onClick={() => pageAction("forward")}>→</IconButton>
            <IconButton disabled={Boolean(busy)} label="刷新" onClick={() => pageAction("reload")}>↻</IconButton>
            <form className="enterprise-browser-address" onSubmit={navigate}>
              <input aria-label="网页地址" name="browser-address" onChange={(event) => setAddress(event.target.value)} spellCheck="false" value={address} />
            </form>
            <IconButton disabled={Boolean(busy)} label="关闭浏览器" onClick={closeBrowser}>×</IconButton>
          </>
        )}
      </section>

      {error ? <div className="enterprise-browser-error" role="alert">{error}</div> : null}

      <section className="enterprise-browser-surface">
        {connected ? (
          <div
            aria-label="浏览器页面，点击以操作"
            className="enterprise-browser-viewport"
            onClick={clickPage}
            onKeyDown={keyPage}
            onWheel={(event) => pageAction("wheel", { delta_x: event.deltaX, delta_y: event.deltaY })}
            ref={viewportRef}
            role="application"
            tabIndex={0}
          >
            <img alt={state?.title || "浏览器页面"} draggable="false" src={api.frameURL(browserSessionID, frameRevision)} />
            {busy ? <div className="enterprise-browser-busy">正在处理...</div> : null}
          </div>
        ) : (
          <div className="enterprise-browser-empty">没有活动浏览器会话</div>
        )}
      </section>

      {connected ? (
        <form className="enterprise-browser-input" onSubmit={sendText}>
          <input aria-label="向当前页面输入文本" onChange={(event) => setInputText(event.target.value)} placeholder="向当前焦点输入文本" value={inputText} />
          <button disabled={!inputText || Boolean(busy)} type="submit">输入</button>
        </form>
      ) : null}
    </main>
  );
}
