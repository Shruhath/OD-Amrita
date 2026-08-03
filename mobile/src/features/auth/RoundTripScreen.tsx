/**
 * Round-trip screen (Story 1.9, Task 2) — the walking-skeleton exit gate's mobile half.
 *
 * AC 2, verbatim: "A hardcoded 'Success ✅' is an AC failure." `role` is the load-bearing
 * assertion (1.9-COMP-001): the device cannot compute it — it is resolved server-side by
 * ResolveRole against AUTH_BOOTSTRAP_ADMINS — so a role rendered here is proof the response
 * actually came from the backend.
 *
 * `apiRequest` is the injected port with the same signature as the real typed client in
 * `src/api/client.ts` (which owns the 401 interceptor) — this screen must not open a second
 * transport.
 */

import { useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';

import { AppSurface, GlassPanel } from '../../ui/AppSurface';

export interface AuthenticatedIdentity {
  email: string;
  name?: string | null;
  subject: string;
  role: string;
}

export interface ErrorEnvelope {
  code: string;
  message: string;
}

export interface RoundTripScreenProps {
  apiRequest: (path: string) => Promise<Response>;
  onSignOut: () => void;
}

type RoundTripState =
  | { status: 'idle' }
  | { status: 'success'; identity: AuthenticatedIdentity }
  | { status: 'error'; code: string; message: string };

function isErrorEnvelope(body: unknown): body is ErrorEnvelope {
  return (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as { code?: unknown }).code === 'string' &&
    typeof (body as { message?: unknown }).message === 'string'
  );
}

export function RoundTripScreen({ apiRequest, onSignOut }: RoundTripScreenProps) {
  const [state, setState] = useState<RoundTripState>({ status: 'idle' });
  // Guards against rapid double-taps firing a second request while the first is still in
  // flight — two concurrent requests can resolve out of order and leave stale state on screen.
  const [pending, setPending] = useState(false);

  const handleRoundTrip = async () => {
    if (pending) return;
    setPending(true);
    try {
      const response = await apiRequest('/api/v1/auth/me');
      const body: unknown = await response.json();
      if (!response.ok) {
        // The server's error middleware always emits {code, message}, but a malformed or
        // non-JSON body (e.g. from an intermediary proxy) must not render a blank error.
        setState(
          isErrorEnvelope(body)
            ? { status: 'error', code: body.code, message: body.message }
            : { status: 'error', code: 'INTERNAL', message: 'Unable to complete the round trip.' },
        );
        return;
      }
      setState({ status: 'success', identity: body as AuthenticatedIdentity });
    } catch (cause) {
      // A transport failure (offline, DNS, timeout) — not something the backend rendered.
      setState({
        status: 'error',
        code: 'INTERNAL',
        message: cause instanceof Error ? cause.message : 'Unable to complete the round trip.',
      });
    } finally {
      setPending(false);
    }
  };

  return (
    <AppSurface contentStyle={styles.surfaceContent}>
      <View style={styles.header}>
        <View style={styles.brandMark}>
          <Text style={styles.brandMarkText}>OD</Text>
        </View>
        <View style={styles.headerTextBlock}>
          <Text style={styles.eyebrow}>OD-AMRITA</Text>
          <Text style={styles.headerSubline}>Signed in</Text>
        </View>
      </View>

      <GlassPanel style={styles.panel}>
        <View style={styles.panelHeader}>
          <View style={styles.statusPill}>
            <View style={[styles.statusDot, pending ? styles.statusDotBusy : null]} />
            <Text style={styles.statusText}>{pending ? 'Verifying' : 'Live session'}</Text>
          </View>
        </View>

        <Text adjustsFontSizeToFit minimumFontScale={0.86} numberOfLines={2} style={styles.title}>
          Server identity
        </Text>

        {state.status === 'idle' ? (
          <View style={styles.emptyState}>
            <Text style={styles.emptyTitle}>Awaiting verification</Text>
            <Text style={styles.emptyCopy}>No server identity yet.</Text>
          </View>
        ) : null}

        {state.status === 'success' ? (
          <View style={styles.identityCard}>
            <Text style={styles.identityLabel}>Verified account</Text>
            {/* A SINGLE Text node, deliberately: the default test identity's email domain
            (@cb.students.amrita.edu) contains "student" as a substring of the role value
            ("student") too — splitting these into sibling Text nodes makes every text query
            ambiguous (matches both). One node keeps each query unique. */}
            <Text
              adjustsFontSizeToFit
              minimumFontScale={0.72}
              numberOfLines={5}
              selectable
              style={styles.identity}
            >
              {state.identity.name ? `${state.identity.name}\n` : null}
              {state.identity.email}
              {'\n'}
              {state.identity.subject}
              {'\n'}
              Role: {state.identity.role}
            </Text>
          </View>
        ) : null}

        {state.status === 'error' ? (
          <View style={styles.errorCard}>
            <Text adjustsFontSizeToFit minimumFontScale={0.72} numberOfLines={1} style={styles.errorCode}>
              {state.code}
            </Text>
            <Text adjustsFontSizeToFit minimumFontScale={0.78} numberOfLines={3} style={styles.errorMessage}>
              {state.message}
            </Text>
          </View>
        ) : null}

        <View style={styles.actions}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Round trip"
            style={({ pressed }) => [
              styles.button,
              pressed && !pending ? styles.buttonPressed : null,
              pending ? styles.buttonDisabled : null,
            ]}
            onPress={() => void handleRoundTrip()}
            disabled={pending}
          >
            {pending ? (
              <ActivityIndicator color="#06101c" />
            ) : (
              <Text adjustsFontSizeToFit minimumFontScale={0.9} numberOfLines={1} style={styles.buttonText}>
                Round trip
              </Text>
            )}
          </Pressable>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Sign out"
            style={({ pressed }) => [styles.secondaryButton, pressed ? styles.secondaryButtonPressed : null]}
            onPress={onSignOut}
          >
            <Text adjustsFontSizeToFit minimumFontScale={0.9} numberOfLines={1} style={styles.secondaryButtonText}>
              Sign out
            </Text>
          </Pressable>
        </View>
      </GlassPanel>
    </AppSurface>
  );
}

const styles = StyleSheet.create({
  surfaceContent: {
    justifyContent: 'center',
  },
  header: {
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
  headerTextBlock: {
    minWidth: 0,
    flex: 1,
  },
  eyebrow: {
    color: '#e7f8ff',
    fontSize: 13,
    fontWeight: '800',
  },
  headerSubline: {
    color: 'rgba(226, 239, 247, 0.68)',
    fontSize: 12,
    fontWeight: '600',
    marginTop: 2,
  },
  panel: {
    padding: 22,
  },
  panelHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginBottom: 20,
  },
  statusPill: {
    maxWidth: '100%',
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(125, 211, 252, 0.24)',
    backgroundColor: 'rgba(125, 211, 252, 0.10)',
    paddingHorizontal: 10,
    paddingVertical: 7,
  },
  statusDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: '#67e8f9',
  },
  statusDotBusy: {
    backgroundColor: '#fbbf24',
  },
  statusText: {
    color: '#d9f7ff',
    fontSize: 12,
    fontWeight: '800',
  },
  title: {
    color: '#f8fafc',
    fontSize: 34,
    fontWeight: '900',
    lineHeight: 39,
    marginBottom: 16,
  },
  emptyState: {
    overflow: 'hidden',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.12)',
    backgroundColor: 'rgba(255, 255, 255, 0.08)',
    padding: 16,
    marginBottom: 18,
  },
  emptyTitle: {
    color: '#f8fafc',
    fontSize: 16,
    fontWeight: '800',
    marginBottom: 6,
  },
  emptyCopy: {
    color: 'rgba(226, 239, 247, 0.70)',
    fontSize: 14,
    lineHeight: 20,
  },
  identityCard: {
    overflow: 'hidden',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.15)',
    backgroundColor: 'rgba(255, 255, 255, 0.09)',
    padding: 16,
    marginBottom: 18,
  },
  identityLabel: {
    color: '#a7f3d0',
    fontSize: 12,
    fontWeight: '900',
    marginBottom: 10,
  },
  identity: {
    color: '#edf8ff',
    fontSize: 14,
    lineHeight: 21,
    flexShrink: 1,
  },
  errorCard: {
    overflow: 'hidden',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(254, 202, 202, 0.22)',
    backgroundColor: 'rgba(127, 29, 29, 0.30)',
    padding: 16,
    marginBottom: 18,
  },
  errorCode: {
    color: '#fecdd3',
    fontSize: 14,
    fontWeight: '900',
    marginBottom: 6,
  },
  errorMessage: {
    color: 'rgba(255, 228, 230, 0.80)',
    fontSize: 14,
    lineHeight: 20,
  },
  actions: {
    gap: 10,
  },
  button: {
    width: '100%',
    minHeight: 54,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 8,
    backgroundColor: '#e5fbff',
    paddingHorizontal: 22,
    paddingVertical: 15,
  },
  buttonPressed: {
    opacity: 0.86,
    transform: [{ scale: 0.99 }],
  },
  buttonDisabled: {
    opacity: 0.82,
  },
  buttonText: {
    color: '#06101c',
    fontSize: 16,
    fontWeight: '900',
  },
  secondaryButton: {
    width: '100%',
    minHeight: 50,
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.18)',
    backgroundColor: 'rgba(255, 255, 255, 0.09)',
    paddingHorizontal: 22,
    paddingVertical: 12,
  },
  secondaryButtonPressed: {
    backgroundColor: 'rgba(255, 255, 255, 0.14)',
  },
  secondaryButtonText: {
    color: '#edf8ff',
    fontSize: 14,
    fontWeight: '800',
  },
});
