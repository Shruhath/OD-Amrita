import { useEffect, useState, type Dispatch, type SetStateAction } from 'react'

/**
 * State that survives the operator leaving the app and coming back.
 *
 * WHAT THIS IS FOR. On a phone, glancing at a notification can cost the whole
 * screen: Android discards a backgrounded tab under memory pressure and the page
 * reloads from scratch. An operator who had chosen a branch, a batch and a
 * section, and loaded a roster, has to do all of it again — during an event,
 * holding a phone in one hand.
 *
 * WHAT MAY BE PUT IN HERE, AND WHAT MUST NOT.
 *
 *   ✅ SELECTIONS — which event, which category, which branch, which slot. These
 *      are the operator's choices, and the ids in them are not identity data:
 *      an event id is public, a branch code is public.
 *
 *   ❌ THE DATA THOSE SELECTIONS FETCH. No roster, no search result, no student
 *      name or roll number, and above all no card token, payload or fingerprint.
 *      A resolved roster on the device IS the cached list AR20 exists to forbid,
 *      and MarkScreen's rules say so in as many words.
 *
 * That distinction is what makes this safe AND sufficient: restoring the
 * selection is enough, because the query keyed on it re-fetches the data from
 * the server on the way back. The operator sees their roster again; the roster
 * itself never touched the disk.
 *
 * SESSION storage, not local. It is scoped to the tab and cleared when the tab
 * closes, which is the behaviour you want on a handset passed between
 * coordinators — and it is what the card populator's progress already uses.
 */

/** Every key this app writes. Namespaced so `clearPersistedState` can be exact. */
export const PERSISTENCE_PREFIX = 'od-amrita.'

/**
 * Reads and validates a stored value.
 *
 * `revive` is required rather than optional because session storage is editable
 * by anyone with the developer tools open, so a stored value is untrusted input.
 * A malformed entry is discarded in favour of the fallback rather than being
 * handed to a component that expects a shape.
 */
function read<T>(key: string, revive: (raw: unknown) => T | null): T | null {
  try {
    const raw = window.sessionStorage.getItem(PERSISTENCE_PREFIX + key)
    if (raw === null) return null
    return revive(JSON.parse(raw) as unknown)
  } catch {
    // Unavailable (private mode, quota) or unparseable. Persistence is an
    // enhancement — never a reason for a screen to fail to render.
    return null
  }
}

export function usePersistentState<T>(
  key: string,
  fallback: T,
  revive: (raw: unknown) => T | null,
): [T, Dispatch<SetStateAction<T>>] {
  // Read ONCE, in the initialiser. Reading in an effect would render the
  // fallback first and then replace it, which is a visible flicker on exactly
  // the return-to-app path this exists to smooth over.
  const [value, setValue] = useState<T>(() => read(key, revive) ?? fallback)

  useEffect(() => {
    try {
      window.sessionStorage.setItem(PERSISTENCE_PREFIX + key, JSON.stringify(value))
    } catch {
      // As above: storage failing means the choice does not survive, not that
      // the operator cannot make it.
    }
  }, [key, value])

  return [value, setValue]
}

/** Accepts a stored string, rejecting anything else. */
export function asString(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/**
 * Accepts a stored object whose named fields are all strings.
 *
 * Checks every field rather than the object as a whole: a partially-written
 * entry would otherwise restore `undefined` into a controlled input, which React
 * turns into an uncontrolled one and warns about — a confusing failure a long
 * way from its cause.
 */
export function asStringRecord<K extends string>(fields: readonly K[]) {
  return (raw: unknown): Record<K, string> | null => {
    if (typeof raw !== 'object' || raw === null) return null
    const source = raw as Record<string, unknown>
    const restored = {} as Record<K, string>
    for (const field of fields) {
      if (typeof source[field] !== 'string') return null
      restored[field] = source[field] as string
    }
    return restored
  }
}

/**
 * Drops everything this app has stored for the session.
 *
 * Called on sign-out. A coordinator's phone is passed around during an event, so
 * the next person to sign in must not inherit the previous one's screen — and
 * the card populator's per-student progress in particular should not outlive the
 * account that produced it.
 */
export function clearPersistedState() {
  try {
    const keys: string[] = []
    for (let index = 0; index < window.sessionStorage.length; index += 1) {
      const key = window.sessionStorage.key(index)
      if (key?.startsWith(PERSISTENCE_PREFIX)) keys.push(key)
    }
    // Collected first, then removed: deleting while walking the store by index
    // shifts everything after it and silently skips entries.
    keys.forEach((key) => window.sessionStorage.removeItem(key))
  } catch {
    // Nothing to clear if storage is unavailable.
  }
}
