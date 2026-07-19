/**
 * Microsoft PKCE OAuth (Story 1.9, Task 1) — pure TS over injected ports.
 *
 * PRINCIPLE (the 1.6 house rule): the OAuth core is pure TS over INJECTED PORTS — a
 * Supabase-auth-like port and a WebBrowser port. No live Supabase, no network, no `.env`, no
 * module singleton reached for from inside. The real ports are wired in App.tsx.
 *
 * WHY PKCE AND NOT IMPLICIT (Decision D2): Supabase's canonical Expo snippet uses the implicit
 * flow — it parses a live access AND refresh token straight out of the redirect URL and calls
 * `setSession`. On a custom scheme (`odamrita://`) that URL is deliverable to ANY app on the
 * device registering the same scheme. PKCE ships a single-use, short-lived `code` instead,
 * redeemable only via `exchangeCodeForSession` — `setSession` is never called here.
 */

import 'react-native-url-polyfill/auto';

/** The native callback registered in mobile/app.json (`scheme: "odamrita"`) and Supabase's Redirect URLs. */
export const DEFAULT_REDIRECT_URI = 'odamrita://auth/callback';

export interface SessionLike {
  access_token: string;
  refresh_token: string;
}

export type AuthResult = { type: string; url?: string };

export interface OAuthAuthPort {
  signInWithOAuth(options: {
    provider: 'azure';
    options: { redirectTo: string; skipBrowserRedirect: true };
  }): Promise<{ data: { url: string | null }; error: unknown }>;
  exchangeCodeForSession(
    code: string,
  ): Promise<{ data: { session: SessionLike | null }; error: unknown }>;
  // Present ONLY so callers/tests can prove it is never invoked (the implicit-flow trap, D2).
  setSession(tokens: unknown): Promise<{ data: { session: SessionLike | null }; error: unknown }>;
  signOut(): Promise<{ error: unknown }>;
}

export interface WebBrowserPort {
  openAuthSessionAsync(url: string, redirectTo: string): Promise<AuthResult>;
}

function errorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === 'string' && message) return message;
  }
  if (typeof error === 'string' && error) return error;
  return fallback;
}

export function createOAuth({
  auth,
  webBrowser,
  redirectTo,
}: {
  auth: OAuthAuthPort;
  webBrowser: WebBrowserPort;
  redirectTo: string;
}) {
  /**
   * Parse a callback URL, surface any provider-reported error (e.g. the @amrita.edu
   * domain rejection, delivered as `error_description` before any token ever exists),
   * and exchange the PKCE `code` for a session.
   */
  async function createSessionFromUrl(url: string): Promise<SessionLike | null> {
    const callback = new URL(url);
    const callbackError =
      callback.searchParams.get('error_description') ?? callback.searchParams.get('error');
    if (callbackError) {
      throw new Error(callbackError);
    }

    const code = callback.searchParams.get('code');
    if (!code) return null;

    const { data, error } = await auth.exchangeCodeForSession(code);
    if (error) {
      throw new Error(errorMessage(error, 'Could not complete Microsoft sign-in.'));
    }
    return data.session ?? null;
  }

  async function performOAuth(): Promise<void> {
    const { data, error } = await auth.signInWithOAuth({
      provider: 'azure',
      options: { redirectTo, skipBrowserRedirect: true },
    });
    if (error) {
      throw new Error(errorMessage(error, 'Could not start Microsoft sign-in.'));
    }
    if (!data.url) {
      throw new Error('Supabase did not return a Microsoft sign-in URL.');
    }

    const result = await webBrowser.openAuthSessionAsync(data.url, redirectTo);
    // cancel/dismiss is a user action, not an error — return cleanly, nothing exchanged.
    if (result.type !== 'success') return;
    if (!result.url) {
      throw new Error('Microsoft sign-in returned no callback URL.');
    }

    await createSessionFromUrl(result.url);
  }

  async function signOut(): Promise<void> {
    await auth.signOut();
  }

  return { performOAuth, createSessionFromUrl, signOut };
}
