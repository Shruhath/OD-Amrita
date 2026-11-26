/**
 * expo-secure-store adapter for the Supabase session.
 *
 * Architecture requires the refresh token to live in OS secure storage. SecureStore has a
 * ~2 KB per-value limit and a full Supabase session JSON can exceed it, so this adapter
 * CHUNKS values: the primary key holds a `chunks:<n>` marker and the payload is split across
 * `<key>.0 … <key>.(n-1)`. This satisfies the arch's intent while the value size stays safe.
 *
 * Robustness (code-review 2026-07-07):
 *  - Chunk boundaries are measured in UTF-8 BYTES (not UTF-16 code units) and never split a code
 *    point, so multibyte content can't push a chunk past the byte limit.
 *  - Writes are marker-FIRST: an interrupted write reads back as ABSENT (a missing chunk → null),
 *    never as a TRUNCATED non-null value that would corrupt session JSON.
 *  - All operations are SERIALIZED through a per-adapter queue so overlapping set/get/remove
 *    calls can't interleave into a torn read or a marker/chunk mismatch.
 *  - The marker is parsed strictly (`chunks:<digits>`) so a stray value can't be misread as one.
 *
 * NOTE: the 401 interceptor and every AC-7 test are storage-agnostic (they mock `supabase.auth`),
 * so this adapter is isolated from the interceptor logic and its choice is reversible.
 */

import * as SecureStore from 'expo-secure-store';

/** Conservative per-chunk budget in UTF-8 bytes (SecureStore warns above ~2048 bytes). */
const CHUNK_BYTES = 1800;
/** Marker distinguishing a chunk-count from a raw value written outside this adapter. */
const MARKER = 'chunks:';

/** UTF-8 byte length of a single code point. */
function codePointBytes(cp: number): number {
  return cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
}

/**
 * Split a string into pieces each ≤ maxBytes UTF-8 bytes, never splitting a code point.
 * Always returns at least one piece (an empty string yields `['']`) so the chunk count is ≥ 1.
 */
function chunkByBytes(value: string, maxBytes: number): string[] {
  const chunks: string[] = [];
  let current = '';
  let currentBytes = 0;
  for (const ch of value) {
    const b = codePointBytes(ch.codePointAt(0) as number);
    if (currentBytes + b > maxBytes && current.length > 0) {
      chunks.push(current);
      current = '';
      currentBytes = 0;
    }
    current += ch;
    currentBytes += b;
  }
  chunks.push(current);
  return chunks;
}

/** Parse a strict `chunks:<non-negative-integer>` marker; anything else is not our marker. */
function parseMarker(meta: string): number | null {
  if (!meta.startsWith(MARKER)) return null;
  const rest = meta.slice(MARKER.length);
  if (!/^\d+$/.test(rest)) return null;
  return Number.parseInt(rest, 10);
}

async function removeChunks(key: string): Promise<void> {
  const meta = await SecureStore.getItemAsync(key);
  if (meta == null) return;
  const count = parseMarker(meta);
  if (count == null) return;
  for (let i = 0; i < count; i += 1) {
    await SecureStore.deleteItemAsync(`${key}.${i}`);
  }
}

// Serialize every operation through a single promise queue: SecureStore calls are async and
// Supabase can persist a session while another read/write is mid-flight. Chaining guarantees a
// setItem's marker+chunk writes complete before the next op observes them (no torn reads).
let opQueue: Promise<unknown> = Promise.resolve();
function serialize<T>(op: () => Promise<T>): Promise<T> {
  const run = opQueue.then(op, op);
  opQueue = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

export const secureStorageAdapter = {
  getItem(key: string): Promise<string | null> {
    return serialize(async () => {
      const meta = await SecureStore.getItemAsync(key);
      if (meta == null) return null;
      // Defensive: a value not written by this adapter (no valid marker) is returned verbatim.
      const count = parseMarker(meta);
      if (count == null) return meta;
      if (count <= 0) return null;

      const parts: string[] = [];
      for (let i = 0; i < count; i += 1) {
        const part = await SecureStore.getItemAsync(`${key}.${i}`);
        // A missing chunk means a corrupt/partial write — treat as absent so the SDK re-auths.
        if (part == null) return null;
        parts.push(part);
      }
      return parts.join('');
    });
  },

  setItem(key: string, value: string): Promise<void> {
    return serialize(async () => {
      // Clear any prior chunks first — the new value may span fewer chunks.
      await removeChunks(key);
      const parts = chunkByBytes(value, CHUNK_BYTES);
      // Write the MARKER FIRST: if the write is interrupted before all chunks land, getItem sees
      // the marker but a missing chunk → returns null (absent), not a truncated corrupt value.
      await SecureStore.setItemAsync(key, `${MARKER}${parts.length}`);
      for (let i = 0; i < parts.length; i += 1) {
        await SecureStore.setItemAsync(`${key}.${i}`, parts[i]);
      }
    });
  },

  removeItem(key: string): Promise<void> {
    return serialize(async () => {
      await removeChunks(key);
      await SecureStore.deleteItemAsync(key);
    });
  },
};
