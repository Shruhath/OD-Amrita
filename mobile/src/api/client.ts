/**
 * Typed HTTP client for the Go backend (Story 1.6, Task 3) — the app-wired composition of the
 * 401 interceptor with the real Supabase session and a real `fetch` transport.
 *
 * Request paths are typed against the OpenAPI SSOT (`@od-amrita/contracts`), so the client tracks
 * the contract instead of hardcoding strings. The bearer is attached from the LIVE Supabase
 * session per call and a 401 triggers a single silent refresh + one retry (see ./authInterceptor).
 */

import type { paths } from '@od-amrita/contracts';
import { supabase } from '../lib/supabase/client';
import { reauthSignal } from '../features/auth/session';
import { createAuthedRequest, type Transport } from './authInterceptor';

const apiBaseUrl = process.env.EXPO_PUBLIC_API_BASE_URL;

if (!apiBaseUrl) {
  throw new Error(
    'Missing EXPO_PUBLIC_API_BASE_URL: set the Go backend base URL (see mobile/.env.example).',
  );
}

// Real transport: a thin wrapper over global fetch. Typing it `Transport<Response>` threads the
// concrete fetch Response through the interceptor, so `apiRequest` returns `Promise<Response>`
// with no cast — the type system enforces the transport's response shape instead of asserting it.
const transport: Transport<Response> = (path, init) => fetch(`${apiBaseUrl}${path}`, init);

// supabase.auth structurally satisfies SupabaseAuthPort (getSession / refreshSession). The
// interceptor fires the app-wide re-auth seam when the session is dead — a future login story
// subscribes to it; there is no login UI yet.
const authedRequest = createAuthedRequest({
  auth: supabase.auth,
  transport,
  onReauthRequired: () => reauthSignal.emit(),
});

/** A backend path from the OpenAPI contract (e.g. `/api/v1/auth/me`). */
export type ApiPath = keyof paths;

/**
 * Send an authenticated request to the Go backend. The typed transport yields the real fetch
 * Response, so callers receive it directly (no cast).
 */
export function apiRequest(path: ApiPath, init?: RequestInit): Promise<Response> {
  return authedRequest(path, init);
}
