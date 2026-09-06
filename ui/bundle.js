// DeepSeek API Balance — kandev plugin UI bundle.
//
// Hand-written, NO-BUILD plain-JS ES module (shared host React via host.jsx —
// never bundles its own React). Registers one component:
//   • "chat-top-bar" — a pill in the session top bar showing the DeepSeek
//     account balance: a DeepSeek monogram chip plus the formatted total of
//     the primary currency. Hovering (desktop) or clicking/tapping (all
//     surfaces) opens a panel below the pill with the granted/topped-up
//     breakdown, every currency entry, the is_available status, the
//     last-updated time, and a Refresh control.
//
// All data comes from this plugin's Go backend through authenticated actions.
// The top bar uses the workspace-scoped action; the prompt toolbar uses the
// task-scoped variant because its slot context provides a task id only.
// The action body carries the forced-refresh flag ({ refresh: true }) because
// the host action envelope has no free-form keys.

// AUTO_REFRESH_MS is how often the UI silently re-reads the backend's warm
// snapshot (no body, no forced rebuild) so an open panel / the pill keep up
// with the server-side poller without forcing DeepSeek round trips.
var AUTO_REFRESH_MS = 60 * 1000;
var TOPBAR_STYLE_ID = "kandev-plugin-deepseek-balance-topbar-style";
var TOPBAR_ID = "deepseek-credits-topbar";
var PROMPT_ID = "deepseek-credits-prompt-action";
var CONFIG_SETTINGS_HREF = "/settings/plugins/kandev-plugin-deepseek-balance";
var PANEL_WIDTH = 272;

var TOPBAR_CSS =
  "#deepseek-credits-topbar{height:28px;min-height:28px}" +
  "@media (max-width:639px){#deepseek-credits-topbar{height:44px;min-height:44px}}" +
  "#deepseek-credits-topbar [data-deepseek-state=loading]{animation:deepseek-credits-pulse 1.6s ease-in-out infinite}" +
  "@keyframes deepseek-credits-pulse{0%,100%{opacity:1}50%{opacity:0.45}}";

function injectTopbarStyles() {
  if (typeof document === "undefined") return;
  if (document.getElementById(TOPBAR_STYLE_ID)) return;
  var style = document.createElement("style");
  style.id = TOPBAR_STYLE_ID;
  style.textContent = TOPBAR_CSS;
  document.head.appendChild(style);
}

function removeTopbarStyles() {
  if (typeof document === "undefined") return;
  var style = document.getElementById(TOPBAR_STYLE_ID);
  if (style && style.parentNode) style.parentNode.removeChild(style);
}

// activeIntervals lets plugin destroy() clear the silent re-read timers even
// when the component outlived a registry teardown edge.
var activeIntervals = new Set();

// ---- palette ---------------------------------------------------------------
// Calm by default: normal balance is soft indigo, low balance warms to amber,
// and an unavailable account is muted coral (never a hard red).
var COLOR = {
  indigo: "#8085e6",
  amber: "#e0a95e",
  coral: "#d97b6c",
  brand: "#4D6BFE",
};

// formatBalance renders a currency amount the way the pill and panel show it:
// narrowSymbol so CNY renders ¥ and USD $, tabular digits. Compact notation is
// used when the amount would overflow the pill.
function formatBalance(value, currency, opts) {
  opts = opts || {};
  var compact = !!opts.compact;
  try {
    return new Intl.NumberFormat(opts.locale || undefined, {
      style: "currency",
      currency: currency,
      currencyDisplay: "narrowSymbol",
      notation: compact ? "compact" : "standard",
      minimumFractionDigits: compact ? 0 : 2,
      maximumFractionDigits: 2,
    }).format(value);
  } catch (e) {
    // Unknown currency code (a future DeepSeek currency): fall back to the
    // raw number so the pill never crashes or renders NaN.
    return String(value);
  }
}

// primaryInfo returns the first balance_infos entry — the spec's primary
// currency, preserving DeepSeek's response order.
function primaryInfo(d) {
  var infos = (d && d.balance_infos) || [];
  return infos.length ? infos[0] : null;
}

// pillTone resolves the pill's signal color from the action response: muted
// coral while is_available is false (wins over amber), amber below the
// server-sent warn_below, calm indigo otherwise. null means neutral (loading /
// unconfigured / error without a snapshot).
function pillTone(d) {
  if (!d) return null;
  if (d.is_available === false) return COLOR.coral;
  var primary = primaryInfo(d);
  if (primary && typeof d.warn_below === "number") {
    var total = Number(primary.total_balance);
    if (isFinite(total) && total < d.warn_below) return COLOR.amber;
  }
  return COLOR.indigo;
}

// pillState maps the action status to the pill's data-state, which drives the
// checking/unavailable distinction via the injected CSS.
function pillState(d) {
  if (!d) return "loading";
  return d.status || "loading";
}
function displayEnabled(data, surface) {
  if (surface === "task-top-right") return !data || data.display_task_top_right !== false;
  if (surface === "prompt-input") return Boolean(data && data.display_prompt_input === true);
  return false;
}

// usagePopoverPosition anchors a fixed panel above or below the trigger,
// clamped to the viewport (copied from kandev-plugin-provider-usage).
function usagePopoverPosition(rect, viewportWidth, viewportHeight, placement) {
  var left = Math.max(8, Math.min(rect.right - PANEL_WIDTH, viewportWidth - PANEL_WIDTH - 8));
  if (placement === "above") {
    return { bottom: Math.max(0, viewportHeight - rect.top), left: left };
  }
  return { top: rect.bottom, left: left };
}

// monogram renders the DeepSeek chip: a brand-hue rounded square with the
// uppercase "DS" fallback mark. tone null renders the neutral muted chip;
// state drives the pulse for the checking state.
function monogram(h, size, opts) {
  opts = opts || {};
  var tone = opts.tone || null;
  var state = opts.state || "ok";
  var bg = tone || "rgba(128,128,140,0.16)";
  var fg = tone || "#8b8b98";
  var s = size || 14;
  return h(
    "span",
    {
      "aria-hidden": "true",
      "data-deepseek-state": state,
      style: {
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: s + "px",
        height: s + "px",
        borderRadius: Math.max(4, Math.round(s * 0.36)) + "px",
        background: bg,
        color: fg,
        fontSize: Math.max(8, Math.round(s * 0.52)) + "px",
        fontWeight: 700,
        lineHeight: 1,
        fontVariantNumeric: "tabular-nums",
      },
    },
    "DS",
  );
}

function promptShowsAmount(d) {
  var primary = primaryInfo(d);
  if (!primary || !d || typeof d.warn_below !== "number") return false;
  var total = Number(primary.total_balance);
  return isFinite(total) && total < d.warn_below;
}

// pillContent renders what the pill shows: the monogram plus the formatted
// primary-currency total when one exists and opts.showAmount is not false;
// icon-only (colored by is_available) for an account with no balance data or
// a prompt-input balance at/above its warning threshold.
function pillContent(h, d, opts) {
  opts = opts || {};
  var showAmount = opts.showAmount !== false;
  var status = pillState(d);
  var primary = primaryInfo(d);
  if (primary && showAmount) {
    var tone = pillTone(d);
    var amount = Number(primary.total_balance);
    var compact = isFinite(amount) && Math.abs(amount) >= 1e6;
    return h(
      "span",
      { style: { display: "inline-flex", alignItems: "center", gap: "6px" } },
      monogram(h, 14, { tone: COLOR.brand, state: status }),
      h(
        "span",
        {
          style: {
            fontVariantNumeric: "tabular-nums",
            fontWeight: 600,
            color: tone || "var(--muted-foreground)",
          },
        },
        formatBalance(amount, primary.currency, { compact: compact }),
      ),
    );
  }
  // No primary currency (empty balance_infos, or loading/unconfigured/error):
  // icon-only. The tone carries the is_available signal when a snapshot
  // exists; neutral otherwise.
  var tone = null;
  if (status === "ok" || status === "error") {
    tone = pillTone(d);
  }
  return monogram(h, 14, { tone: tone, state: status });
}

// ---- the panel --------------------------------------------------------------

function panelRow(h, label, value) {
  return h(
    "div",
    { style: { display: "flex", alignItems: "center", justifyContent: "space-between", gap: "12px" } },
    h("span", { style: { color: "var(--muted-foreground)", fontSize: "12px" } }, label),
    h("span", { style: { fontVariantNumeric: "tabular-nums", fontWeight: 600, fontSize: "12px" } }, value),
  );
}

function settingsLink(h, host) {
  return h(
    "a",
    {
      href: CONFIG_SETTINGS_HREF,
      style: {
        color: "var(--muted-foreground)",
        textDecoration: "underline",
        textUnderlineOffset: "2px",
        cursor: "pointer",
      },
      onClick: function (event) {
        event.preventDefault();
        host.navigate(CONFIG_SETTINGS_HREF);
      },
    },
    "Settings → Plugins → DeepSeek API Balance",
  );
}

// panelBody renders the panel content for the current action response.
function panelBody(h, ui, host, d, refreshing, onRefresh) {
  var status = d ? d.status : "loading";

  var header = h(
    "div",
    { style: { display: "flex", alignItems: "center", gap: "8px", marginBottom: "10px" } },
    monogram(h, 18, { tone: COLOR.brand }),
    h("span", { style: { fontWeight: 600, fontSize: "13px" } }, "DeepSeek API Balance"),
  );

  var body = [];

  if (status === "unconfigured") {
    body.push(
      h(
        "div",
        { style: { fontSize: "12px", lineHeight: 1.5, color: "var(--muted-foreground)" } },
        "No API key configured.",
        h("br"),
        "Set it in ",
        settingsLink(h, host),
        ", or provide the DEEPSEEK_API_KEY environment variable.",
      ),
    );
  } else if (status === "loading") {
    body.push(
      h("div", { style: { fontSize: "12px", color: "var(--muted-foreground)" } }, "Checking balance…"),
    );
  } else {
    var primary = primaryInfo(d);
    var hasBalance = !!primary;

    // The insufficient-balance status LEADS the panel when DeepSeek reports
    // the account unavailable.
    if (d.is_available === false) {
      body.push(
        h("div", { style: { fontSize: "12px", fontWeight: 600, color: COLOR.coral, marginBottom: hasBalance ? "8px" : "0" } },
          "Unavailable: insufficient balance"),
      );
    }

    if (hasBalance) {
      var total = Number(primary.total_balance);
      body.push(
        h(
          "div",
          { style: { marginBottom: "10px" } },
          h("div", { style: { fontSize: "11px", color: "var(--muted-foreground)", marginBottom: "2px" } }, "Total balance"),
          h(
            "div",
            { style: { fontSize: "20px", fontWeight: 700, fontVariantNumeric: "tabular-nums", lineHeight: 1.2 } },
            formatBalance(total, primary.currency),
          ),
        ),
        panelRow(h, "Granted", formatBalance(Number(primary.granted_balance), primary.currency)),
        panelRow(h, "Topped up", formatBalance(Number(primary.topped_up_balance), primary.currency)),
      );
    }

    // Every currency entry when the account has several.
    var infos = (d.balance_infos || []).slice(1);
    if (infos.length) {
      body.push(
        h("div", { style: { fontSize: "11px", color: "var(--muted-foreground)", marginTop: "10px", marginBottom: "4px" } },
          "Other currencies"),
      );
      infos.forEach(function (info) {
        body.push(
          panelRow(h, info.currency, formatBalance(Number(info.total_balance), info.currency)),
        );
      });
    }

    // is_available status line (not already led by the unavailable line above).
    if (d.is_available === true) {
      body.push(
        h("div", { style: { fontSize: "12px", color: "var(--muted-foreground)", marginTop: "8px" } },
          "Status: available"),
      );
    }

    // A failure after a success keeps the last-known balance rendered; the
    // reason is shown so the operator knows the number is stale.
    if (status === "error" && d.error) {
      body.push(
        h("div", { style: { fontSize: "12px", lineHeight: 1.5, color: COLOR.coral, marginTop: "8px" } },
          "Last check failed: " + d.error.message),
      );
      if (!hasBalance) {
        body.push(
          h("div", { style: { fontSize: "12px", lineHeight: 1.5, color: "var(--muted-foreground)", marginTop: "4px" } },
            "Check the key in ",
            settingsLink(h, host),
            ", or the DEEPSEEK_API_KEY environment variable."),
        );
      }
    }

    if (d.fetched_at) {
      body.push(
        h("div", { style: { fontSize: "11px", color: "var(--muted-foreground)", marginTop: "10px" } },
          "Updated " + host.utils.formatRelativeTime(d.fetched_at)),
      );
    }

    body.push(
      h(
        "div",
        { style: { marginTop: "12px" } },
        h(
          ui.Button,
          {
            id: "deepseek-credits-refresh",
            type: "button",
            variant: "outline",
            size: "sm",
            disabled: !!refreshing,
            className: "w-full",
            onClick: onRefresh,
          },
          refreshing ? "Refreshing…" : "Refresh",
        ),
      ),
    );
  }

  return h("div", null, header, body);
}

// ---- the chat-top-bar component -------------------------------------------
// Self-contained hover panel: its own open state and a position:fixed panel
// (anchored to the trigger's rect) so it works regardless of whether the slot
// sits inside a Radix TooltipProvider, and escapes any overflow clipping on
// the top bar. Clicking the pill also toggles it, so nothing required is
// hover-only.
function makeTopBarBalance(host) {
  var React = host.React;
  var h = host.jsx;
  var ui = host.ui;

  return function TopBarBalance(props) {
    var ctx = (props && props.slotProps) || {};
    var workspaceId = ctx.workspaceId || "";

    var stateHook = React.useState({ data: null, error: null });
    var state = stateHook[0];
    var setState = stateHook[1];
    var openHook = React.useState(false);
    var open = openHook[0];
    var setOpen = openHook[1];
    var posHook = React.useState({ top: 0, left: 0 });
    var pos = posHook[0];
    var setPos = posHook[1];
    var refreshingHook = React.useState(false);
    var refreshing = refreshingHook[0];
    var setRefreshing = refreshingHook[1];
    var wrapRef = React.useRef(null);
    var closeTimer = React.useRef(null);

    // fetchBalance reads the balance.get action. A forced refresh travels in
    // the action body ({ refresh: true }); the silent path sends no body so it
    // serves the backend's cached snapshot. A rejection (non-2xx, host 504,
    // transport) is a transient error: the last-known render is kept and the
    // next interval or Refresh retries — the backend always answers 200 with
    // body-encoded domain errors.
    function fetchBalance(opts) {
      opts = opts || {};
      var input = { workspaceId: workspaceId };
      if (opts.refresh) {
        input.body = { refresh: true };
        setRefreshing(true);
      }
      host.api
        .invokeAction("balance.get", input)
        .then(function (data) {
          setRefreshing(false);
          setState({ data: data, error: null });
        })
        .catch(function () {
          setRefreshing(false);
          setState(function (s) { return { data: s.data, error: true }; });
        });
    }

    function load(force) {
      if (!workspaceId) return; // workspace-scoped action: no workspace, no fetch
      fetchBalance({ refresh: !!force });
    }

    React.useEffect(function () {
      load(false);
    }, [workspaceId]);

    // Keep the pill / open panel in step with the backend poller by silently
    // re-reading the warm snapshot on an interval (no body, no forced
    // rebuild).
    React.useEffect(function () {
      if (!workspaceId) return;
      var id = setInterval(function () { fetchBalance({}); }, AUTO_REFRESH_MS);
      activeIntervals.add(id);
      return function () {
        clearInterval(id);
        activeIntervals.delete(id);
      };
    }, [workspaceId]);

    function reposition() {
      var el = wrapRef.current;
      if (!el || !el.getBoundingClientRect) return;
      var r = el.getBoundingClientRect();
      // No vertical gap: the fixed container starts at the trigger's bottom
      // and bridges to the card with transparent padding, so the mouse never
      // leaves the hover area on the way down.
      setPos(usagePopoverPosition(r, window.innerWidth, window.innerHeight, "below"));
    }
    function cancelClose() {
      if (closeTimer.current) {
        clearTimeout(closeTimer.current);
        closeTimer.current = null;
      }
    }
    function openNow() {
      cancelClose();
      reposition();
      setOpen(true);
      load(false);
    }
    function scheduleClose() {
      cancelClose();
      closeTimer.current = setTimeout(function () { setOpen(false); }, 260);
    }
    function toggle() {
      if (open) {
        setOpen(false);
      } else {
        openNow();
      }
    }

    // No workspace selector: render nothing and issue no fetch (a workspace-
    // scoped action with no selector would be rejected with 400 and retried).
    if (!workspaceId) return null;

    var d = state.data;
    if (!displayEnabled(d, "task-top-right")) return null;

    return h(
      "div",
      { ref: wrapRef, style: { display: "inline-flex" }, onMouseEnter: openNow, onMouseLeave: scheduleClose },
      h(
        ui.Button,
        {
          id: TOPBAR_ID,
          type: "button",
          variant: "outline",
          size: "sm",
          className: "h-6 gap-1.5 px-2 rounded-md text-xs font-medium text-muted-foreground hover:text-foreground",
          "aria-label": "DeepSeek API balance",
          onFocus: openNow,
          onClick: toggle,
        },
        pillContent(h, d),
      ),
      open
        ? h(
            "div",
            {
              onMouseEnter: cancelClose,
              onMouseLeave: scheduleClose,
              style: { position: "fixed", top: pos.top + "px", left: pos.left + "px", zIndex: 9999, paddingTop: "8px" },
            },
            h(
              ui.Card,
              { style: { padding: "13px 14px", boxShadow: "0 10px 28px rgba(15,20,40,0.20)", width: PANEL_WIDTH + "px" } },
              panelBody(h, ui, host, d, refreshing, function () { load(true); }),
            ),
          )
        : null,
    );
  };
}

function makePromptBalance(host) {
  var React = host.React;
  var h = host.jsx;
  var ui = host.ui;

  return function PromptBalance(props) {
    var ctx = (props && props.slotProps) || {};
    var taskId = ctx.taskId || "";
    var stateHook = React.useState({ data: null });
    var state = stateHook[0];
    var setState = stateHook[1];
    var openHook = React.useState(false);
    var open = openHook[0];
    var setOpen = openHook[1];
    var posHook = React.useState({ top: 0, left: 0 });
    var pos = posHook[0];
    var setPos = posHook[1];
    var refreshingHook = React.useState(false);
    var refreshing = refreshingHook[0];
    var setRefreshing = refreshingHook[1];
    var wrapRef = React.useRef(null);
    var closeTimer = React.useRef(null);

    function load(force) {
      if (!taskId) return;
      if (force) setRefreshing(true);
      host.api.invokeAction("balance.get.task", {
        taskId: taskId,
        body: force ? { refresh: true } : undefined,
      }).then(function (data) {
        setRefreshing(false);
        setState({ data: data });
      }).catch(function () {
        setRefreshing(false);
      });
    }

    React.useEffect(function () {
      load(false);
      var id = setInterval(function () { load(false); }, AUTO_REFRESH_MS);
      activeIntervals.add(id);
      return function () {
        clearInterval(id);
        activeIntervals.delete(id);
        if (closeTimer.current) clearTimeout(closeTimer.current);
      };
    }, [taskId]);

    function reposition() {
      var element = wrapRef.current;
      if (!element || !element.getBoundingClientRect) return;
      setPos(usagePopoverPosition(element.getBoundingClientRect(), window.innerWidth, window.innerHeight, "above"));
    }
    function cancelClose() {
      if (closeTimer.current) {
        clearTimeout(closeTimer.current);
        closeTimer.current = null;
      }
    }
    function openNow() {
      cancelClose();
      reposition();
      setOpen(true);
      load(false);
    }
    function scheduleClose() {
      cancelClose();
      closeTimer.current = setTimeout(function () { setOpen(false); }, 260);
    }
    function toggle() {
      if (open) setOpen(false);
      else openNow();
    }

    var d = state.data;
    if (!taskId || !displayEnabled(d, "prompt-input")) return null;
    return h(
      "div",
      {
        ref: wrapRef,
        "data-deepseek-surface": "prompt-input",
        style: { display: "inline-flex" },
        onMouseEnter: openNow,
        onMouseLeave: scheduleClose,
      },
      h(
        ui.Button,
        {
          id: PROMPT_ID,
          type: "button",
          variant: "ghost",
          size: "sm",
          className: "h-7 gap-1.5 px-1.5 text-xs text-muted-foreground hover:bg-primary/10 hover:text-foreground",
          "aria-label": "DeepSeek API balance",
          "aria-haspopup": "dialog",
          onFocus: openNow,
          onClick: toggle,
        },
        pillContent(h, d, { showAmount: promptShowsAmount(d) }),
      ),
      open
        ? h(
            "div",
            {
              "data-deepseek-panel": "prompt-input",
              onMouseEnter: cancelClose,
              onMouseLeave: scheduleClose,
              style: {
                position: "fixed",
                bottom: pos.bottom + "px",
                left: pos.left + "px",
                zIndex: 9999,
                paddingBottom: "12px",
                pointerEvents: "auto",
              },
            },
            h(
              ui.Card,
              { style: { padding: "13px 14px", boxShadow: "0 10px 28px rgba(15,20,40,0.20)", width: PANEL_WIDTH + "px" } },
              panelBody(h, ui, host, d, refreshing, function () { load(true); }),
            ),
          )
        : null,
    );
  };
}

// ==========================================================================
window.registerKandevPlugin("kandev-plugin-deepseek-balance", {
  initialize: function (registry, host) {
    injectTopbarStyles();
    registry.registerComponent("chat-top-bar", makeTopBarBalance(host));
    registry.registerComponent("chat-input-actions", makePromptBalance(host));
  },
  destroy: function () {
    activeIntervals.forEach(clearInterval);
    activeIntervals.clear();
    removeTopbarStyles();
  },
});
