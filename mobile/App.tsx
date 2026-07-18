import * as WebBrowser from 'expo-web-browser';
import { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { apiRequest } from './src/api/client';
import { reauthSignal } from './src/features/auth/session';
import {
  DEFAULT_REDIRECT_URI,
  signInWithMicrosoft,
  type OAuthSession,
} from './src/features/auth/oauth';
import { supabase } from './src/lib/supabase/client';

type AuthenticatedIdentity = {
  email: string;
  subject: string;
  role: 'admin' | 'student';
  name?: string | null;
};

const browserAuth = {
  openAuthSessionAsync: (authUrl: string, redirectUrl: string) =>
    WebBrowser.openAuthSessionAsync(authUrl, redirectUrl),
};

function displayError(error: unknown): string {
  return error instanceof Error ? error.message : 'Unable to complete sign-in.';
}

export default function App() {
  const [hasSession, setHasSession] = useState(false);
  const [identity, setIdentity] = useState<AuthenticatedIdentity | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const identityRequestInFlight = useRef(false);

  const loadIdentity = async () => {
    if (identityRequestInFlight.current) return;
    identityRequestInFlight.current = true;
    setBusy(true);
    try {
      const response = await apiRequest('/api/v1/auth/me');
      const payload = (await response.json()) as AuthenticatedIdentity | { message?: string };
      if (!response.ok) {
        throw new Error('message' in payload && payload.message ? payload.message : 'Authentication failed.');
      }
      setIdentity(payload as AuthenticatedIdentity);
      setError(null);
    } catch (cause) {
      setIdentity(null);
      setError(displayError(cause));
    } finally {
      identityRequestInFlight.current = false;
      setBusy(false);
    }
  };

  useEffect(() => {
    let mounted = true;
    void supabase.auth.getSession().then(({ data }) => {
      if (!mounted) return;
      setHasSession(Boolean(data.session));
      if (data.session) void loadIdentity();
      else setBusy(false);
    });

    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((event, session) => {
      if (!mounted) return;
      setHasSession(Boolean(session));
      if (event === 'SIGNED_OUT') {
        setIdentity(null);
        setError(null);
        setBusy(false);
      }
    });
    const unsubscribeReauth = reauthSignal.subscribe(() => {
      if (!mounted) return;
      setHasSession(false);
      setIdentity(null);
      setError('Your session expired. Sign in again to continue.');
      setBusy(false);
    });

    return () => {
      mounted = false;
      subscription.unsubscribe();
      unsubscribeReauth();
    };
  }, []);

  const handleSignIn = async () => {
    setError(null);
    setBusy(true);
    try {
      const session: OAuthSession | null = await signInWithMicrosoft({
        auth: supabase.auth,
        browser: browserAuth,
        redirectUri: DEFAULT_REDIRECT_URI,
      });
      if (session) {
        // The exchange emits SIGNED_IN, but the explicit call makes the
        // authenticated round trip deterministic across native SDK versions.
        setHasSession(true);
        await loadIdentity();
      } else {
        // Browser cancellation is a normal user action, not a failed session.
        setBusy(false);
      }
    } catch (cause) {
      setError(displayError(cause));
      setBusy(false);
    }
  };

  const handleSignOut = async () => {
    setBusy(true);
    const { error: signOutError } = await supabase.auth.signOut();
    if (signOutError) setError(signOutError.message);
    setHasSession(false);
    setIdentity(null);
    setBusy(false);
  };

  if (hasSession && identity) {
    return (
      <View style={styles.container}>
        <Text style={styles.eyebrow}>OD-AMRITA</Text>
        <Text style={styles.title}>You are checked in.</Text>
        <Text style={styles.email}>{identity.email}</Text>
        <Text style={styles.role}>Role: {identity.role}</Text>
        <Pressable style={styles.secondaryButton} onPress={() => void handleSignOut()} disabled={busy}>
          <Text style={styles.secondaryButtonText}>Sign out</Text>
        </Pressable>
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
      <Pressable style={styles.primaryButton} onPress={() => void handleSignIn()} disabled={busy}>
        {busy ? <ActivityIndicator color="#08111f" /> : <Text style={styles.primaryButtonText}>Continue with Microsoft</Text>}
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
  email: {
    color: '#f8fafc',
    fontSize: 18,
    marginBottom: 10,
  },
  role: {
    color: '#7dd3fc',
    fontSize: 15,
    marginBottom: 28,
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
  secondaryButton: {
    borderColor: '#46617e',
    borderRadius: 12,
    borderWidth: 1,
    minWidth: 140,
    paddingHorizontal: 22,
    paddingVertical: 14,
    alignItems: 'center',
  },
  secondaryButtonText: {
    color: '#d7e3f0',
    fontSize: 15,
    fontWeight: '600',
  },
});
