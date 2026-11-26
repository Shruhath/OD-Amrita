/**
 * Unit tests for the OAuth sign-in module. They are the executable specification of its
 * behaviour.
 *
 * PRINCIPLE (the 1.6 house rule, kept): the OAuth core is pure TS over INJECTED PORTS — a
 * Supabase-auth-like port and a WebBrowser port. No live Supabase, no network, no `.env`, no
 * module singleton reached for from inside. Every collaborator here is a fake.
 *
 * SEAM CONTRACT these tests pin (the dev implements exactly this shape):
 *
 *   createOAuth({ auth, webBrowser, redirectTo }) -> {
 *     performOAuth():            Promise<void>
 *     createSessionFromUrl(url): Promise<SessionLike | null>
 *     signOut():                 Promise<void>
 *   }
 *
 * WHY PKCE AND NOT IMPLICIT (Decision D2): Supabase's canonical Expo snippet uses the implicit
 * flow — it parses a live access AND refresh token straight out of the redirect URL and calls
 * `setSession`. On a custom scheme (`odamrita://`) that URL is deliverable to ANY app on the
 * device registering the same scheme. PKCE ships a single-use, 5-minute `code` instead,
 * redeemable only with the `code_verifier` in our SecureStore. These tests therefore assert
 * `exchangeCodeForSession` is used and that `setSession` is NEVER called — if a future refactor
 * "simplifies" back to the documented implicit snippet, 1.9-UNIT-001 fails loudly.
 */

import {
  createOAuth,
  type OAuthAuthPort,
  type WebBrowserPort,
  type AuthResult,
} from '../oauth';

// ---------------------------------------------------------------------------
// Test doubles (deterministic, no network) — data factories per data-factories.md
// ---------------------------------------------------------------------------

const REDIRECT_TO = 'odamrita://';
const PROVIDER_URL = 'https://project.supabase.co/auth/v1/authorize?provider=azure';

/** The exact rejection Supabase's before-user-created hook returns for a non-@amrita.edu login. */
const DOMAIN_REJECTION_MESSAGE = 'Only @amrita.edu accounts can access this system.';
const DOMAIN_REJECTION_URL =
  'odamrita://?error=access_denied&error_description=Only+%40amrita.edu+accounts+can+access+this+system.';

type AuthCalls = {
  signInWithOAuth: Array<Record<string, unknown>>;
  exchangeCodeForSession: string[];
  setSession: unknown[];
  signOut: number;
};

/**
 * A Supabase-auth-like fake. `signInError` scripts the provider handshake failing;
 * everything else records what the module under test asked for.
 */
function fakeAuth(opts: { signInError?: string } = {}) {
  const calls: AuthCalls = {
    signInWithOAuth: [],
    exchangeCodeForSession: [],
    setSession: [],
    signOut: 0,
  };

  const auth: OAuthAuthPort = {
    async signInWithOAuth(args) {
      calls.signInWithOAuth.push(args as unknown as Record<string, unknown>);
      if (opts.signInError) {
        return { data: { url: null }, error: { message: opts.signInError } };
      }
      return { data: { url: PROVIDER_URL }, error: null };
    },
    async exchangeCodeForSession(code) {
      calls.exchangeCodeForSession.push(code);
      return { data: { session: { access_token: 'at', refresh_token: 'rt' } }, error: null };
    },
    // Present ONLY so the tests can prove it is never called (the implicit-flow trap, D2).
    async setSession(tokens) {
      calls.setSession.push(tokens);
      return { data: { session: null }, error: null };
    },
    async signOut() {
      calls.signOut += 1;
      return { error: null };
    },
  };

  return { auth, calls };
}

/** A WebBrowser fake that returns a scripted auth-session result and records what it was handed. */
function fakeWebBrowser(result: AuthResult) {
  const calls: Array<{ url: string; redirectTo: string }> = [];
  const webBrowser: WebBrowserPort = {
    async openAuthSessionAsync(url, redirectTo) {
      calls.push({ url, redirectTo });
      return result;
    },
  };
  return { webBrowser, calls };
}

/** The happy-path redirect: a PKCE authorization code on our custom scheme. */
const successRedirect = (code = 'pkce-auth-code-123'): AuthResult => ({
  type: 'success',
  url: `odamrita://?code=${code}`,
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-001 [P0] — the PKCE happy path
// ---------------------------------------------------------------------------

test('[P0] a successful redirect exchanges the code from the URL for a session (PKCE)', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser(successRedirect('the-real-code'));
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await oauth.performOAuth();

  expect(calls.exchangeCodeForSession).toEqual(['the-real-code']);
});

test('[P0] setSession is NEVER called — the implicit flow is not our flow (D2)', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser(successRedirect());
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await oauth.performOAuth();

  expect(calls.setSession).toHaveLength(0);
});

test('[P0] createSessionFromUrl exchanges the code and returns the session', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser(successRedirect());
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  const session = await oauth.createSessionFromUrl('odamrita://?code=cold-start-code');

  expect(calls.exchangeCodeForSession).toEqual(['cold-start-code']);
  expect(session).not.toBeNull();
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-002 [P0] — an error in the redirect URL must not become a session
// ---------------------------------------------------------------------------

test('[P0] a redirect carrying an error code throws and exchanges nothing', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser({
    type: 'success',
    url: 'odamrita://?error=server_error&error_code=otp_expired&error_description=Token+has+expired',
  });
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await expect(oauth.performOAuth()).rejects.toThrow();
  expect(calls.exchangeCodeForSession).toHaveLength(0);
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-003 [P0] — the domain rejection must surface the REAL message
// ---------------------------------------------------------------------------

test('[P0] a non-@amrita.edu login surfaces the domain-rejection message, not a generic failure', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser({ type: 'success', url: DOMAIN_REJECTION_URL });
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  // The Supabase before-user-created hook denies the login BEFORE a token exists, so this comes
  // back as a redirect error — never as a 403 from our API. The user must be told WHY.
  await expect(oauth.performOAuth()).rejects.toThrow(DOMAIN_REJECTION_MESSAGE);
  expect(calls.exchangeCodeForSession).toHaveLength(0);
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-004 [P1] — cancel is a user action, not an error
// ---------------------------------------------------------------------------

test.each([['cancel'], ['dismiss']])(
  '[P1] a %s result returns cleanly — no session, no throw (it is a user action, not an error)',
  async (type) => {
    const { auth, calls } = fakeAuth();
    const { webBrowser } = fakeWebBrowser({ type } as AuthResult);
    const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

    await expect(oauth.performOAuth()).resolves.not.toThrow();
    expect(calls.exchangeCodeForSession).toHaveLength(0);
  },
);

// ---------------------------------------------------------------------------
// 1.9-UNIT-005 [P1] — a failed provider handshake never opens a browser
// ---------------------------------------------------------------------------

test('[P1] a signInWithOAuth error propagates and the browser is never opened', async () => {
  const { auth } = fakeAuth({ signInError: 'provider is not enabled' });
  const { webBrowser, calls: browserCalls } = fakeWebBrowser(successRedirect());
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await expect(oauth.performOAuth()).rejects.toThrow(/provider is not enabled/);
  expect(browserCalls).toHaveLength(0);
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-006 [P1] — the handshake wiring itself
// ---------------------------------------------------------------------------

test('[P1] performOAuth requests the azure provider with our redirect and suppresses the browser redirect', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser, calls: browserCalls } = fakeWebBrowser(successRedirect());
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await oauth.performOAuth();

  // `azure` — NOT `microsoft`. That is the id the Supabase Azure/Entra provider is enabled under.
  expect(calls.signInWithOAuth).toEqual([
    {
      provider: 'azure',
      options: { redirectTo: REDIRECT_TO, skipBrowserRedirect: true },
    },
  ]);

  // skipBrowserRedirect means WE open the browser — with the provider URL Supabase handed back,
  // and the same redirect the session must come home to.
  expect(browserCalls).toEqual([{ url: PROVIDER_URL, redirectTo: REDIRECT_TO }]);
});

// ---------------------------------------------------------------------------
// 1.9-UNIT-007 [P2] — sign out
// ---------------------------------------------------------------------------

test('[P2] signOut delegates to the auth port', async () => {
  const { auth, calls } = fakeAuth();
  const { webBrowser } = fakeWebBrowser(successRedirect());
  const oauth = createOAuth({ auth, webBrowser, redirectTo: REDIRECT_TO });

  await oauth.signOut();

  expect(calls.signOut).toBe(1);
});
