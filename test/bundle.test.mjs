// DeepSeek API Balance bundle tests — node --test + vm, host mock, no browser.
// Covers the task-05 acceptance matrix: slot registration, pill rendering and
// colors, panel content, hover/click mechanics, invokeAction argument shape,
// transient-error handling, and destroy cleanup.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

function bundleSource() {
  return readFileSync(new URL("../ui/bundle.js", import.meta.url), "utf8");
}

// ---------------------------------------------------------------------------
// Element-tree helpers (elements are plain {type, props, children} objects)
// ---------------------------------------------------------------------------

function element(type, props, ...children) {
  return { type, props: props || {}, children };
}

function everyElement(node, predicate, out = []) {
  if (Array.isArray(node)) {
    node.forEach((child) => everyElement(child, predicate, out));
    return out;
  }
  if (!node || typeof node !== "object") return out;
  if (predicate(node)) out.push(node);
  everyElement(node.children, predicate, out);
  return out;
}

function byType(tree, type) {
  return everyElement(tree, (node) => node.type === type);
}

function byId(tree, id) {
  return everyElement(tree, (node) => node.props.id === id);
}
function renderedText(node) {
  if (Array.isArray(node)) return node.map(renderedText).join("");
  if (node == null || typeof node === "boolean") return "";
  if (typeof node !== "object") return String(node);
  return renderedText(node.children);
}

// ---------------------------------------------------------------------------
// Sandbox plumbing: fake timers, document, host mock, React harness
// ---------------------------------------------------------------------------

function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeDocument() {
  const styles = [];
  const head = {
    appendChild(el) {
      el.parentNode = head;
      styles.push(el);
    },
    removeChild(el) {
      const i = styles.indexOf(el);
      if (i >= 0) styles.splice(i, 1);
    },
  };
  return {
    styles,
    head,
    createElement() {
      return { id: "", textContent: "", parentNode: null };
    },
    getElementById(id) {
      return styles.find((s) => s.id === id) || null;
    },
  };
}

function makeHost(overrides) {
  const actionDeferreds = [];
  const actionCalls = [];
  const host = {
    jsx: element,
    ui: { Button: "Button", Card: "Card" },
    api: {
      invokeAction(key, input) {
        actionCalls.push({ key, input });
        const d = deferred();
        actionDeferreds.push(d);
        return d.promise;
      },
    },
    utils: {
      formatRelativeTime: (v) => "updated:" + String(v),
    },
    ...(overrides || {}),
  };
  return {
    host,
    actionCalls,
    resolveAction(i, data) {
      actionDeferreds[i].resolve(data);
      return flushMicrotasks();
    },
    rejectAction(i, err) {
      actionDeferreds[i].reject(err || new Error("boom"));
      return flushMicrotasks();
    },
  };
}

function flushMicrotasks() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// normalize JSON-roundtrips a value so vm-realm objects compare cleanly under
// the strict assert module.
function normalize(value) {
  return JSON.parse(JSON.stringify(value));
}

// createReactApi returns a host.React mock whose hooks delegate to the
// currently mounted component context (set by mount). This lets the bundle
// capture host.React at initialize time while re-renders stay per-mount.
function createReactApi() {
  let ctx = null;
  return {
    _setContext(next) {
      ctx = next;
    },
    useState(initial) {
      const c = ctx;
      const i = c.hookIdx++;
      if (!c.stateValues[i]) {
        c.stateValues[i] = [typeof initial === "function" ? initial() : initial, (v) => c.setValue(i, v)];
      }
      return c.stateValues[i];
    },
    useRef(initial) {
      const c = ctx;
      const i = c.hookIdx++;
      if (!c.refs[i]) c.refs[i] = { current: initial };
      return c.refs[i];
    },
    useEffect(fn, deps) {
      const c = ctx;
      const i = c.hookIdx++;
      if (!c.effects[i]) c.effects[i] = { fn: null, deps: null, cleanup: null, lastDeps: null };
      c.effects[i].fn = fn;
      c.effects[i].deps = deps;
    },
  };
}

// mount renders a component under the React mock with effect deps handling,
// ref assignment (the wrap element gets a fake getBoundingClientRect), and
// synchronous re-render on state updates.
function mount(react, component, { slotProps, rect }) {
  const ctx = {
    stateValues: [],
    refs: [],
    effects: [],
    hookIdx: 0,
    mounted: true,
    props: { slotProps },
    tree: null,
    setValue(i, next) {
      if (!ctx.mounted) return;
      const cur = ctx.stateValues[i][0];
      ctx.stateValues[i][0] = typeof next === "function" ? next(cur) : next;
      render();
    },
  };
  react._setContext(ctx);

  function fakeElement() {
    return {
      getBoundingClientRect: () =>
        rect || { top: 20, right: 300, bottom: 48, left: 272, width: 28, height: 28 },
    };
  }

  function assignRefs(node) {
    if (Array.isArray(node)) {
      node.forEach(assignRefs);
      return;
    }
    if (!node || typeof node !== "object") return;
    if (node.props.ref) node.props.ref.current = fakeElement();
    assignRefs(node.children);
  }

  function render() {
    ctx.hookIdx = 0;
    ctx.tree = component(ctx.props);
    assignRefs(ctx.tree);
    ctx.effects.forEach((e) => {
      if (!e.fn) return;
      const changed = !e.deps || !e.lastDeps || e.deps.some((d, j) => d !== e.lastDeps[j]);
      if (changed) {
        if (e.cleanup) e.cleanup();
        e.cleanup = e.fn() || null;
        e.lastDeps = e.deps ? [...e.deps] : null;
      }
    });
    return ctx.tree;
  }

  function unmount() {
    ctx.mounted = false;
    ctx.effects.forEach((e) => {
      if (e.cleanup) e.cleanup();
      e.cleanup = null;
    });
    ctx.effects.length = 0;
    react._setContext(null);
  }

  render();
  return { tree: () => ctx.tree, render, unmount, react };
}

// ---------------------------------------------------------------------------
// Bundle loading
// ---------------------------------------------------------------------------

function loadPlugin() {
  let plugin;
  const document = makeDocument();
  const intervals = new Map();
  const timeouts = new Map();
  let nextTimer = 1;

  const sandbox = {
    window: {
      innerWidth: 1440,
      innerHeight: 900,
      registerKandevPlugin(_id, definition) {
        plugin = definition;
      },
    },
    document,
    setInterval(fn) {
      const id = nextTimer++;
      intervals.set(id, fn);
      return id;
    },
    clearInterval(id) {
      intervals.delete(id);
    },
    setTimeout(fn) {
      const id = nextTimer++;
      timeouts.set(id, fn);
      return id;
    },
    clearTimeout(id) {
      timeouts.delete(id);
    },
    Intl,
    Date,
    Math,
    String,
    Number,
    isFinite,
  };
  vm.runInNewContext(bundleSource(), sandbox);

  return { plugin, document, intervals, timeouts };
}

// loadWithTestableHelpers loads a second instance exposing the pure helpers.
function loadWithTestableHelpers() {
  let plugin;
  const sandbox = {
    window: {
      innerWidth: 1440,
      innerHeight: 900,
      registerKandevPlugin(_id, definition) {
        plugin = definition;
      },
    },
    document: makeDocument(),
    setInterval() {
      return 1;
    },
    clearInterval() {},
    setTimeout() {
      return 1;
    },
    clearTimeout() {},
    Intl,
    Date,
    Math,
    String,
    Number,
    isFinite,
  };
  vm.runInNewContext(
    bundleSource() +
      "\nwindow.__deepseekTest = {" +
      " formatBalance: typeof formatBalance === 'function' ? formatBalance : null," +
      " pillTone: typeof pillTone === 'function' ? pillTone : null," +
      " pillState: typeof pillState === 'function' ? pillState : null," +
      " primaryInfo: typeof primaryInfo === 'function' ? primaryInfo : null," +
      " usagePopoverPosition: typeof usagePopoverPosition === 'function' ? usagePopoverPosition : null," +
      " pillContent: typeof pillContent === 'function' ? pillContent : null," +
      " panelBody: typeof panelBody === 'function' ? panelBody : null" +
      " };",
    sandbox,
  );
  return { plugin, helpers: () => sandbox.window.__deepseekTest };
}

// mountPlugin wires a fresh host + React mock, initializes the bundle, mounts
// the chat-top-bar component, and returns the host kit + mounted harness.
function mountPlugin(plugin, { slotProps, rect }) {
  const react = createReactApi();
  const hostKit = makeHost({ React: react });
  const components = [];
  plugin.initialize(
    {
      registerComponent(slot, comp) {
        components.push(comp);
      },
    },
    hostKit.host,
  );
  const mounted = mount(react, components[0], { slotProps, rect });
  return { ...hostKit, mounted };
}

// okData builds a canned ok action response.
function okData(overrides) {
  return {
    status: "ok",
    error: null,
    fetched_at: "2026-08-20T12:00:00Z",
    is_available: true,
    balance_infos: [
      { currency: "CNY", total_balance: "110.00", granted_balance: "10.00", topped_up_balance: "100.00" },
    ],
    warn_below: 10,
    ...(overrides || {}),
  };
}

function pillOf(tree) {
  const buttons = byId(tree, "deepseek-credits-topbar");
  assert.equal(buttons.length, 1, "exactly one topbar pill");
  return buttons[0];
}

function pillText(tree) {
  return renderedText(pillOf(tree));
}

function amountSpan(tree) {
  // The amount is the tabular-nums 600-weight span inside the pill.
  const spans = everyElement(
    pillOf(tree),
    (n) => n.type === "span" && n.props.style && n.props.style.fontWeight === 600,
  );
  assert.equal(spans.length, 1, "pill renders exactly one amount span");
  return spans[0];
}

// openPanel simulates mouseenter on the wrap and returns the re-rendered tree
// (the panel appears after the state update).
function openPanel(mounted) {
  const tree = mounted.tree();
  const wrap = everyElement(tree, (n) => n.type === "div" && typeof n.props.onMouseEnter === "function")[0];
  assert.ok(wrap, "wrap element exists");
  wrap.props.onMouseEnter();
  return mounted.tree();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test("registers exactly the chat-top-bar component slot", () => {
  const { plugin } = loadPlugin();
  const slots = [];
  plugin.initialize(
    {
      registerComponent(slot) {
        slots.push(slot);
      },
    },
    {},
  );
  assert.deepEqual(slots, ["chat-top-bar", "chat-input-actions"]);
});

test("prompt input action renders when enabled", async () => {
  const { plugin } = loadPlugin();
  const react = createReactApi();
  const hostKit = makeHost({ React: react });
  const components = [];
  plugin.initialize(
    { registerComponent(_slot, component) { components.push(component); } },
    hostKit.host,
  );
  const mounted = mount(react, components[1], { slotProps: { taskId: "task-1" } });
  await hostKit.resolveAction(0, okData({ display_prompt_input: true }));
  assert.equal(byId(mounted.tree(), "deepseek-credits-prompt-action").length, 1);
  mounted.unmount();
});

test("topbar styles enforce desktop and phone geometry", () => {
  const { plugin, document } = loadPlugin();
  plugin.initialize({ registerComponent() {} }, {});
  const css = document.styles.map((s) => s.textContent).join("\n");
  assert.match(css, /#deepseek-credits-topbar\{height:28px;min-height:28px\}/);
  assert.match(css, /@media \(max-width:639px\)\{#deepseek-credits-topbar\{height:44px;min-height:44px\}/);
});

test("pill renders the formatted primary-currency balance", async () => {
  const { plugin } = loadPlugin();
  const { mounted, actionCalls, resolveAction } = mountPlugin(plugin, {
    slotProps: { workspaceId: "ws-1", activeSessionId: "s1" },
  });

  assert.equal(actionCalls.length, 1, "mount issues one balance.get");
  await resolveAction(0, okData());

  const text = pillText(mounted.tree());
  assert.ok(text.includes("¥110.00"), "pill shows the formatted total, got: " + text);
  assert.ok(text.includes("Ds"), "pill carries the DeepSeek monogram");
});

test("pill turns amber below the server-sent warn_below", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(
    0,
    okData({
      balance_infos: [{ currency: "CNY", total_balance: "5.00", granted_balance: "0.00", topped_up_balance: "5.00" }],
    }),
  );

  assert.equal(amountSpan(mounted.tree()).props.style.color, "#e0a95e");
});

test("pill turns muted coral while unavailable", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData({ is_available: false }));

  assert.equal(amountSpan(mounted.tree()).props.style.color, "#d97b6c");
});

test("coral wins over amber when low AND unavailable", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(
    0,
    okData({
      is_available: false,
      balance_infos: [{ currency: "CNY", total_balance: "5.00", granted_balance: "0.00", topped_up_balance: "5.00" }],
    }),
  );

  assert.equal(amountSpan(mounted.tree()).props.style.color, "#d97b6c");
});

test("neutral checking state before the first snapshot", async () => {
  const { plugin } = loadPlugin();
  const { mounted } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });

  // No snapshot yet: loading, no amount.
  const text = pillText(mounted.tree());
  assert.ok(!text.includes("¥"), "no fabricated balance while loading");
  const loading = everyElement(pillOf(mounted.tree()), (n) => n.props["data-deepseek-state"] === "loading");
  assert.equal(loading.length, 1, "loading state marked on the monogram");
});

test("unconfigured guidance renders in the panel", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, {
    status: "unconfigured",
    error: null,
    fetched_at: null,
    is_available: null,
    balance_infos: null,
    warn_below: null,
  });

  assert.ok(!pillText(mounted.tree()).includes("¥"));
  const tree = openPanel(mounted);
  const panelText = renderedText(tree);
  assert.match(panelText, /No API key configured\./);
  assert.match(panelText, /Settings → Plugins → DeepSeek API Balance/);
  assert.match(panelText, /DEEPSEEK_API_KEY/);
});

test("loading state renders checking text in the panel", async () => {
  const { plugin } = loadPlugin();
  const { mounted } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });

  const tree = openPanel(mounted);
  assert.match(renderedText(tree), /Checking balance…/);
});

test("status error with no snapshot renders neutral unavailable and the reason", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, {
    status: "error",
    error: { code: "invalid_key", message: "DeepSeek rejected the API key (401)" },
    fetched_at: null,
    is_available: null,
    balance_infos: null,
    warn_below: 10,
  });

  const error = everyElement(pillOf(mounted.tree()), (n) => n.props["data-deepseek-state"] === "error");
  assert.equal(error.length, 1, "error-without-snapshot is a distinct neutral state, not loading");
  assert.ok(!pillText(mounted.tree()).includes("¥"), "no fabricated balance");

  const tree = openPanel(mounted);
  const panelText = renderedText(tree);
  assert.match(panelText, /DeepSeek rejected the API key \(401\)/);
  assert.match(panelText, /Settings → Plugins → DeepSeek API Balance/);
});

test("error keeps the last-known render", async () => {
  const { plugin, intervals } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData());

  // Next action (the silent re-read): domain error carrying the retained
  // snapshot.
  const intervalId = [...intervals.keys()][0];
  intervals.get(intervalId)();
  await resolveAction(
    1,
    okData({
      status: "error",
      error: { code: "network", message: "DeepSeek balance request failed: connection refused" },
    }),
  );
  assert.ok(pillText(mounted.tree()).includes("¥110.00"), "last-known balance stays rendered");
  const tree = openPanel(mounted);
  assert.match(renderedText(tree), /connection refused/, "the failure reason is shown in the panel");
});

test("panel lists breakdown, status, and last-updated", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData());

  const tree = openPanel(mounted);
  const panelText = renderedText(tree);
  assert.match(panelText, /Total balance/);
  assert.match(panelText, /¥110\.00/);
  assert.match(panelText, /Granted/);
  assert.match(panelText, /¥10\.00/);
  assert.match(panelText, /Topped up/);
  assert.match(panelText, /¥100\.00/);
  assert.match(panelText, /Status: available/);
  assert.match(
    panelText,
    /Updated updated:2026-08-20T12:00:00Z/,
    "last-updated via host.utils.formatRelativeTime",
  );
});

test("with several balance_infos the pill shows the first entry and the panel lists every entry", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(
    0,
    okData({
      balance_infos: [
        { currency: "CNY", total_balance: "110.00", granted_balance: "10.00", topped_up_balance: "100.00" },
        { currency: "USD", total_balance: "5.20", granted_balance: "0.00", topped_up_balance: "5.20" },
      ],
    }),
  );

  assert.ok(pillText(mounted.tree()).includes("¥110.00"), "pill shows the primary (first) entry");
  assert.ok(!pillText(mounted.tree()).includes("$5.20"), "pill does not show secondary entries");

  const tree = openPanel(mounted);
  const panelText = renderedText(tree);
  assert.match(panelText, /Other currencies/);
  assert.match(panelText, /USD/);
  assert.match(panelText, /\$5\.20/);
});

test("EMPTY balance_infos renders icon-only colored by is_available, no formatting", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData({ balance_infos: [], is_available: false }));

  const text = pillText(mounted.tree());
  assert.ok(!text.includes("¥"), "no currency amount without a primary currency");
  const coral = everyElement(
    pillOf(mounted.tree()),
    (n) => n.type === "span" && n.props.style && n.props.style.background === "#d97b6c",
  );
  assert.equal(coral.length, 1, "icon-only pill colored coral when unavailable");
});

test("hover opens the panel and mouseleave schedules close via the padding-bridge timer", async () => {
  const { plugin, timeouts } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData());

  // mouseenter on the wrap opens the panel.
  const wrap = everyElement(mounted.tree(), (n) => n.type === "div" && typeof n.props.onMouseEnter === "function")[0];
  assert.ok(wrap);
  wrap.props.onMouseEnter();
  assert.equal(byId(mounted.tree(), "deepseek-credits-refresh").length, 1, "panel is open after hover");

  // mouseleave schedules the close timer (the padding bridge).
  wrap.props.onMouseLeave();
  assert.equal(timeouts.size, 1, "close scheduled on mouseleave");

  // Entering the panel cancels the scheduled close.
  const panelWrap = everyElement(mounted.tree(), (n) => n.props && n.props.style && n.props.style.position === "fixed")[0];
  assert.ok(panelWrap, "fixed panel bridge exists");
  panelWrap.props.onMouseEnter();
  assert.equal(timeouts.size, 0, "entering the panel cancels the close timer");

  // Leaving again and firing the close timer closes the panel.
  wrap.props.onMouseLeave();
  assert.equal(timeouts.size, 1);
  const fn = [...timeouts.values()][0];
  timeouts.clear();
  fn();
  assert.equal(byId(mounted.tree(), "deepseek-credits-refresh").length, 0, "panel closed after the timer fires");
});

test("click toggles the panel", async () => {
  const { plugin } = loadPlugin();
  const { mounted, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData());

  pillOf(mounted.tree()).props.onClick();
  assert.equal(byId(mounted.tree(), "deepseek-credits-refresh").length, 1, "click opens");

  pillOf(mounted.tree()).props.onClick();
  assert.equal(byId(mounted.tree(), "deepseek-credits-refresh").length, 0, "second click closes");
});

test("null or empty workspaceId renders nothing and issues no invokeAction", () => {
  const { plugin } = loadPlugin();
  for (const workspaceId of [null, "", undefined]) {
    const { mounted, actionCalls } = mountPlugin(plugin, {
      slotProps: { workspaceId, activeSessionId: "s1" },
    });
    assert.equal(mounted.tree(), null, "renders nothing without a workspace");
    assert.equal(actionCalls.length, 0, "no fetch without a workspace");
  }
});

test("silent interval reads the cached snapshot with no body; Refresh forces a rebuild", async () => {
  const { plugin, intervals } = loadPlugin();
  const { mounted, actionCalls, resolveAction } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  await resolveAction(0, okData());

  // The 60s silent re-read: { workspaceId } only, no body. (The input object
  // is built inside the vm realm; normalize before strict comparison.)
  const intervalId = [...intervals.keys()][0];
  intervals.get(intervalId)();
  assert.equal(actionCalls.length, 2);
  assert.deepEqual(normalize(actionCalls[1]), { key: "balance.get", input: { workspaceId: "ws-1" } });
  await resolveAction(1, okData());

  // Opening the panel triggers a plain cached read (no body).
  const tree = openPanel(mounted);
  assert.equal(actionCalls.length, 3);
  assert.deepEqual(normalize(actionCalls[2]), { key: "balance.get", input: { workspaceId: "ws-1" } });
  await resolveAction(2, okData());

  // Refresh: body { refresh: true }.
  const refresh = byId(tree, "deepseek-credits-refresh")[0];
  refresh.props.onClick();
  assert.equal(actionCalls.length, 4);
  assert.deepEqual(normalize(actionCalls[3]), {
    key: "balance.get",
    input: { workspaceId: "ws-1", body: { refresh: true } },
  });
});

test("a rejected invokeAction is transient: last render kept, next interval retries", async () => {
  const { plugin, intervals } = loadPlugin();
  const { mounted, actionCalls, resolveAction, rejectAction } = mountPlugin(plugin, {
    slotProps: { workspaceId: "ws-1" },
  });
  await resolveAction(0, okData());

  // The silent re-read is rejected: transient error, last render kept.
  const intervalId = [...intervals.keys()][0];
  intervals.get(intervalId)();
  await rejectAction(1, new Error("host 504"));
  assert.ok(pillText(mounted.tree()).includes("¥110.00"), "transient failure never clears the last render");

  intervals.get(intervalId)();
  assert.equal(actionCalls.length, 3, "the next interval retries");
});

test("destroy clears the silent re-read timer and removes injected styles", async () => {
  const { plugin, document, intervals } = loadPlugin();
  const { mounted } = mountPlugin(plugin, { slotProps: { workspaceId: "ws-1" } });
  assert.equal(intervals.size, 1);

  plugin.destroy();
  assert.equal(intervals.size, 0, "silent re-read timer cleared");
  assert.equal(document.styles.length, 0, "injected styles removed");
  mounted.unmount();
});

// ---------------------------------------------------------------------------
// Pure helper contracts
// ---------------------------------------------------------------------------

test("formatBalance renders narrowSymbol currency and compact overflow", () => {
  const { helpers } = loadWithTestableHelpers();
  const { formatBalance } = helpers();
  assert.equal(formatBalance(110, "CNY"), "¥110.00");
  assert.equal(formatBalance(5.2, "USD"), "$5.20");
  const standard = formatBalance(1234567, "CNY");
  const compact = formatBalance(1234567, "CNY", { compact: true });
  assert.ok(compact.length < standard.length, "compact shortens the overflow amount");
  assert.ok(!compact.includes("1,234,567"), "compact drops the full digits");
});

test("usagePopoverPosition anchors below the trigger and clamps to the viewport", () => {
  const { helpers } = loadWithTestableHelpers();
  const { usagePopoverPosition } = helpers();
  // Spread into the host realm so deepEqual compares plain objects (vm
  // results carry the sandbox realm's prototype).
  const below = { ...usagePopoverPosition({ top: 20, right: 300, bottom: 48 }, 1440, 900, "below") };
  assert.deepEqual(below, { top: 48, left: 28 });
  // A trigger near the left edge clamps to the 8px margin.
  const clamped = { ...usagePopoverPosition({ top: 20, right: 100, bottom: 48 }, 1440, 900, "below") };
  assert.deepEqual(clamped, { top: 48, left: 8 });
});
