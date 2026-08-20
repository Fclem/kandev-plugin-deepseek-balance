import type {
  PluginHostApi,
  PluginRegistry,
  RepositoryProviderRegistration,
} from "@kandev/plugin-sdk";
import { describe, expect, it, vi } from "vitest";
import { registerSourceControlRecipe } from "./register";

type TaskAction = Parameters<PluginRegistry["registerTaskAction"]>[0];
type TaskLinkDialog = Parameters<PluginHostApi["openTaskLinkDialog"]>[0];
type ReviewProvider = Parameters<PluginRegistry["registerReviewProvider"]>[0];
type ActionResponder = (key: string, input?: unknown, options?: unknown) => unknown | Promise<unknown>;

function harness(actionResult: unknown | ActionResponder) {
  let repositoryProvider: RepositoryProviderRegistration | undefined;
  let taskAction: TaskAction | undefined;
  let taskLinkDialog: TaskLinkDialog | undefined;
  const taskLinkClose = vi.fn();
  let reviewProvider: ReviewProvider | undefined;
  const registry = {
    registerRepositoryProvider: vi.fn((provider: RepositoryProviderRegistration) => {
      repositoryProvider = provider;
    }),
    registerTaskAction: vi.fn((action: TaskAction) => {
      taskAction = action;
    }),
    registerReviewProvider: vi.fn((provider: ReviewProvider) => {
      reviewProvider = provider;
    }),
  } as unknown as PluginRegistry;
  const invokeAction = vi.fn(async (key: string, input?: unknown, options?: unknown) =>
    typeof actionResult === "function"
      ? (actionResult as ActionResponder)(key, input, options)
      : actionResult,
  );
  const host = {
    api: { invokeAction },
    openTaskLinkDialog: vi.fn((dialog: TaskLinkDialog) => {
      taskLinkDialog = dialog;
      return { close: taskLinkClose };
    }),
    jsx: vi.fn(),
    ui: { ChangeRequestDetail: Symbol("ChangeRequestDetail") },
  } as unknown as PluginHostApi;

  const lifecycle = registerSourceControlRecipe(registry, host, {
    providerId: "acme",
    label: "Acme",
    changeRequestNoun: "change request",
    parseReference: (value) => value.trim() || null,
    toChangeRequestDetail: (review) => review,
  });

  return {
    registry,
    host,
    invokeAction,
    lifecycle,
    get repositoryProvider() { return repositoryProvider; },
    get taskAction() { return taskAction; },
    get taskLinkDialog() { return taskLinkDialog; },
    taskLinkClose,
    get reviewProvider() { return reviewProvider; },
  };
}

describe("registerSourceControlRecipe", () => {
  it("forwards server-side repository search and pagination with the host signal", async () => {
    const test = harness({
      repositories: [
        {
          provider_id: "untrusted-result-provider",
          provider_host: "code.example",
          provider_scope: "connection-7",
          owner_or_project: "tools",
          repository_id: "immutable-repository-9",
          name: "widgets",
          clone_url: "https://code.example/tools/widgets.git",
          default_branch: "main",
        },
      ],
      next_cursor: "opaque-page-2",
    });
    const signal = new AbortController().signal;

    const result = await test.repositoryProvider!.listRepositories({
      workspaceId: "workspace-1",
      query: "widgets",
      cursor: "opaque-page-1",
      limit: 25,
      signal,
    });

    expect(test.invokeAction).toHaveBeenCalledWith(
      "repositories.list",
      {
        workspaceId: "workspace-1",
        body: { query: "widgets", cursor: "opaque-page-1", limit: 25 },
      },
      { signal },
    );
    expect(result).toEqual({
      repositories: [
        {
          providerId: "acme",
          providerHost: "code.example",
          providerScope: "connection-7",
          ownerOrProject: "tools",
          repositoryId: "immutable-repository-9",
          repositoryName: "widgets",
          cloneUrl: "https://code.example/tools/widgets.git",
          defaultBranch: "main",
        },
      ],
      nextCursor: "opaque-page-2",
    });
  });

  it("routes URL inspection through the verified workspace action", async () => {
    const test = harness({
      repository: {
        provider_host: "code.example",
        provider_scope: "connection-7",
        owner_or_project: "tools",
        repository_id: "immutable-repository-9",
        name: "widgets",
        clone_url: "https://code.example/tools/widgets.git",
      },
    });
    const signal = new AbortController().signal;

    const inspected = await test.repositoryProvider!.inspectURL({
      workspaceId: "workspace-1",
      url: "https://code.example/tools/widgets",
      signal,
    });

    expect(test.invokeAction).toHaveBeenCalledWith(
      "repositories.inspect",
      {
        workspaceId: "workspace-1",
        body: { url: "https://code.example/tools/widgets" },
      },
      { signal },
    );
    expect(inspected).toMatchObject({
      providerId: "acme",
      providerScope: "connection-7",
      repositoryId: "immutable-repository-9",
    });
  });

  it("lists branches through a credential-free immutable repository descriptor", async () => {
    const test = harness({
      branches: [
        { name: "main", commit: "abc123", is_default: true },
        { name: "feature/provider-hooks", commit: "def456" },
      ],
    });
    const signal = new AbortController().signal;

    const branches = await test.repositoryProvider!.listBranches({
      workspaceId: "workspace-1",
      repository: {
        providerId: "acme",
        providerHost: "code.example",
        providerScope: "connection-7",
        ownerOrProject: "tools",
        repositoryId: "immutable-repository-9",
        repositoryName: "widgets",
        cloneUrl: "https://code.example/tools/widgets.git",
        defaultBranch: "main",
      },
      signal,
    });

    expect(test.invokeAction).toHaveBeenCalledWith(
      "repositories.branches",
      {
        workspaceId: "workspace-1",
        body: {
          repository: {
            provider_id: "acme",
            provider_host: "code.example",
            provider_scope: "connection-7",
            provider_repository_id: "immutable-repository-9",
            owner_or_project: "tools",
            name: "widgets",
            clone_url: "https://code.example/tools/widgets.git",
            default_branch: "main",
          },
        },
      },
      { signal },
    );
    expect(branches).toEqual([{ name: "main" }, { name: "feature/provider-hooks" }]);
  });

  it("creates a change request from verified host selectors and returns partial-link outcomes", async () => {
    const test = harness({
      url: "https://code.example/tools/widgets/changes/42",
      provider: "untrusted-result-provider",
      linked: false,
      association_error: "Created remotely; link it again after reconnecting.",
    });
    const signal = new AbortController().signal;

    const result = await test.repositoryProvider!.createChangeRequest!({
      workspaceId: "workspace-1",
      taskId: "task-2",
      sessionId: "session-3",
      repositoryId: "host-repository-4",
      repository: {
        id: "host-repository-4",
        workspace_id: "workspace-1",
        name: "widgets",
        provider: "acme",
      },
      title: "Demonstrate provider hooks",
      body: "Provider-neutral description",
      baseBranch: "main",
      draft: true,
      signal,
    });

    expect(test.invokeAction).toHaveBeenCalledWith(
      "change_requests.create",
      {
        workspaceId: "workspace-1",
        taskId: "task-2",
        sessionId: "session-3",
        repositoryId: "host-repository-4",
        body: {
          title: "Demonstrate provider hooks",
          description: "Provider-neutral description",
          destination: "main",
          draft: true,
        },
      },
      { signal },
    );
    expect(result).toEqual({
      url: "https://code.example/tools/widgets/changes/42",
      provider: "acme",
      linked: false,
      associationError: "Created remotely; link it again after reconnecting.",
    });
  });

  it("registers a host-owned Link action and forwards cancellation to the link action", async () => {
    const test = harness({ linked: true });
    await test.taskAction!.run({
      workspaceId: "workspace-1",
      taskId: "task-2",
      repositories: [],
      pathname: "/tasks/task-2",
      presentation: "mobile",
    });
    const signal = new AbortController().signal;

    await test.taskLinkDialog!.onSubmit("  tools/widgets#42  ", signal);

    expect(test.taskAction).toMatchObject({
      id: "acme-link-change-request",
      label: "Acme change request",
      placement: "link",
      singleTaskOnly: true,
    });
    expect(test.invokeAction).toHaveBeenCalledWith(
      "change_requests.link",
      {
        workspaceId: "workspace-1",
        taskId: "task-2",
        body: { reference: "tools/widgets#42" },
      },
      { signal },
    );
  });

  it("publishes semantic review status and CI checks for host-owned surfaces", async () => {
    const test = harness((key: string) => {
      if (key !== "change_requests.get") throw new Error(`unexpected action: ${key}`);
      return {
        reviews: [
          {
            provider_id: "untrusted-result-provider",
            review_key: "connection-7/repository-9#42",
            title: "Provider-neutral hooks",
            url: "https://code.example/tools/widgets/changes/42",
            connection_scope: "connection-7",
            repository_id: "repository-9",
            change_request_number: 42,
            state: "open",
            task_status: {
              number: 42,
              state: "open",
              pipeline_state: "failure",
              checks: [
                {
                  id: "build",
                  label: "Build",
                  state: "failure",
                  detail: "One job failed",
                  url: "https://ci.example/build/7",
                },
              ],
              review: {
                state: "changes_requested",
                approved: 1,
                required: 2,
                requested: 1,
              },
              unresolved_comments: 3,
              updated_at: 1_765_000_000_000,
            },
          },
          {
            review_key: "missing-immutable-identity",
            title: "Ignored",
            url: "https://code.example/ignored",
            change_request_number: 9,
          },
        ],
      };
    });
    const signal = new AbortController().signal;

    await test.reviewProvider!.refresh("task-2", signal);

    expect(test.invokeAction).toHaveBeenCalledWith(
      "change_requests.get",
      { taskId: "task-2" },
      { signal },
    );
    expect(test.reviewProvider!.getSnapshot("task-2")).toEqual([
      {
        providerId: "acme",
        reviewKey: "connection-7/repository-9#42",
        title: "Provider-neutral hooks",
        url: "https://code.example/tools/widgets/changes/42",
        connectionScope: "connection-7",
        repositoryId: "repository-9",
        changeRequestNumber: 42,
        state: "open",
        taskStatus: {
          number: 42,
          state: "open",
          pipelineState: "failure",
          checks: [
            {
              id: "build",
              label: "Build",
              state: "failure",
              detail: "One job failed",
              url: "https://ci.example/build/7",
            },
          ],
          review: {
            state: "changes_requested",
            approved: 1,
            required: 2,
            requested: 1,
          },
          unresolvedComments: 3,
          updatedAt: 1_765_000_000_000,
        },
      },
    ]);
  });

  it("keeps complete immutable association identity through refresh and unlink", async () => {
    const test = harness((key: string) => {
      if (key === "change_requests.associations") {
        return {
          associations: [
            {
              provider_id: "untrusted-result-provider",
              task_id: "task-2",
              review_key: "display-key-can-change",
              connection_scope: "connection-7",
              repository_id: "repository-9",
              change_request_number: 42,
            },
            {
              task_id: "task-ignored",
              review_key: "incomplete",
              repository_id: "repository-9",
              change_request_number: 9,
            },
          ],
        };
      }
      if (key === "change_requests.unlink") return { unlinked: true };
      throw new Error(`unexpected action: ${key}`);
    });
    const signal = new AbortController().signal;

    await test.reviewProvider!.refreshAssociations!("workspace-1", signal);

    expect(test.reviewProvider!.getAssociationSnapshot!("workspace-1")).toEqual([
      {
        providerId: "acme",
        taskId: "task-2",
        reviewKey: "display-key-can-change",
        connectionScope: "connection-7",
        repositoryId: "repository-9",
        changeRequestNumber: 42,
      },
    ]);

    await test.reviewProvider!.unlink!({
      workspaceId: "workspace-1",
      taskId: "task-2",
      reviewKey: "new-display-key",
      connectionScope: "connection-7",
      repositoryId: "repository-9",
      changeRequestNumber: 42,
      signal,
    });

    expect(test.invokeAction).toHaveBeenLastCalledWith(
      "change_requests.unlink",
      {
        workspaceId: "workspace-1",
        taskId: "task-2",
        body: {
          connection_scope: "connection-7",
          repository_id: "repository-9",
          number: 42,
        },
      },
      { signal },
    );
  });

  it("uses the same host review detail surface on desktop and mobile", async () => {
    const test = harness({
      reviews: [
        {
          review_key: "connection-7/repository-9#42",
          title: "Provider-neutral hooks",
          url: "https://code.example/tools/widgets/changes/42",
          connection_scope: "connection-7",
          repository_id: "repository-9",
          change_request_number: 42,
          state: "open",
        },
      ],
    });
    await test.reviewProvider!.refresh("task-2", new AbortController().signal);

    const common = {
      panelId: "review-panel",
      workspaceId: "workspace-1",
      taskId: "task-2",
      reviewKey: "connection-7/repository-9#42",
      connectionScope: "connection-7",
      repositoryId: "repository-9",
      changeRequestNumber: 42,
    };
    test.reviewProvider!.ReviewPanel({ ...common, presentation: "desktop" });
    test.reviewProvider!.ReviewPanel({ ...common, presentation: "mobile", sessionId: "session-3" });

    expect(test.host.jsx).toHaveBeenNthCalledWith(
      1,
      test.host.ui.ChangeRequestDetail,
      expect.objectContaining({
        presentation: "desktop",
        detail: expect.objectContaining({
          providerId: "acme",
          connectionScope: "connection-7",
          repositoryId: "repository-9",
          changeRequestNumber: 42,
        }),
      }),
    );
    expect(test.host.jsx).toHaveBeenNthCalledWith(
      2,
      test.host.ui.ChangeRequestDetail,
      expect.objectContaining({
        presentation: "mobile",
        detail: expect.objectContaining({
          providerId: "acme",
          connectionScope: "connection-7",
          repositoryId: "repository-9",
          changeRequestNumber: 42,
        }),
      }),
    );
  });

  it("fences late refresh results after plugin unload", async () => {
    let resolveResponse!: (value: unknown) => void;
    const response = new Promise<unknown>((resolve) => {
      resolveResponse = resolve;
    });
    const test = harness(() => response);
    const refresh = test.reviewProvider!.refresh("task-2", new AbortController().signal);

    test.lifecycle.destroy();
    resolveResponse({
      reviews: [
        {
          review_key: "connection-7/repository-9#42",
          title: "Late result",
          url: "https://code.example/tools/widgets/changes/42",
          connection_scope: "connection-7",
          repository_id: "repository-9",
          change_request_number: 42,
          state: "open",
        },
      ],
    });
    await refresh;

    expect(test.reviewProvider!.getSnapshot("task-2")).toEqual([]);
  });

  it("preserves a remote create outcome when best-effort snapshot refresh fails", async () => {
    const test = harness((key: string) => {
      if (key === "change_requests.create") {
        return {
          url: "https://code.example/tools/widgets/changes/42",
          linked: false,
          association_error: "task association could not be saved",
        };
      }
      if (key === "change_requests.get" || key === "change_requests.associations") {
        throw new Error("refresh unavailable");
      }
      throw new Error(`unexpected action: ${key}`);
    });
    const signal = new AbortController().signal;

    const result = await test.repositoryProvider!.createChangeRequest!({
      workspaceId: "workspace-1",
      taskId: "task-2",
      sessionId: "session-3",
      repositoryId: "host-repository-4",
      repository: {
        id: "host-repository-4",
        workspace_id: "workspace-1",
        name: "widgets",
        provider: "acme",
      },
      title: "Provider hooks",
      body: "Description",
      baseBranch: "main",
      draft: false,
      signal,
    });

    expect(result).toMatchObject({
      url: "https://code.example/tools/widgets/changes/42",
      linked: false,
      associationError: "task association could not be saved",
    });
    expect(test.invokeAction.mock.calls.map(([key]) => key)).toEqual([
      "change_requests.create",
      "change_requests.get",
      "change_requests.associations",
    ]);
  });

  it("keeps a completed mutation outcome when cancellation stops only its refresh", async () => {
    const controller = new AbortController();
    const test = harness((key: string) => {
      if (key === "change_requests.create") {
        return { url: "https://code.example/tools/widgets/changes/42", linked: true };
      }
      if (key === "change_requests.get") {
        controller.abort();
        throw controller.signal.reason;
      }
      throw new Error(`unexpected action: ${key}`);
    });

    const result = await test.repositoryProvider!.createChangeRequest!({
      workspaceId: "workspace-1",
      taskId: "task-2",
      sessionId: "session-3",
      repositoryId: "host-repository-4",
      repository: {
        id: "host-repository-4",
        workspace_id: "workspace-1",
        name: "widgets",
        provider: "acme",
      },
      title: "Provider hooks",
      body: "Description",
      draft: false,
      signal: controller.signal,
    });

    expect(result).toMatchObject({
      url: "https://code.example/tools/widgets/changes/42",
      linked: true,
    });
    expect(controller.signal.aborted).toBe(true);
  });

  it("does not start provider I/O for an already-canceled callback", async () => {
    const test = harness({ repositories: [] });
    const controller = new AbortController();
    controller.abort();

    await expect(test.repositoryProvider!.listRepositories({
      workspaceId: "workspace-1",
      signal: controller.signal,
    })).rejects.toMatchObject({ name: "AbortError" });

    expect(test.invokeAction).not.toHaveBeenCalled();
  });

  it("closes plugin-owned Link dialogs during teardown", async () => {
    const test = harness({ linked: true });
    await test.taskAction!.run({
      workspaceId: "workspace-1",
      taskId: "task-2",
      repositories: [],
      pathname: "/tasks/task-2",
      presentation: "desktop",
    });

    test.lifecycle.destroy();

    expect(test.taskLinkClose).toHaveBeenCalledOnce();
  });
});
