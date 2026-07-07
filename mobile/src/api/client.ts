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

// Real transport: a thin wrapper over global fetch. The live fetch Response is a superset of the
// interceptor's status-only ResponseLike, so it satisfies Transport structurally.
const transport: Transport = (path, init) => fetch(`${apiBaseUrl}${path}`, init);

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
 * Send an authenticated request to the Go backend. The transport yields the real fetch Response;
 * the interceptor's contract is status-only, so we surface it back to callers as a Response.
 */
export function apiRequest(path: ApiPath, init?: RequestInit): Promise<Response> {
  return authedRequest(path, init) as Promise<Response>;
}
