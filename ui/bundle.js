// kandev plugin UI bundle — the frontend half of this plugin.
//
// This is a hand-written, NO-BUILD plain-JS ES module. It ships byte-for-byte
// inside the package tar.gz under ui/bundle.js, and kandev serves it directly
// from the extracted package at GET /api/plugins/<id>/ui/bundle.js, then
// dynamically imports it as a native ES module. There is nothing to build:
// edit this file and repackage (`make package` / `make package-host`).
//
// The contract, in three touch points:
//   - window.registerKandevPlugin(id, { initialize, destroy }) is the single
//     global entry point the host calls once this module has been evaluated.
//     `id` MUST match manifest.yaml's id.
//   - The `host` object handed to initialize() carries the SHARED host React
//     instance (`host.React`, `host.jsx` == host.React.createElement) plus a
//     curated design system (`host.ui`), imperative toasts (`host.toast`),
//     shared helpers (`host.utils`), the live theme (`host.theme` /
//     `host.onThemeChange`), provider-neutral context (`host.context`), and
//     navigation (`host.navigate`). NEVER import or bundle your own React —
//     that breaks hook identity across the host tree. The same goes for
//     recharts: use the `host.ui.Chart*` wrappers, because a second copy splits the
//     context its tooltips and legends resolve through, exactly like React.
//   - `registry` is where you declare nav items, routes, slot components,
//     providers, task actions, review surfaces, and WS handlers. Every
//     registration is tracked under this plugin's id, so
//     the host bulk-unregisters everything when the plugin is disabled.
//
// `host.ui` is much broader than the few components used below — Accordion*,
// Collapsible*, Select*, Tabs*, Sheet*, Pagination*, ScrollArea, Skeleton,
// Switch, the Chart* recharts wrappers, and kandev's own PageTopbar,
// Combobox and TaskCreateDialog are all there. Reach for one before
// hand-rolling: a styled <div> progress bar or a getBoundingClientRect
// popover will drift from the app around it. The authoritative list is
// `apps/web/lib/plugins/host-api.ts` (`PLUGIN_UI`) in the kandev repo.
//
// Everything in this file is meant to be deleted piece by piece. The page
// below is one Card built from independent parts — Popover, Progress, Table,
// Empty — each of which can be removed without touching the others.
//
// Rename "kandev-plugin-template" below to your plugin id, then keep / delete
// registrations to match what your plugin actually contributes.

// ---------------------------------------------------------------------------
// A tiny module-level pub/sub holding the most recent task.created deliveries.
// Kept outside any component so the list survives route navigation and is
// shared by every subscriber. Delete this if you don't register a WS handler.
// ---------------------------------------------------------------------------

// How many deliveries we keep. Also the denominator of the Progress bar below,
// which is the honest thing for it to show: how full this buffer is.
const RECENT_LIMIT = 5;

let recentTasks = []; // newest first, capped at RECENT_LIMIT
const recentListeners = new Set();

function publishRecentTasks(next) {
  recentTasks = next;
  for (const listener of recentListeners) listener(recentTasks);
}

// recordTask turns one task.created WS payload into a row. The payload is the
// backend's task event shape (snake_case: task_id, title, workspace_id, ...);
// it carries no "delivered at" field, so we stamp arrival ourselves — that is
// the timestamp the table renders through host.utils.formatRelativeTime.
function recordTask(payload) {
  const task = payload || {};
  const entry = {
    taskId: task.task_id || "unknown",
    title: task.title || "Untitled task",
    seenAt: new Date().toISOString(),
  };
  publishRecentTasks([entry, ...recentTasks].slice(0, RECENT_LIMIT));
}

// useRecentTasks re-renders its component whenever publishRecentTasks fires.
// Built on host.React's useState/useEffect since this bundle can't ship its
// own useSyncExternalStore without bundling React.
function useRecentTasks(React) {
  const [tasks, setTasks] = React.useState(recentTasks);
  React.useEffect(() => {
    // Resync first: a delivery may have landed between the initial render and
    // this subscription.
    setTasks(recentTasks);
    recentListeners.add(setTasks);
    return () => recentListeners.delete(setTasks);
  }, []);
  return tasks;
}

// ---------------------------------------------------------------------------
// useHostTheme — the live light/dark theme, as component state.
//
// `host.theme` is a getter evaluated on every access, but `host` is built once
// per plugin load: read it into a variable that outlives a render and you have
// frozen it. Anything that only *styles* things can ignore this entirely —
// every host.ui component and every Tailwind/CSS-variable class already
// follows the theme on its own. You need this hook for the narrow case where
// your plugin computes a color itself (canvas painting, an inline SVG fill,
// a color passed to a chart) or, as below, displays the theme.
//
// Delete this together with whatever reads it.
// ---------------------------------------------------------------------------
function useHostTheme(host) {
  const React = host.React;
  const [theme, setTheme] = React.useState(host.theme);
  React.useEffect(() => {
    setTheme(host.theme); // resync, same reason as above
    // onThemeChange returns its own unsubscribe — returning it straight from
    // the effect is the whole teardown. Skipping it leaks a listener that
    // outlives the component.
    return host.onThemeChange(setTheme);
  }, []);
  return theme;
}

// ---------------------------------------------------------------------------
// Inline SVG icons. The bundle ships no build step and can't import an icon
// set (that would mean bundling), so glyphs are drawn by hand at 16px to match
// first-party icons. Swap for your own.
// ---------------------------------------------------------------------------
function icon(h, path, size) {
  return h(
    "svg",
    {
      xmlns: "http://www.w3.org/2000/svg",
      width: size || 16,
      height: size || 16,
      viewBox: "0 0 24 24",
      fill: "none",
      stroke: "currentColor", // follows the theme with no JS — see useHostTheme
      strokeWidth: 2,
      strokeLinecap: "round",
      strokeLinejoin: "round",
      "aria-hidden": "true",
    },
    h("path", { d: path }),
  );
}

const STAR_PATH = "M12 2l2.9 6.3 6.9.8-5.1 4.7 1.4 6.8L12 18l-6.1 3.4 1.4-6.8L2.2 9.9l6.9-.8L12 2z";
const INFO_PATH = "M12 16v-4M12 8h.01M12 21a9 9 0 100-18 9 9 0 000 18z";
const INBOX_PATH = "M22 12h-6l-2 3h-4l-2-3H2M5.5 5h13l3.5 7v6a2 2 0 01-2 2H4a2 2 0 01-2-2v-6l3.5-7z";

// The host renders shortcuts with the platform's own modifier glyph; match it
// rather than hard-coding "Ctrl", which is simply wrong on macOS.
const MOD_KEY = /Mac|iPhone|iPad/i.test((navigator && navigator.platform) || "") ? "⌘" : "Ctrl";

// ---------------------------------------------------------------------------
// A native route/page, rendered inside the kandev SPA (not an iframe) from the
// host's own design system. The host renders its first-party title bar above
// this page; here we contribute only the body.
// ---------------------------------------------------------------------------
function makePluginPage(host) {
  const { jsx: h, ui, toast, utils } = host;
  const {
    Button,
    Card,
    CardAction,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle,
    Empty,
    EmptyDescription,
    EmptyHeader,
    EmptyMedia,
    EmptyTitle,
    Kbd,
    KbdGroup,
    Popover,
    PopoverContent,
    PopoverDescription,
    PopoverHeader,
    PopoverTitle,
    PopoverTrigger,
    Progress,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
  } = ui;

  // AboutPopover — host.ui.Popover*, positioned by the host (Radix), so it
  // flips and clamps at the viewport edge without any getBoundingClientRect
  // math of your own. Delete this and the CardAction wrapping it together.
  function AboutPopover() {
    // The one thing on this page that genuinely needs a theme subscription:
    // it prints the resolved value, so a stale read is visible as a wrong
    // label when the user flips the theme with this popover open.
    const theme = useHostTheme(host);

    return h(
      Popover,
      null,
      h(
        PopoverTrigger,
        { asChild: true },
        h(
          Button,
          {
            id: "template-about-trigger",
            type: "button",
            variant: "ghost",
            size: "icon",
            className: "h-7 w-7",
            "aria-label": "About this page",
          },
          icon(h, INFO_PATH),
        ),
      ),
      h(
        PopoverContent,
        { align: "end", className: "w-80" },
        h(
          PopoverHeader,
          null,
          h(PopoverTitle, null, "Where these rows come from"),
          h(
            PopoverDescription,
            null,
            "The registerWsHandler(\"task.created\", ...) call at the bottom of ",
            "ui/bundle.js. It fires for every task created anywhere in kandev ",
            "while this tab is open — no polling, no refetch.",
          ),
        ),
        h(
          "p",
          { className: "text-muted-foreground mt-3 text-xs" },
          "Create one with ",
          // host.ui.Kbd renders a key the same way the app's own shortcut
          // surfaces do. This is kandev's real "new task" binding.
          h(KbdGroup, null, h(Kbd, null, MOD_KEY), h(Kbd, null, "N")),
          " to watch a row appear.",
        ),
        h(
          "p",
          { id: "template-theme-readout", className: "text-muted-foreground mt-3 text-xs" },
          `Host theme: ${theme}. `,
          "Everything above follows it with no JS — host.ui components and CSS ",
          "variables restyle themselves. Subscribe via host.onThemeChange only ",
          "for colors you compute yourself.",
        ),
      ),
    );
  }

  // RecentTasksTable / EmptyState — the two halves of the same slot. Keep
  // whichever matches your data and delete the other; an empty state built
  // from host.ui.Empty* costs nothing and stops your page from looking broken
  // before its first delivery.
  function RecentTasksTable({ tasks }) {
    return h(
      Table,
      null,
      h(
        TableHeader,
        null,
        h(
          TableRow,
          null,
          h(TableHead, null, "Task"),
          h(TableHead, { className: "w-32 text-right" }, "Seen"),
        ),
      ),
      h(
        TableBody,
        null,
        tasks.map((task) =>
          h(
            TableRow,
            { key: `${task.taskId}-${task.seenAt}` },
            h(TableCell, { className: "font-medium" }, task.title),
            h(
              TableCell,
              { className: "text-muted-foreground text-right text-xs" },
              // host.utils.formatRelativeTime is locale-aware
              // (Intl.RelativeTimeFormat) in the user's active locale. A
              // hand-rolled "3 minutes ago" ladder is English-only by
              // construction and silently untranslated for everyone else.
              utils.formatRelativeTime(task.seenAt),
            ),
          ),
        ),
      ),
    );
  }

  function EmptyState() {
    return h(
      Empty,
      { id: "template-page-empty" },
      h(
        EmptyHeader,
        null,
        h(EmptyMedia, { variant: "icon" }, icon(h, INBOX_PATH)),
        h(EmptyTitle, null, "No tasks created yet"),
        h(
          EmptyDescription,
          null,
          "This page fills in as tasks are created while it is open.",
        ),
      ),
    );
  }

  return function PluginPage() {
    const tasks = useRecentTasks(host.React);
    const isEmpty = tasks.length === 0;

    // One action, wired to both toast variants. host.toast is imperative — the
    // host mounts the single <Toaster/>, so there is nothing to render and it
    // works from anywhere, including inside host.openModal content.
    //
    // The button stays enabled when the buffer is empty on purpose: that is
    // what makes the .error path reachable. toast.error renders like any other
    // variant and logs `[plugins] toast.error from "<id>"` to the console, but
    // deliberately files NO backend error report — kandev's error log is for
    // kandev's own faults, not a plugin reporting an expected condition.
    const onClear = () => {
      if (isEmpty) {
        toast.error("Nothing to clear yet");
        return;
      }
      const cleared = tasks.length;
      publishRecentTasks([]);
      toast.success(`Cleared ${cleared} row${cleared === 1 ? "" : "s"}`);
    };

    return h(
      "div",
      { className: "p-4 max-w-2xl" },
      h(
        Card,
        null,
        h(
          CardHeader,
          null,
          h(CardTitle, { id: "template-page-title" }, "Template plugin"),
          h(CardDescription, null, `The ${RECENT_LIMIT} most recent tasks created since this page loaded`),
          h(CardAction, null, h(AboutPopover)),
        ),
        h(
          CardContent,
          null,
          // host.ui.Progress takes a 0-100 value. Used here for what it is
          // actually good at: a bounded ratio. Don't reach for it to fake an
          // indeterminate spinner — host.ui.Spinner is that.
          h(
            "div",
            { className: "mb-4" },
            h(Progress, { id: "template-page-progress", value: (tasks.length / RECENT_LIMIT) * 100 }),
            h(
              "p",
              {
                // host.utils.cn is the host's own clsx + tailwind-merge
                // combiner, so conditional classes merge the same way they do
                // in the components they sit next to.
                className: utils.cn(
                  "mt-2 text-xs",
                  isEmpty ? "text-muted-foreground/60" : "text-muted-foreground",
                ),
              },
              `buffer ${tasks.length} of ${RECENT_LIMIT}`,
            ),
          ),
          isEmpty ? h(EmptyState) : h(RecentTasksTable, { tasks }),
          h(
            "div",
            { className: "mt-4 flex justify-end" },
            h(
              Button,
              {
                id: "template-page-clear",
                type: "button",
                variant: "outline",
                size: "sm",
                onClick: onClear,
              },
              "Clear",
            ),
          ),
        ),
      ),
    );
  };
}

// ---------------------------------------------------------------------------
// A component for the "chat-input-actions" slot: an icon button rendered in
// the chat composer toolbar, beside the model picker, mic, and send. The host
// passes { sessionId, taskId, taskTitle } as slotProps, so the button knows
// which task/session the user is looking at.
// ---------------------------------------------------------------------------
function makeChatToolbarAction(host) {
  const { jsx: h, ui } = host;
  const { Button, Tooltip, TooltipTrigger, TooltipContent } = ui;

  return function ChatToolbarAction({ slotProps }) {
    const ctx = slotProps || {};
    const label = ctx.taskTitle || ctx.taskId;
    const tooltip = label ? `Template — open page (task: ${label})` : "Template — open page";

    // A plain Tooltip needs no provider of your own: the app shell mounts one,
    // and host.openModal content gets its own, so this works inside a plugin
    // modal too. host.ui.TooltipProvider is exported only for when you want a
    // custom delayDuration over a dense cluster of tooltips.
    return h(
      Tooltip,
      null,
      h(
        TooltipTrigger,
        { asChild: true },
        h(
          Button,
          {
            id: "template-chat-action",
            type: "button",
            variant: "ghost",
            size: "icon",
            className: "h-7 w-7 cursor-pointer hover:bg-muted/40",
            "aria-label": tooltip,
            onClick: () => host.navigate("/template"),
          },
          icon(h, STAR_PATH),
        ),
      ),
      h(TooltipContent, null, tooltip),
    );
  };
}

// ---------------------------------------------------------------------------
// Registration. Keep only what your plugin uses.
// ---------------------------------------------------------------------------
window.registerKandevPlugin("kandev-plugin-template", {
  initialize(registry, host) {
    // A sidebar entry. `icon` is a curated host icon name; it also becomes the
    // default topbar icon for the route registered on the same path.
    registry.registerNavItem({
      id: "template",
      label: "Template",
      path: "/template",
      icon: "puzzle",
      section: "main",
    });

    // A native route. The topbar title/icon default to the nav item above;
    // here we add a subtitle. Pass { topbar: false } to own the whole page
    // chrome yourself (host.ui.PageTopbar is available for that).
    registry.registerRoute("/template", makePluginPage(host), {
      topbar: { subtitle: "A starter kandev plugin page" },
    });

    // A WS handler: fires for every task.created message the SPA receives,
    // regardless of which component is mounted, since the buffer lives at
    // module scope. Register handlers only for events you actually use.
    registry.registerWsHandler("task.created", recordTask);

    // A chat-composer toolbar button.
    registry.registerComponent("chat-input-actions", makeChatToolbarAction(host));
  },

  destroy() {
    // The host bulk-unregisters everything under this plugin's id; reset local
    // module state too so a re-enable starts clean.
    publishRecentTasks([]);
    recentListeners.clear();
  },
});
