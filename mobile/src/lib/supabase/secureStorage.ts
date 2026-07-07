/**
 * expo-secure-store adapter for the Supabase session (Story 1.6, storage decision).
 *
 * Architecture requires the refresh token to live in OS secure storage. SecureStore has a
 * ~2 KB per-value limit and a full Supabase session JSON can exceed it, so this adapter
 * CHUNKS values: the primary key holds a `chunks:<n>` marker and the payload is split across
 * `<key>.0 … <key>.(n-1)`. This satisfies the arch's intent while the value size stays safe.
 *
 * NOTE: the 401 interceptor and every AC-7 test are storage-agnostic (they mock `supabase.auth`),
 * so this adapter is isolated from the interceptor logic and its choice is reversible.
 */

import * as SecureStore from 'expo-secure-store';

/** Conservative per-chunk size in characters (SecureStore warns above ~2048 bytes). */
const CHUNK_SIZE = 1800;
/** Marker distinguishing a chunk-count from a raw value written outside this adapter. */
const MARKER = 'chunks:';

async function removeChunks(key: string): Promise<void> {
  const meta = await SecureStore.getItemAsync(key);
  if (meta == null || !meta.startsWith(MARKER)) return;
  const count = Number.parseInt(meta.slice(MARKER.length), 10);
  if (!Number.isInteger(count)) return;
  for (let i = 0; i < count; i += 1) {
    await SecureStore.deleteItemAsync(`${key}.${i}`);
  }
}

export const secureStorageAdapter = {
  async getItem(key: string): Promise<string | null> {
    const meta = await SecureStore.getItemAsync(key);
    if (meta == null) return null;
    // Defensive: a value not written by this adapter (no marker) is returned verbatim.
    if (!meta.startsWith(MARKER)) return meta;

    const count = Number.parseInt(meta.slice(MARKER.length), 10);
    if (!Number.isInteger(count) || count <= 0) return null;

    const parts: string[] = [];
    for (let i = 0; i < count; i += 1) {
      const part = await SecureStore.getItemAsync(`${key}.${i}`);
      // A missing chunk means a corrupt/partial write — treat as absent so the SDK re-auths.
      if (part == null) return null;
      parts.push(part);
    }
    return parts.join('');
  },

  async setItem(key: string, value: string): Promise<void> {
    // Clear any prior chunks first — the new value may be shorter (fewer chunks).
    await removeChunks(key);
    const count = Math.max(1, Math.ceil(value.length / CHUNK_SIZE));
    for (let i = 0; i < count; i += 1) {
      await SecureStore.setItemAsync(`${key}.${i}`, value.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE));
    }
    await SecureStore.setItemAsync(key, `${MARKER}${count}`);
  },

  async removeItem(key: string): Promise<void> {
    await removeChunks(key);
    await SecureStore.deleteItemAsync(key);
  },
};
