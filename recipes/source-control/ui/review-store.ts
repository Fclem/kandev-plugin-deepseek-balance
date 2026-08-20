export type RefreshToken = Readonly<{ epoch: number; version: number }>;

export interface SnapshotStore<Key, Value> {
  get(key: Key): readonly Value[];
  subscribe(key: Key, listener: () => void): () => void;
  beginRefresh(key: Key): RefreshToken;
  commit(key: Key, token: RefreshToken, values: readonly Value[]): boolean;
  clear(): void;
}

/**
 * Small external store for registered review snapshots. Version tokens reject
 * out-of-order results; epoch changes reject every result started before unload.
 */
export function createSnapshotStore<Key, Value>(): SnapshotStore<Key, Value> {
  const snapshots = new Map<Key, readonly Value[]>();
  const listeners = new Map<Key, Set<() => void>>();
  const versions = new Map<Key, number>();
  let epoch = 0;

  return {
    get: (key) => snapshots.get(key) ?? [],

    subscribe(key, listener) {
      const keyListeners = listeners.get(key) ?? new Set<() => void>();
      keyListeners.add(listener);
      listeners.set(key, keyListeners);
      return () => {
        keyListeners.delete(listener);
        if (keyListeners.size === 0) listeners.delete(key);
      };
    },

    beginRefresh(key) {
      const version = (versions.get(key) ?? 0) + 1;
      versions.set(key, version);
      return { epoch, version };
    },

    commit(key, token, values) {
      if (token.epoch !== epoch || versions.get(key) !== token.version) return false;
      snapshots.set(key, [...values]);
      listeners.get(key)?.forEach((listener) => listener());
      return true;
    },

    clear() {
      epoch += 1;
      versions.clear();
      snapshots.clear();
      listeners.forEach((keyListeners) => keyListeners.forEach((listener) => listener()));
      listeners.clear();
    },
  };
}
