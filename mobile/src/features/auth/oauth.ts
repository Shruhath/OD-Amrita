import 'react-native-url-polyfill/auto';

/** The native callback registered in mobile/app.json and Supabase Redirect URLs. */
export const DEFAULT_REDIRECT_URI = 'odamrita://auth/callback';

export interface OAuthSession {
  access_token: string;
}

export interface OAuthAuthPort {
  signInWithOAuth(options: {
    provider: 'azure';
    options: {
      redirectTo: string;
      skipBrowserRedirect: true;
      queryParams: { prompt: 'select_account' };
    };
  }): Promise<{ data: { url: string | null }; error: unknown }>;
  exchangeCodeForSession(code: string): Promise<{
    data: { session: OAuthSession | null };
    error: unknown;
  }>;
}

export interface BrowserAuthPort {
  openAuthSessionAsync(
    authUrl: string,
    redirectUrl: string,
  ): Promise<{ type: string; url?: string }>;
}

export class OAuthFlowError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OAuthFlowError';
  }
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === 'string' && error) return error;
  return fallback;
}

/**
 * Launch Microsoft OAuth through Supabase and exchange the PKCE callback code.
 * Browser and auth clients are injected so cancellation and callback failures
 * are testable without a native device or a live Supabase project.
 */
export async function signInWithMicrosoft({
  auth,
  browser,
  redirectUri = DEFAULT_REDIRECT_URI,
}: {
  auth: OAuthAuthPort;
  browser: BrowserAuthPort;
  redirectUri?: string;
}): Promise<OAuthSession | null> {
  const { data, error } = await auth.signInWithOAuth({
    provider: 'azure',
    options: {
      redirectTo: redirectUri,
      skipBrowserRedirect: true,
      queryParams: { prompt: 'select_account' },
    },
  });

  if (error) {
    throw new OAuthFlowError(errorMessage(error, 'Could not start Microsoft sign-in.'));
  }
  if (!data.url) {
    throw new OAuthFlowError('Supabase did not return a Microsoft sign-in URL.');
  }

  const result = await browser.openAuthSessionAsync(data.url, redirectUri);
  if (result.type !== 'success') return null;
  if (!result.url) {
    throw new OAuthFlowError('Microsoft sign-in returned no callback URL.');
  }

  const callback = new URL(result.url);
  const callbackError =
    callback.searchParams.get('error_description') ?? callback.searchParams.get('error');
  if (callbackError) {
    throw new OAuthFlowError(callbackError);
  }

  const code = callback.searchParams.get('code');
  if (!code) {
    throw new OAuthFlowError('Microsoft sign-in returned no authorization code.');
  }

  const exchanged = await auth.exchangeCodeForSession(code);
  if (exchanged.error) {
    throw new OAuthFlowError(
      errorMessage(exchanged.error, 'Could not complete Microsoft sign-in.'),
    );
  }
  if (!exchanged.data.session) {
    throw new OAuthFlowError('Supabase returned no session after Microsoft sign-in.');
  }
  return exchanged.data.session;
}
