/**
 * App shell (Story 1.9) — thin per D3 (no router): switches on the session the
 * `sessionGate` reports between the sign-in screen and the authenticated `RoundTripScreen`.
 *
 * All the actual decisions live in injected-port modules, tested headless (the 1.6 house
 * pattern): `oauth.ts` (the PKCE handshake), `sessionGate.ts` (session state + the 1.6 re-auth
 * seam), `RoundTripScreen.tsx` (the authenticated round trip). This file only wires the REAL
 * Supabase client and WebBrowser into those ports — it holds no auth logic of its own.
 */

import * as WebBrowser from 'expo-web-browser';
import { useEffect, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { apiRequest, type ApiPath } from './src/api/client';
import { reauthSignal } from './src/features/auth/session';
import { createOAuth, DEFAULT_REDIRECT_URI } from './src/features/auth/oauth';
import { createSessionGate, type SessionGateAuthPort, type SessionLike } from './src/features/auth/sessionGate';
import { RoundTripScreen } from './src/features/auth/RoundTripScreen';
import { supabase } from './src/lib/supabase/client';

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
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const stop = sessionGate.start((next) => {
      setSession(next);
      setBusy(false);
    });
    return stop;
  }, []);

  const handleSignIn = async () => {
    setError(null);
    setBusy(true);
    try {
      await oauth.performOAuth();
    } catch (cause) {
      setError(displayError(cause));
    } finally {
      setBusy(false);
    }
  };

  const handleSignOut = async () => {
    setBusy(true);
    await oauth.signOut();
    setBusy(false);
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
      <Text style={styles.eyebrow}>OD-AMRITA</Text>
      <Text style={styles.title}>Attendance, without the paper.</Text>
      <Text style={styles.subtitle}>Sign in with your Amrita Microsoft account to continue.</Text>
      {error ? <Text style={styles.error}>{error}</Text> : null}
      <Pressable
        style={styles.primaryButton}
        accessibilityRole="button"
        accessibilityLabel="Continue with Microsoft"
        onPress={() => void handleSignIn()}
        disabled={busy}
      >
        {busy ? (
          <ActivityIndicator color="#08111f" />
        ) : (
          <Text style={styles.primaryButtonText}>Continue with Microsoft</Text>
        )}
      </Pressable>
      <StatusBar style="light" />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#08111f',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 32,
  },
  eyebrow: {
    color: '#7dd3fc',
    fontSize: 13,
    fontWeight: '700',
    letterSpacing: 3,
    marginBottom: 18,
  },
  title: {
    color: '#f8fafc',
    fontSize: 34,
    fontWeight: '800',
    textAlign: 'center',
    marginBottom: 14,
  },
  subtitle: {
    color: '#a8b6c8',
    fontSize: 16,
    lineHeight: 24,
    maxWidth: 330,
    textAlign: 'center',
    marginBottom: 30,
  },
  error: {
    color: '#fda4af',
    fontSize: 14,
    lineHeight: 20,
    maxWidth: 340,
    textAlign: 'center',
    marginBottom: 20,
  },
  primaryButton: {
    alignItems: 'center',
    backgroundColor: '#7dd3fc',
    borderRadius: 12,
    minWidth: 260,
    paddingHorizontal: 22,
    paddingVertical: 16,
  },
  primaryButtonText: {
    color: '#08111f',
    fontSize: 16,
    fontWeight: '700',
  },
});
