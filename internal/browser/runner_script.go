package browser

const playwrightRunnerScript = `
(async () => {
  const fs = require('fs');
  const http = require('http');
  const path = require('path');
  const { spawn } = require('child_process');

  function readStdin() {
    return new Promise((resolve, reject) => {
      let data = '';
      process.stdin.setEncoding('utf8');
      process.stdin.on('data', chunk => data += chunk);
      process.stdin.on('end', () => resolve(data));
      process.stdin.on('error', reject);
    });
  }

  async function loadPlaywright() {
    try {
      return require('playwright');
    } catch (_) {}
    try {
      return require('playwright-core');
    } catch (_) {}
    try {
      return await import('playwright');
    } catch (_) {
      return await import('playwright-core');
    }
  }

  function mkdirp(filePath) {
    if (!filePath) return;
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
  }

  function sleep(ms) {
    return new Promise(resolve => setTimeout(resolve, ms));
  }

  function readJSON(url, timeout = 1000) {
    return new Promise((resolve, reject) => {
      const request = http.get(url, { timeout }, response => {
        let data = '';
        response.setEncoding('utf8');
        response.on('data', chunk => data += chunk);
        response.on('end', () => {
          if (response.statusCode < 200 || response.statusCode >= 300) {
            reject(new Error('http ' + response.statusCode + ' from ' + url));
            return;
          }
          try {
            resolve(JSON.parse(data || '{}'));
          } catch (err) {
            reject(err);
          }
        });
      });
      request.on('timeout', () => request.destroy(new Error('timeout reading ' + url)));
      request.on('error', reject);
    });
  }

  async function endpointAlive(endpoint) {
    if (!endpoint) return false;
    try {
      await readJSON(endpoint.replace(/\/$/, '') + '/json/version');
      return true;
    } catch (_) {
      return false;
    }
  }

  async function waitForDevToolsEndpoint(userDataDir, deadlineMs) {
    const activePortPath = path.join(userDataDir, 'DevToolsActivePort');
    const deadline = Date.now() + deadlineMs;
    while (Date.now() < deadline) {
      try {
        const lines = fs.readFileSync(activePortPath, 'utf8').trim().split(/\r?\n/);
        const port = Number(lines[0]);
        if (port > 0) {
          const endpoint = 'http://127.0.0.1:' + port;
          if (await endpointAlive(endpoint)) return endpoint;
        }
      } catch (_) {}
      await sleep(100);
    }
    throw new Error('timed out waiting for Chromium DevTools endpoint');
  }

  async function persistentEndpoint(input, state, playwright) {
    if (await endpointAlive(state.cdp_endpoint)) return state.cdp_endpoint;
    const sessionDir = input.state_path ? path.dirname(input.state_path) : path.join(require('os').tmpdir(), 'tma-browser', 'anonymous');
    fs.mkdirSync(sessionDir, { recursive: true });
    const userDataDir = path.join(sessionDir, 'profile');
    fs.mkdirSync(userDataDir, { recursive: true });
    try { fs.unlinkSync(path.join(userDataDir, 'DevToolsActivePort')); } catch (_) {}

    let executablePath = process.env.TMA_BROWSER_EXECUTABLE_PATH || '';
    if (!executablePath && playwright.chromium.executablePath) {
      executablePath = playwright.chromium.executablePath();
    }
    if (!executablePath) {
      throw new Error('persistent browser requires TMA_BROWSER_EXECUTABLE_PATH or a Playwright bundled Chromium');
    }

    const args = [
      '--remote-debugging-port=0',
      '--user-data-dir=' + userDataDir,
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-dev-shm-usage',
      '--no-sandbox',
      'about:blank'
    ];
    const out = fs.openSync(path.join(sessionDir, 'browser.out.log'), 'a');
    const err = fs.openSync(path.join(sessionDir, 'browser.err.log'), 'a');
    const child = spawn(executablePath, args, {
      detached: true,
      stdio: ['ignore', out, err]
    });
    child.unref();
    return await waitForDevToolsEndpoint(userDataDir, 15000);
  }

  function normalizeText(value, limit = 4000) {
    return String(value || '').replace(/\s+/g, ' ').trim().slice(0, limit);
  }

  function cssPath(el) {
    if (!el || !el.tagName) return '';
    if (el.id) return '#' + CSS.escape(el.id);
    const parts = [];
    let node = el;
    while (node && node.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
      let part = node.tagName.toLowerCase();
      if (node.id) {
        part += '#' + CSS.escape(node.id);
        parts.unshift(part);
        break;
      }
      const parent = node.parentElement;
      if (parent) {
        const siblings = Array.from(parent.children).filter(child => child.tagName === node.tagName);
        if (siblings.length > 1) {
          part += ':nth-of-type(' + (siblings.indexOf(node) + 1) + ')';
        }
      }
      parts.unshift(part);
      node = parent;
    }
    return parts.join(' > ');
  }

  async function observe(page) {
    const elements = await page.evaluate(() => {
      const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"],[tabindex]'));
      return candidates.slice(0, 80).map((el, index) => {
        const role = el.getAttribute('role') || el.tagName.toLowerCase();
        const text = (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.value || '').replace(/\s+/g, ' ').trim();
        const selector = (function cssPath(node) {
          if (!node || !node.tagName) return '';
          if (node.id) return '#' + CSS.escape(node.id);
          const parts = [];
          let current = node;
          while (current && current.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
            let part = current.tagName.toLowerCase();
            if (current.id) {
              part += '#' + CSS.escape(current.id);
              parts.unshift(part);
              break;
            }
            const parent = current.parentElement;
            if (parent) {
              const siblings = Array.from(parent.children).filter(child => child.tagName === current.tagName);
              if (siblings.length > 1) part += ':nth-of-type(' + (siblings.indexOf(current) + 1) + ')';
            }
            parts.unshift(part);
            current = parent;
          }
          return parts.join(' > ');
        })(el);
        return { ref: 'e' + (index + 1), role, text: text.slice(0, 160), selector, tag: el.tagName.toLowerCase() };
      });
    });
    return {
      url: page.url(),
      title: await page.title(),
      text: normalizeText(await page.locator('body').innerText({ timeout: 2000 }).catch(() => ''), 6000),
      elements
    };
  }

  async function selectorFromRef(page, ref) {
    const match = String(ref || '').match(/^e(\d+)$/);
    if (!match) return '';
    const index = Number(match[1]) - 1;
    return await page.evaluate((index) => {
      const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"],[tabindex]'));
      const node = candidates[index];
      if (!node) return '';
      if (node.id) return '#' + CSS.escape(node.id);
      const parts = [];
      let current = node;
      while (current && current.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
        let part = current.tagName.toLowerCase();
        if (current.id) {
          part += '#' + CSS.escape(current.id);
          parts.unshift(part);
          break;
        }
        const parent = current.parentElement;
        if (parent) {
          const siblings = Array.from(parent.children).filter(child => child.tagName === current.tagName);
          if (siblings.length > 1) part += ':nth-of-type(' + (siblings.indexOf(current) + 1) + ')';
        }
        parts.unshift(part);
        current = parent;
      }
      return parts.join(' > ');
    }, index);
  }

  async function resolveSelector(page, input) {
    const selector = input.selector || await selectorFromRef(page, input.ref);
    if (!selector) throw new Error('browser ' + input.action + ' requires selector or valid ref');
    return selector;
  }

  async function applyAction(page, action, timeout) {
    if (!action || !action.action) return;
    if (action.action === 'click') {
      if (!action.selector) throw new Error('browser replay click requires selector');
      await page.locator(action.selector).first().click({ timeout });
      return;
    }
    if (action.action === 'type') {
      if (!action.selector) throw new Error('browser replay type requires selector');
      const locator = page.locator(action.selector).first();
      if (action.clear) await locator.fill('', { timeout });
      await locator.fill(String(action.text || ''), { timeout });
    }
  }

  async function waitForTakeover(page, waitSeconds) {
    const deadline = Date.now() + Math.max(1, Number(waitSeconds || 300)) * 1000;
    let lastObserved = await observe(page).catch(() => ({}));
    while (Date.now() < deadline) {
      if (page.isClosed()) break;
      await sleep(1000);
      const nextObserved = await observe(page).catch(() => null);
      if (nextObserved && nextObserved.url) lastObserved = nextObserved;
    }
    return lastObserved;
  }

  const input = JSON.parse(await readStdin() || '{}');
  const timeout = Number(input.timeout_ms || 15000);
  const statePath = input.state_path || '';
  const playwright = await loadPlaywright();

  let state = {};
  if (statePath && fs.existsSync(statePath)) {
    try { state = JSON.parse(fs.readFileSync(statePath, 'utf8')); } catch (_) { state = {}; }
  }
  const persistent = !!input.persistent;

  if (input.action === 'close') {
    if (persistent && await endpointAlive(state.cdp_endpoint)) {
      const browser = await playwright.chromium.connectOverCDP(state.cdp_endpoint);
      const session = await browser.newBrowserCDPSession();
      await session.send('Browser.close').catch(async () => { await browser.close().catch(() => {}); });
    }
    if (statePath) {
      mkdirp(statePath);
      fs.writeFileSync(statePath, JSON.stringify({
        base_url: state.base_url || state.url || '',
        url: state.url || '',
        storage_state: state.storage_state || {},
        actions: [],
        closed: true
      }));
    }
    console.log(JSON.stringify({
      browser_session_id: input.browser_session_id || (input.meta && input.meta.session_id) || 'anonymous',
      text: 'Browser session closed.',
      persistent
    }));
    return;
  }

  let browser;
  let context;
  let page;
  let cdpEndpoint = '';
  if (persistent) {
    cdpEndpoint = await persistentEndpoint(input, state, playwright);
    browser = await playwright.chromium.connectOverCDP(cdpEndpoint);
    context = browser.contexts()[0] || await browser.newContext();
    page = context.pages().find(candidate => !candidate.isClosed()) || await context.newPage();
  } else {
    const launchOptions = {
      headless: String(process.env.TMA_BROWSER_HEADLESS || 'true') !== 'false',
      args: ['--no-sandbox', '--disable-dev-shm-usage']
    };
    if (process.env.TMA_BROWSER_EXECUTABLE_PATH) {
      launchOptions.executablePath = process.env.TMA_BROWSER_EXECUTABLE_PATH;
    }
    browser = await playwright.chromium.launch(launchOptions);
  }

  const hasInputURL = typeof input.url === 'string' && input.url.trim() !== '';
  const baseURL = hasInputURL ? input.url : (state.base_url || state.url || 'about:blank');
  let actions = persistent || hasInputURL || input.action === 'open' ? [] : (Array.isArray(state.actions) ? state.actions : []);
  const contextOptions = {};
  if (input.user_agent) contextOptions.userAgent = input.user_agent;
  if (input.viewport && input.viewport.width && input.viewport.height) {
    contextOptions.viewport = { width: Number(input.viewport.width), height: Number(input.viewport.height) };
  }
  if (!persistent && state.storage_state) contextOptions.storageState = state.storage_state;

  if (!persistent) {
    context = await browser.newContext(contextOptions);
    page = await context.newPage();
  } else if (input.viewport && input.viewport.width && input.viewport.height) {
    await page.setViewportSize({ width: Number(input.viewport.width), height: Number(input.viewport.height) }).catch(() => {});
  }
  page.setDefaultTimeout(timeout);

  const currentURL = page.url();
  const targetURL = persistent && !hasInputURL && currentURL && currentURL !== 'about:blank' ? '' : (baseURL || 'about:blank');
  if (targetURL && targetURL !== 'about:blank') {
    await page.goto(targetURL, { waitUntil: 'domcontentloaded', timeout });
  }
  for (const action of actions) {
    await applyAction(page, action, timeout);
  }

  if (input.action === 'click') {
    const selector = await resolveSelector(page, input);
    await applyAction(page, { action: 'click', selector }, timeout);
    actions = actions.concat([{ action: 'click', selector }]);
  } else if (input.action === 'type') {
    const selector = await resolveSelector(page, input);
    const entry = { action: 'type', selector, text: String(input.text || ''), clear: !!input.clear };
    await applyAction(page, entry, timeout);
    actions = actions.concat([entry]);
  } else if (input.action === 'screenshot') {
    if (input.screenshot_path) {
      mkdirp(input.screenshot_path);
      await page.screenshot({ path: input.screenshot_path, fullPage: !!input.full_page });
    }
  }

  let observed;
  if (input.action === 'takeover') {
    observed = await waitForTakeover(page, input.wait_seconds || 300);
  } else {
    observed = await observe(page);
  }
  if (statePath) {
    mkdirp(statePath);
    const storageState = await context.storageState().catch(() => state.storage_state || {});
    fs.writeFileSync(statePath, JSON.stringify({
      base_url: baseURL,
      url: observed.url,
      storage_state: storageState,
      actions,
      cdp_endpoint: cdpEndpoint || state.cdp_endpoint || '',
      persistent
    }));
  }

  const output = {
    browser_session_id: input.browser_session_id || (input.meta && input.meta.session_id) || 'anonymous',
    url: observed.url,
    title: observed.title,
    text: observed.text,
    elements: observed.elements,
    screenshot_path: input.screenshot_path || '',
    persistent
  };
  console.log(JSON.stringify(output));
  if (!persistent) {
    await browser.close().catch(() => {});
  }
})().catch(err => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});
`
