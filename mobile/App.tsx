/**
 * App shell — thin per D3 (no router): switches on the session the
 * `sessionGate` reports between the sign-in screen and the authenticated `RoundTripScreen`.
 *
 * All the actual decisions live in injected-port modules, tested headless (the 1.6 house
 * pattern): `oauth.ts` (the PKCE handshake), `sessionGate.ts` (session state + the 1.6 re-auth
 * seam), `RoundTripScreen.tsx` (the authenticated round trip). This file only wires the REAL
 * Supabase client and WebBrowser into those ports — it holds no auth logic of its own.
 */

import * as Linking from 'expo-linking';
import * as WebBrowser from 'expo-web-browser';
import { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { apiRequest, type ApiPath } from './src/api/client';
import { reauthSignal } from './src/features/auth/session';
import { createOAuth, DEFAULT_REDIRECT_URI } from './src/features/auth/oauth';
import { createSessionGate, type SessionGateAuthPort, type SessionLike } from './src/features/auth/sessionGate';
import { RoundTripScreen } from './src/features/auth/RoundTripScreen';
import { supabase } from './src/lib/supabase/client';
import { AppSurface, GlassPanel } from './src/ui/AppSurface';

// Required once at module scope by expo-web-browser so a web-flow auth session
// (opened via openAuthSessionAsync) can signal completion back to the opener.
WebBrowser.maybeCompleteAuthSession();

const oauth = createOAuth({
  auth: supabase.auth,
  webBrowser: {
    openAuthSessionAsync: (url, redirectTo) => WebBrowser.openAuthSessionAsync(url, redirectTo),
  },
  redirectTo: DEFAULT_REDIRECT_URI,
});

// Adapter: the real Supabase `onAuthStateChange` callback is `(event, session) => void` — the
// gate's port intentionally only knows about sessions, not Supabase's event vocabulary, so the
// two-argument shape is collapsed to one right here at the wiring boundary.
const sessionGateAuth: SessionGateAuthPort = {
  getSession: () => supabase.auth.getSession(),
  onAuthStateChange: (callback) => {
    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((_event, session) => callback(session));
    return { data: { subscription } };
  },
  signOut: () => supabase.auth.signOut(),
};

const sessionGate = createSessionGate({ auth: sessionGateAuth, reauthSignal });

// RoundTripScreen's port is untyped `string` (so it stays test-friendly, with no dependency on
// the generated contract types); the real client is typed against ApiPath, so the cast happens
// here at the wiring boundary, not inside the screen.
function roundTripApiRequest(path: string): Promise<Response> {
  return apiRequest(path as ApiPath);
}

function displayError(error: unknown): string {
  return error instanceof Error ? error.message : 'Unable to complete sign-in.';
}

export default function App() {
  const [session, setSession] = useState<SessionLike | null>(null);
  // `gateReady`/`linkBusy`/`actionBusy` are three independent loading sources (initial
  // session-gate resolution, deep-link code exchange, and manual sign-in/out) rather than one
  // shared `busy` flag — sharing a single flag across the two effects below let either one
  // stomp on a state transition the other was mid-way through.
  const [gateReady, setGateReady] = useState(false);
  const [linkBusy, setLinkBusy] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // getInitialURL() and the 'url' listener can both deliver the SAME cold-start redirect on some
  // platforms — without this guard the single-use PKCE code gets redeemed twice, and the second
  // exchange fails with a spurious visible error even though sign-in already succeeded.
  const handledUrls = useRef<Set<string>>(new Set());

  useEffect(() => {
    const stop = sessionGate.start((next) => {
      setSession(next);
      setGateReady(true);
    });
    return stop;
  }, []);

  // Closes the cold-start gap: if the OS kills the app during the Microsoft browser
  // interstitial, `performOAuth`'s in-memory await never resumes — the app relaunches
  // fresh via the odamrita:// redirect instead. getInitialURL() catches that case;
  // the 'url' listener covers the same redirect arriving while merely backgrounded.
  useEffect(() => {
    async function completeFromUrl(url: string | null) {
      if (!url || !url.startsWith(DEFAULT_REDIRECT_URI)) return;
      if (handledUrls.current.has(url)) return;
      handledUrls.current.add(url);
      setError(null);
      setLinkBusy(true);
      try {
        await oauth.createSessionFromUrl(url);
      } catch (cause) {
        setError(displayError(cause));
      } finally {
        setLinkBusy(false);
      }
    }

    Linking.getInitialURL()
      .then(completeFromUrl)
      .catch((cause) => setError(displayError(cause)));
    const subscription = Linking.addEventListener('url', ({ url }) => void completeFromUrl(url));
    return () => subscription.remove();
  }, []);

  const busy = !gateReady || linkBusy || actionBusy;

  const handleSignIn = async () => {
    setError(null);
    setActionBusy(true);
    try {
      await oauth.performOAuth();
    } catch (cause) {
      setError(displayError(cause));
    } finally {
      setActionBusy(false);
    }
  };

  const handleSignOut = async () => {
    setActionBusy(true);
    try {
      await oauth.signOut();
    } catch (cause) {
      setError(displayError(cause));
    } finally {
      setActionBusy(false);
    }
  };

  if (session) {
    return (
      <View style={styles.container}>
        <RoundTripScreen apiRequest={roundTripApiRequest} onSignOut={() => void handleSignOut()} />
        <StatusBar style="light" />
      </View>
    );
  }

  return (
    <View style={styles.container}>
      <AppSurface>
        <View style={styles.brandLockup}>
          <View style={styles.brandMark}>
            <Text style={styles.brandMarkText}>OD</Text>
          </View>
          <View style={styles.brandTextBlock}>
            <Text style={styles.eyebrow}>OD-AMRITA</Text>
            <Text style={styles.brandSubline}>Campus attendance</Text>
          </View>
        </View>

        <GlassPanel style={styles.signInPanel}>
          <View style={styles.statusPill}>
            <View style={styles.statusDot} />
            <Text style={styles.statusText}>{busy ? 'Checking session' : 'Ready'}</Text>
          </View>
          <Text adjustsFontSizeToFit minimumFontScale={0.86} numberOfLines={2} style={styles.title}>
            On-duty attendance, refined.
          </Text>
          <Text style={styles.subtitle}>Amrita Microsoft account required.</Text>
          {error ? (
            <Text adjustsFontSizeToFit minimumFontScale={0.78} numberOfLines={4} style={styles.error}>
              {error}
            </Text>
          ) : null}
          <Pressable
            style={({ pressed }) => [
              styles.primaryButton,
              pressed && !busy ? styles.primaryButtonPressed : null,
              busy ? styles.primaryButtonDisabled : null,
            ]}
            accessibilityRole="button"
            accessibilityLabel="Continue with Microsoft"
            onPress={() => void handleSignIn()}
            disabled={busy}
          >
            {busy ? (
              <ActivityIndicator color="#07111d" />
            ) : (
              <Text adjustsFontSizeToFit minimumFontScale={0.9} numberOfLines={1} style={styles.primaryButtonText}>
                Continue with Microsoft
              </Text>
            )}
          </Pressable>
        </GlassPanel>
      </AppSurface>
      <StatusBar style="light" />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#06101c',
  },
  brandLockup: {
    width: '100%',
    maxWidth: 430,
    alignSelf: 'center',
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    marginBottom: 18,
  },
  brandMark: {
    width: 46,
    height: 46,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.28)',
    backgroundColor: 'rgba(255, 255, 255, 0.14)',
  },
  brandMarkText: {
    color: '#f8fafc',
    fontSize: 16,
    fontWeight: '900',
  },
  eyebrow: {
    color: '#e7f8ff',
    fontSize: 13,
    fontWeight: '800',
  },
  brandTextBlock: {
    minWidth: 0,
    flex: 1,
  },
  brandSubline: {
    color: 'rgba(226, 239, 247, 0.68)',
    fontSize: 12,
    fontWeight: '600',
    marginTop: 2,
  },
  signInPanel: {
    padding: 22,
  },
  statusPill: {
    alignSelf: 'flex-start',
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(125, 211, 252, 0.24)',
    backgroundColor: 'rgba(125, 211, 252, 0.10)',
    paddingHorizontal: 10,
    paddingVertical: 7,
    marginBottom: 22,
  },
  statusDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: '#67e8f9',
  },
  statusText: {
    color: '#d9f7ff',
    fontSize: 12,
    fontWeight: '800',
  },
  title: {
    color: '#f8fafc',
    fontSize: 38,
    fontWeight: '900',
    lineHeight: 43,
    marginBottom: 12,
  },
  subtitle: {
    color: 'rgba(226, 239, 247, 0.76)',
    fontSize: 16,
    lineHeight: 23,
    marginBottom: 24,
  },
  error: {
    overflow: 'hidden',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(254, 202, 202, 0.22)',
    backgroundColor: 'rgba(127, 29, 29, 0.30)',
    color: '#fecdd3',
    fontSize: 13,
    lineHeight: 19,
    marginBottom: 16,
    padding: 12,
  },
  primaryButton: {
    width: '100%',
    minHeight: 54,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#e5fbff',
    borderRadius: 8,
    paddingHorizontal: 22,
    paddingVertical: 15,
  },
  primaryButtonPressed: {
    opacity: 0.86,
    transform: [{ scale: 0.99 }],
  },
  primaryButtonDisabled: {
    opacity: 0.82,
  },
  primaryButtonText: {
    color: '#07111d',
    fontSize: 16,
    fontWeight: '900',
  },
});
