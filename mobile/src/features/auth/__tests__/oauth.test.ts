import {
  DEFAULT_REDIRECT_URI,
  OAuthFlowError,
  signInWithMicrosoft,
  type BrowserAuthPort,
  type OAuthAuthPort,
} from '../oauth';

function fakeAuth(overrides: Partial<OAuthAuthPort> = {}) {
  const auth: OAuthAuthPort = {
    signInWithOAuth: jest.fn(async () => ({ data: { url: 'https://supabase.example/oauth' }, error: null })),
    exchangeCodeForSession: jest.fn(async () => ({ data: { session: { access_token: 'access-token' } }, error: null })),
    ...overrides,
  };
  return auth;
}

function fakeBrowser(result: { type: string; url?: string }): BrowserAuthPort {
  return { openAuthSessionAsync: jest.fn(async () => result) };
}

describe('Microsoft PKCE OAuth flow', () => {
  test('starts Azure OAuth with the native callback and exchanges the callback code', async () => {
    const auth = fakeAuth();
    const browser = fakeBrowser({
      type: 'success',
      url: `${DEFAULT_REDIRECT_URI}?code=pkce-code`,
    });

    const session = await signInWithMicrosoft({ auth, browser });

    expect(session?.access_token).toBe('access-token');
    expect(auth.signInWithOAuth).toHaveBeenCalledWith({
      provider: 'azure',
      options: {
        redirectTo: DEFAULT_REDIRECT_URI,
        skipBrowserRedirect: true,
        queryParams: { prompt: 'select_account' },
      },
    });
    expect(auth.exchangeCodeForSession).toHaveBeenCalledWith('pkce-code');
    expect(browser.openAuthSessionAsync).toHaveBeenCalledWith(
      'https://supabase.example/oauth',
      DEFAULT_REDIRECT_URI,
    );
  });

  test('cancellation does not attempt a code exchange', async () => {
    const auth = fakeAuth();
    const browser = fakeBrowser({ type: 'cancel' });

    await expect(signInWithMicrosoft({ auth, browser })).resolves.toBeNull();
    expect(auth.exchangeCodeForSession).not.toHaveBeenCalled();
  });

  test('provider callback errors are surfaced as OAuthFlowError', async () => {
    const auth = fakeAuth();
    const browser = fakeBrowser({
      type: 'success',
      url: `${DEFAULT_REDIRECT_URI}?error=access_denied&error_description=Account%20not%20allowed`,
    });

    await expect(signInWithMicrosoft({ auth, browser })).rejects.toEqual(
      new OAuthFlowError('Account not allowed'),
    );
    expect(auth.exchangeCodeForSession).not.toHaveBeenCalled();
  });

  test('a successful browser callback without a code fails closed', async () => {
    const auth = fakeAuth();
    const browser = fakeBrowser({ type: 'success', url: DEFAULT_REDIRECT_URI });

    await expect(signInWithMicrosoft({ auth, browser })).rejects.toThrow(
      'no authorization code',
    );
  });

  test('Supabase exchange errors are surfaced and no incomplete session is accepted', async () => {
    const auth = fakeAuth({
      exchangeCodeForSession: jest.fn(async () => ({
        data: { session: null },
        error: new Error('bad_pkce_verifier'),
      })),
    });
    const browser = fakeBrowser({ type: 'success', url: `${DEFAULT_REDIRECT_URI}?code=code` });

    await expect(signInWithMicrosoft({ auth, browser })).rejects.toThrow('bad_pkce_verifier');
  });
});
