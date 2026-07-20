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
import { Pressable, StyleSheet, Text, View } from 'react-native';

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
    <View style={styles.container}>
      {state.status === 'success' ? (
        // A SINGLE Text node, deliberately: the default test identity's email domain
        // (@cb.students.amrita.edu) contains "student" as a substring of the role value
        // ("student") too — splitting these into sibling Text nodes makes every text query
        // ambiguous (matches both). One node keeps each query unique.
        <Text style={styles.identity}>
          {state.identity.name ? `${state.identity.name}\n` : null}
          {state.identity.email}
          {'\n'}
          {state.identity.subject}
          {'\n'}
          Role: {state.identity.role}
        </Text>
      ) : null}
      {state.status === 'error' ? <Text style={styles.error}>{state.code}</Text> : null}
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Round trip"
        style={styles.button}
        onPress={() => void handleRoundTrip()}
        disabled={pending}
      >
        <Text style={styles.buttonText}>Round trip</Text>
      </Pressable>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Sign out"
        style={styles.secondaryButton}
        onPress={onSignOut}
      >
        <Text style={styles.secondaryButtonText}>Sign out</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    padding: 32,
  },
  identity: {
    fontSize: 15,
    textAlign: 'center',
    marginBottom: 24,
  },
  error: {
    fontSize: 15,
    marginBottom: 16,
  },
  button: {
    alignItems: 'center',
    borderRadius: 12,
    minWidth: 200,
    paddingHorizontal: 22,
    paddingVertical: 14,
    marginBottom: 12,
  },
  buttonText: {
    fontSize: 16,
    fontWeight: '700',
  },
  secondaryButton: {
    alignItems: 'center',
    borderRadius: 12,
    minWidth: 140,
    paddingHorizontal: 22,
    paddingVertical: 12,
  },
  secondaryButtonText: {
    fontSize: 14,
    fontWeight: '600',
  },
});
