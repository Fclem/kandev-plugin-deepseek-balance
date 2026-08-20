import { describe, expect, it, vi } from "vitest";
import { createSnapshotStore } from "./review-store";

describe("createSnapshotStore", () => {
  it("rejects an older refresh result after a newer refresh starts", () => {
    const store = createSnapshotStore<string, { id: string }>();
    const listener = vi.fn();
    store.subscribe("task-1", listener);

    const oldRefresh = store.beginRefresh("task-1");
    const newRefresh = store.beginRefresh("task-1");

    expect(store.commit("task-1", oldRefresh, [{ id: "stale" }])).toBe(false);
    expect(store.get("task-1")).toEqual([]);
    expect(listener).not.toHaveBeenCalled();

    expect(store.commit("task-1", newRefresh, [{ id: "current" }])).toBe(true);
    expect(store.get("task-1")).toEqual([{ id: "current" }]);
    expect(listener).toHaveBeenCalledOnce();
  });
});
