import type {
  PluginHostApi,
  PluginIcon,
  PluginRegistry,
  RepositoryInspection,
  RepositoryProviderRegistration,
  ReviewSummary,
  ReviewTaskAssociation,
  ReviewTaskStatus,
} from "@kandev/plugin-sdk";
import { createSnapshotStore } from "./review-store";

export type SourceControlRecipeOptions = {
  providerId: string;
  label: string;
  icon?: PluginIcon;
  changeRequestNoun: string;
  order?: number;
  supportsDraft?: boolean;
  matchesURL?(url: string): boolean;
  parseReference(reference: string): string | null;
  toChangeRequestDetail(review: ReviewSummary): unknown;
};

export type SourceControlRecipeLifecycle = {
  destroy(): void;
};

type RepositoryPageResponse = {
  repositories?: unknown[];
  next_cursor?: unknown;
};

type RepositoryInspectionResponse = { repository?: unknown };
type BranchListResponse = { branches?: unknown[] };
type ReviewListResponse = { reviews?: unknown[] };
type ReviewAssociationListResponse = { associations?: unknown[] };
type CreateChangeRequestResponse = {
  url?: unknown;
  output?: unknown;
  linked?: unknown;
  association_error?: unknown;
};

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function nonNegativeInteger(value: unknown): number | undefined {
  const number = finiteNumber(value);
  return number !== undefined && Number.isInteger(number) && number >= 0 ? number : undefined;
}

function positiveInteger(value: unknown): number | undefined {
  const number = nonNegativeInteger(value);
  return number !== undefined && number > 0 ? number : undefined;
}

function normalizeTaskStatus(value: unknown): ReviewTaskStatus | undefined {
  const source = record(value);
  const number = positiveInteger(source.number);
  const state = text(source.state);
  const pipelineState = text(source.pipeline_state);
  if (
    number === undefined ||
    !(["open", "merged", "closed", "draft"] as const).includes(
      state as "open" | "merged" | "closed" | "draft",
    ) ||
    !(["success", "failure", "pending", "neutral"] as const).includes(
      pipelineState as "success" | "failure" | "pending" | "neutral",
    )
  ) {
    return undefined;
  }

  const checks = Array.isArray(source.checks)
    ? source.checks.flatMap((value) => {
        const check = record(value);
        const id = text(check.id);
        const label = text(check.label);
        const checkState = text(check.state);
        if (
          !id ||
          !label ||
          !(["success", "failure", "pending", "neutral"] as const).includes(
            checkState as "success" | "failure" | "pending" | "neutral",
          )
        ) {
          return [];
        }
        const detail = text(check.detail);
        const url = text(check.url);
        return [{
          id,
          label,
          state: checkState as "success" | "failure" | "pending" | "neutral",
          ...(detail ? { detail } : {}),
          ...(url ? { url } : {}),
        }];
      })
    : [];

  const reviewSource = record(source.review);
  const reviewState = text(reviewSource.state);
  const approved = nonNegativeInteger(reviewSource.approved);
  const required = nonNegativeInteger(reviewSource.required);
  const requested = nonNegativeInteger(reviewSource.requested);
  const review =
    approved !== undefined &&
    (["approved", "changes_requested", "pending"] as const).includes(
      reviewState as "approved" | "changes_requested" | "pending",
    )
      ? {
          state: reviewState as "approved" | "changes_requested" | "pending",
          approved,
          ...(required === undefined ? {} : { required }),
          ...(requested === undefined ? {} : { requested }),
        }
      : undefined;
  const unresolvedComments = nonNegativeInteger(source.unresolved_comments);
  const updatedAt = finiteNumber(source.updated_at);

  return {
    number,
    state: state as "open" | "merged" | "closed" | "draft",
    pipelineState: pipelineState as "success" | "failure" | "pending" | "neutral",
    checks,
    ...(review ? { review } : {}),
    ...(unresolvedComments === undefined ? {} : { unresolvedComments }),
    ...(updatedAt === undefined ? {} : { updatedAt }),
  };
}

function normalizeReview(providerId: string, value: unknown): ReviewSummary | null {
  const source = record(value);
  const reviewKey = text(source.review_key);
  const title = text(source.title);
  const url = text(source.url);
  const connectionScope = text(source.connection_scope);
  const repositoryId = text(source.repository_id);
  const changeRequestNumber = positiveInteger(source.change_request_number);
  if (
    !reviewKey ||
    !title ||
    !url ||
    !connectionScope ||
    !repositoryId ||
    changeRequestNumber === undefined
  ) {
    return null;
  }
  const state = text(source.state);
  const taskStatus = normalizeTaskStatus(source.task_status);
  return {
    providerId,
    reviewKey,
    title,
    url,
    connectionScope,
    repositoryId,
    changeRequestNumber,
    state,
    ...(taskStatus ? { taskStatus } : {}),
  };
}

function normalizeAssociation(providerId: string, value: unknown): ReviewTaskAssociation | null {
  const source = record(value);
  const taskId = text(source.task_id);
  const reviewKey = text(source.review_key);
  const connectionScope = text(source.connection_scope);
  const repositoryId = text(source.repository_id);
  const changeRequestNumber = positiveInteger(source.change_request_number);
  if (
    !taskId ||
    !reviewKey ||
    !connectionScope ||
    !repositoryId ||
    changeRequestNumber === undefined
  ) {
    return null;
  }
  return {
    providerId,
    taskId,
    reviewKey,
    connectionScope,
    repositoryId,
    changeRequestNumber,
  };
}

function normalizeRepository(providerId: string, value: unknown): RepositoryInspection | null {
  const source = record(value);
  const providerHost = text(source.provider_host);
  const ownerOrProject = text(source.owner_or_project);
  const repositoryId = text(source.repository_id);
  const repositoryName = text(source.name);
  const cloneUrl = text(source.clone_url);
  if (!providerHost || !ownerOrProject || !repositoryId || !repositoryName || !cloneUrl) return null;
  const providerScope = text(source.provider_scope);
  const defaultBranch = text(source.default_branch);
  return {
    providerId,
    providerHost,
    ...(providerScope ? { providerScope } : {}),
    ownerOrProject,
    repositoryId,
    repositoryName,
    cloneUrl,
    ...(defaultBranch ? { defaultBranch } : {}),
  };
}

function credentialFreeRepository(repository: RepositoryInspection) {
  return {
    provider_id: repository.providerId,
    provider_host: repository.providerHost,
    provider_scope: repository.providerScope ?? "",
    provider_repository_id: repository.repositoryId,
    owner_or_project: repository.ownerOrProject,
    name: repository.repositoryName,
    clone_url: repository.cloneUrl,
    default_branch: repository.defaultBranch ?? "",
  };
}

export function registerSourceControlRecipe(
  registry: PluginRegistry,
  host: PluginHostApi,
  options: SourceControlRecipeOptions,
): SourceControlRecipeLifecycle {
  const reviewStore = createSnapshotStore<string, ReviewSummary>();
  const associationStore = createSnapshotStore<string, ReviewTaskAssociation>();
  const overlays = new Set<{ close(): void }>();

  async function refreshReviews(taskId: string, signal: AbortSignal, workspaceId?: string) {
    const token = reviewStore.beginRefresh(taskId);
    signal.throwIfAborted();
    const response = await host.api.invokeAction<ReviewListResponse>(
      "change_requests.get",
      { ...(workspaceId ? { workspaceId } : {}), taskId },
      { signal },
    );
    signal.throwIfAborted();
    reviewStore.commit(
      taskId,
      token,
      (response.reviews ?? []).flatMap((review) => {
        const normalized = normalizeReview(options.providerId, review);
        return normalized ? [normalized] : [];
      }),
    );
  }

  async function refreshAssociations(workspaceId: string, signal: AbortSignal) {
    const token = associationStore.beginRefresh(workspaceId);
    signal.throwIfAborted();
    const response = await host.api.invokeAction<ReviewAssociationListResponse>(
      "change_requests.associations",
      { workspaceId },
      { signal },
    );
    signal.throwIfAborted();
    associationStore.commit(
      workspaceId,
      token,
      (response.associations ?? []).flatMap((association) => {
        const normalized = normalizeAssociation(options.providerId, association);
        return normalized ? [normalized] : [];
      }),
    );
  }

  async function refreshAfterMutation(workspaceId: string, taskId: string, signal: AbortSignal) {
    try {
      await Promise.all([
        refreshReviews(taskId, signal, workspaceId),
        refreshAssociations(workspaceId, signal),
      ]);
    } catch {
      // The mutation already succeeded. Its known outcome wins even when the
      // supplied signal cancels this optional refresh; the host can retry it.
    }
  }

  const repositoryProvider: RepositoryProviderRegistration = {
    id: options.providerId,
    label: options.label,
    ...(options.icon ? { icon: options.icon } : {}),
    ...(options.matchesURL ? { matchesURL: options.matchesURL } : {}),
    ...(options.supportsDraft === undefined ? {} : { supportsDraft: options.supportsDraft }),

    async listRepositories({ workspaceId, query = "", cursor = "", limit = 100, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction<RepositoryPageResponse>(
        "repositories.list",
        { workspaceId, body: { query, cursor, limit } },
        { signal },
      );
      signal.throwIfAborted();
      return {
        repositories: (response.repositories ?? []).flatMap((repository) => {
          const normalized = normalizeRepository(options.providerId, repository);
          return normalized ? [normalized] : [];
        }),
        nextCursor: text(response.next_cursor) || undefined,
      };
    },

    async listBranches({ workspaceId, repository, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction<BranchListResponse>(
        "repositories.branches",
        { workspaceId, body: { repository: credentialFreeRepository(repository) } },
        { signal },
      );
      signal.throwIfAborted();
      return (response.branches ?? []).flatMap((branch) => {
        const name = text(record(branch).name);
        return name ? [{ name }] : [];
      });
    },

    async inspectURL({ workspaceId, url, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction<RepositoryInspectionResponse>(
        "repositories.inspect",
        { workspaceId, body: { url } },
        { signal },
      );
      signal.throwIfAborted();
      return normalizeRepository(options.providerId, response.repository);
    },

    async createChangeRequest({
      workspaceId,
      taskId,
      sessionId,
      repositoryId,
      title,
      body,
      baseBranch,
      draft,
      signal,
    }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction<CreateChangeRequestResponse>(
        "change_requests.create",
        {
          workspaceId,
          taskId,
          sessionId,
          repositoryId,
          body: {
            title,
            description: body,
            destination: baseBranch ?? "",
            draft,
          },
        },
        { signal },
      );
      const url = text(response.url);
      if (!url) throw new Error("source-control recipe: create response did not include a URL");
      await refreshAfterMutation(workspaceId, taskId, signal);
      const output = text(response.output);
      const associationError = text(response.association_error);
      return {
        url,
        provider: options.providerId,
        ...(output ? { output } : {}),
        ...(typeof response.linked === "boolean" ? { linked: response.linked } : {}),
        ...(associationError ? { associationError } : {}),
      };
    },
  };

  registry.registerRepositoryProvider(repositoryProvider);
  registry.registerTaskAction({
    id: `${options.providerId}-link-change-request`,
    label: `${options.label} ${options.changeRequestNoun}`,
    ...(options.icon ? { icon: options.icon } : {}),
    placement: "link",
    singleTaskOnly: true,
    async run(context) {
      const dialog = host.openTaskLinkDialog({
        title: `Link ${options.label} ${options.changeRequestNoun}`,
        description: `Enter a ${options.label} ${options.changeRequestNoun} URL or canonical reference.`,
        inputLabel: options.changeRequestNoun,
        emptyError: `Enter a valid ${options.label} ${options.changeRequestNoun} reference.`,
        failureMessage: `Failed to link ${options.label} ${options.changeRequestNoun}.`,
        successMessage: `${options.label} ${options.changeRequestNoun} linked`,
        inputTestId: `${options.providerId}-review-reference`,
        errorTestId: `${options.providerId}-review-reference-error`,
        submitTestId: `${options.providerId}-review-reference-submit`,
        async onSubmit(reference, signal) {
          signal.throwIfAborted();
          const parsed = options.parseReference(reference);
          if (!parsed) {
            throw new Error(`Enter a valid ${options.label} ${options.changeRequestNoun} reference.`);
          }
          await host.api.invokeAction(
            "change_requests.link",
            {
              workspaceId: context.workspaceId,
              taskId: context.taskId,
              body: { reference: parsed },
            },
            { signal },
          );
          await refreshAfterMutation(context.workspaceId, context.taskId, signal);
        },
      });
      overlays.add(dialog);
    },
  });
  registry.registerReviewProvider({
    id: options.providerId,
    label: options.label,
    ...(options.icon ? { icon: options.icon } : {}),
    changeRequestNoun: options.changeRequestNoun,
    order: options.order ?? 100,
    getSnapshot: (taskId) => reviewStore.get(taskId),
    subscribe: (taskId, listener) => reviewStore.subscribe(taskId, listener),
    refresh: (taskId, signal) => refreshReviews(taskId, signal),
    getAssociationSnapshot: (workspaceId) => associationStore.get(workspaceId),
    subscribeAssociations: (workspaceId, listener) =>
      associationStore.subscribe(workspaceId, listener),
    refreshAssociations,
    async unlink({
      workspaceId,
      taskId,
      connectionScope,
      repositoryId,
      changeRequestNumber,
      signal,
    }) {
      const number =
        typeof changeRequestNumber === "string" && /^\d+$/.test(changeRequestNumber)
          ? positiveInteger(Number(changeRequestNumber))
          : positiveInteger(changeRequestNumber);
      if (!connectionScope.trim() || !repositoryId.trim() || number === undefined) {
        throw new Error("source-control recipe: cannot unlink an incomplete review identity");
      }
      signal.throwIfAborted();
      await host.api.invokeAction(
        "change_requests.unlink",
        {
          workspaceId,
          taskId,
          body: {
            connection_scope: connectionScope,
            repository_id: repositoryId,
            number,
          },
        },
        { signal },
      );
    },
    ReviewPanel: (props) => {
      const review = reviewStore.get(props.taskId).find(
        (candidate) =>
          candidate.reviewKey === props.reviewKey &&
          candidate.connectionScope === props.connectionScope &&
          candidate.repositoryId === props.repositoryId &&
          String(candidate.changeRequestNumber) === String(props.changeRequestNumber),
      );
      return host.jsx(host.ui.ChangeRequestDetail, {
        detail: review ? options.toChangeRequestDetail(review) : null,
        presentation: props.presentation,
        loading: false,
        error: null,
      });
    },
  });
  return {
    destroy() {
      overlays.forEach((overlay) => overlay.close());
      overlays.clear();
      reviewStore.clear();
      associationStore.clear();
    },
  };
}
