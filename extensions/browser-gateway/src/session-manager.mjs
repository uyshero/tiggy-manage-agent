import { createHash, randomUUID } from "node:crypto";
import { mkdir } from "node:fs/promises";
import path from "node:path";

function positiveInteger(value, fallback, maximum) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) return fallback;
  return Math.min(parsed, maximum);
}

function requestedViewport(input = {}) {
  return {
    width: positiveInteger(input.width, 1280, 2560),
    height: positiveInteger(input.height, 720, 1600)
  };
}

export function safeSessionID(value) {
  const normalized = String(value || "").trim();
  if (!normalized) return `brs_${randomUUID().replaceAll("-", "")}`;
  if (!/^[A-Za-z0-9._:-]{1,160}$/.test(normalized)) throw Object.assign(new Error("invalid browser session id"), { statusCode: 400 });
  return normalized;
}

export function isolationDigest(scope, sessionID) {
  return createHash("sha256")
    .update([scope.workspaceID, scope.ownerID, sessionID].join("\0"))
    .digest("hex")
    .slice(0, 32);
}

function sessionKey(workspaceID, sessionID) {
  return `${workspaceID}\0${sessionID}`;
}

function normalizeText(value, limit) {
  return String(value || "").replace(/\s+/g, " ").trim().slice(0, limit);
}

export class BrowserSessionManager {
  constructor(options) {
    this.chromium = options.chromium;
    this.profileRoot = options.profileRoot;
    this.executablePath = options.executablePath || "";
    this.maxSessionsPerWorkspace = positiveInteger(options.maxSessionsPerWorkspace, 4, 1000);
    this.idleTTLMS = positiveInteger(options.idleTTLMS, 300000, 86400000);
    this.sessions = new Map();
    this.cleanupTimer = setInterval(() => this.cleanupExpired().catch(() => {}), Math.min(this.idleTTLMS, 60000));
    this.cleanupTimer.unref?.();
  }

  countForWorkspace(workspaceID) {
    let count = 0;
    for (const session of this.sessions.values()) if (session.workspaceID === workspaceID) count += 1;
    return count;
  }

  async create(scope, input = {}) {
    const id = safeSessionID(input.browser_session_id);
    const key = sessionKey(scope.workspaceID, id);
    const existing = this.sessions.get(key);
    if (existing) {
      await this.assertAccess(scope, existing);
      existing.lastUsedAt = Date.now();
      return this.observe(existing);
    }
    if (this.countForWorkspace(scope.workspaceID) >= this.maxSessionsPerWorkspace) {
      throw Object.assign(new Error("workspace browser session quota exceeded"), { statusCode: 429 });
    }

    const tmaSessionID = String(input.tma_session_id || scope.tmaSessionID || "").trim();
    const profileScope = {
      workspaceID: scope.workspaceID,
      ownerID: tmaSessionID ? `session:${tmaSessionID}` : scope.ownerID
    };
    const profilePath = path.join(this.profileRoot, isolationDigest(profileScope, id));
    await mkdir(profilePath, { recursive: true, mode: 0o700 });
    const viewport = requestedViewport(input.viewport);
    const launchOptions = {
      headless: true,
      viewport,
      args: ["--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check"]
    };
    if (this.executablePath) launchOptions.executablePath = this.executablePath;
    const context = await this.chromium.launchPersistentContext(profilePath, launchOptions);
    const page = context.pages()[0] || await context.newPage();
    page.setDefaultTimeout(15000);
    const session = {
      id,
      key,
      workspaceID: scope.workspaceID,
      ownerID: scope.ownerID,
      tmaSessionID,
      context,
      page,
      viewport,
      refs: new Map(),
      queue: Promise.resolve(),
      revision: 0,
      createdAt: Date.now(),
      lastUsedAt: Date.now()
    };
    this.sessions.set(key, session);
    try {
      if (input.url) await page.goto(String(input.url), { waitUntil: "domcontentloaded" });
      return await this.observe(session);
    } catch (error) {
      this.sessions.delete(key);
      await context.close().catch(() => {});
      throw error;
    }
  }

  async assertAccess(scope, session) {
    if (scope.workspaceID !== session.workspaceID) throw Object.assign(new Error("browser session not found"), { statusCode: 404 });
    if (scope.kind === "service") {
      if (!session.tmaSessionID || session.tmaSessionID !== scope.tmaSessionID) {
        throw Object.assign(new Error("browser session is owned by another TMA session"), { statusCode: 403 });
      }
      return;
    }
    if (session.ownerID !== scope.ownerID && !session.tmaSessionID) {
      throw Object.assign(new Error("browser session is owned by another user"), { statusCode: 403 });
    }
  }

  async get(scope, id) {
    const session = this.sessions.get(sessionKey(scope.workspaceID, id));
    if (!session) throw Object.assign(new Error("browser session not found"), { statusCode: 404 });
    await this.assertAccess(scope, session);
    return session;
  }

  async run(scope, id, operation) {
    const session = await this.get(scope, id);
    const queued = session.queue.then(async () => {
      session.lastUsedAt = Date.now();
      const result = await operation(session);
      session.revision += 1;
      return result;
    });
    session.queue = queued.catch(() => {});
    return queued;
  }

  async observe(session) {
    const page = session.page;
    const observed = await page.evaluate(() => {
      const nodes = Array.from(document.querySelectorAll("a,button,input,textarea,select,[role=button],[contenteditable=true],[tabindex]"))
        .filter((element) => {
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
        })
        .slice(0, 80);
      const selectorFor = (element) => {
        if (element.id) return `#${CSS.escape(element.id)}`;
        const parts = [];
        let current = element;
        while (current && current.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
          let part = current.tagName.toLowerCase();
          const parent = current.parentElement;
          if (parent) {
            const siblings = Array.from(parent.children).filter((child) => child.tagName === current.tagName);
            if (siblings.length > 1) part += `:nth-of-type(${siblings.indexOf(current) + 1})`;
          }
          parts.unshift(part);
          current = parent;
        }
        return parts.join(" > ");
      };
      return {
        text: document.body?.innerText || "",
        elements: nodes.map((element, index) => ({
          ref: `e${index + 1}`,
          role: element.getAttribute("role") || element.tagName.toLowerCase(),
          text: element.innerText || element.getAttribute("aria-label") || element.getAttribute("placeholder") || element.value || "",
          selector: selectorFor(element),
          tag: element.tagName.toLowerCase()
        }))
      };
    }).catch(() => ({ text: "", elements: [] }));
    session.refs = new Map(observed.elements.map((element) => [element.ref, element.selector]));
    return {
      browser_session_id: session.id,
      tma_session_id: session.tmaSessionID,
      url: page.url(),
      title: await page.title().catch(() => ""),
      text: normalizeText(observed.text, 6000),
      elements: observed.elements.map((element) => ({ ...element, text: normalizeText(element.text, 160) })),
      viewport: session.viewport,
      revision: session.revision,
      created_at: new Date(session.createdAt).toISOString(),
      last_used_at: new Date(session.lastUsedAt).toISOString()
    };
  }

  selector(session, input) {
    const selector = String(input.selector || session.refs.get(String(input.ref || "")) || "").trim();
    if (!selector) throw Object.assign(new Error("selector or valid ref is required"), { statusCode: 400 });
    return selector;
  }

  async action(scope, id, action, input = {}) {
    return this.run(scope, id, async (session) => {
      const page = session.page;
      if (action === "read") return this.observe(session);
      if (action === "open") await page.goto(String(input.url || "about:blank"), { waitUntil: "domcontentloaded" });
      else if (action === "back") await page.goBack({ waitUntil: "domcontentloaded" }).catch(() => null);
      else if (action === "forward") await page.goForward({ waitUntil: "domcontentloaded" }).catch(() => null);
      else if (action === "reload") await page.reload({ waitUntil: "domcontentloaded" });
      else if (action === "click") await page.locator(this.selector(session, input)).click();
      else if (action === "type") {
        const locator = page.locator(this.selector(session, input));
        if (input.clear !== false) await locator.fill("");
        await locator.fill(String(input.text || ""));
      } else if (action === "mouse") {
        await page.mouse.click(Number(input.x), Number(input.y));
      } else if (action === "wheel") {
        await page.mouse.wheel(Number(input.delta_x || 0), Number(input.delta_y || 0));
      } else if (action === "key") {
        await page.keyboard.press(String(input.key || ""));
      } else if (action === "insert_text") {
        await page.keyboard.insertText(String(input.text || ""));
      } else {
        throw Object.assign(new Error(`unsupported browser action ${action}`), { statusCode: 404 });
      }
      return this.observe(session);
    });
  }

  async screenshot(scope, id, options = {}) {
    return this.run(scope, id, (session) => session.page.screenshot({
      type: "jpeg",
      quality: positiveInteger(options.quality, 72, 90),
      fullPage: options.full_page === true
    }));
  }

  async close(scope, id) {
    const session = await this.get(scope, id);
    this.sessions.delete(session.key);
    await session.queue.catch(() => {});
    await session.context.close().catch(() => {});
    return { browser_session_id: id, closed: true };
  }

  async cleanupExpired() {
    const cutoff = Date.now() - this.idleTTLMS;
    const expired = [...this.sessions.values()].filter((session) => session.lastUsedAt < cutoff);
    await Promise.all(expired.map((session) => this.close({
      kind: "user",
      workspaceID: session.workspaceID,
      ownerID: session.ownerID
    }, session.id).catch(() => {})));
  }

  async shutdown() {
    clearInterval(this.cleanupTimer);
    await Promise.all([...this.sessions.values()].map((session) => session.context.close().catch(() => {})));
    this.sessions.clear();
  }
}
