/**
 * Supabase auth client for React Native (Story 1.6) — the FIRST real code in mobile/src/.
 *
 * Under the hybrid auth model the Supabase SDK owns sessions, silent background refresh and
 * revocation; the Go backend verifies the JWT and re-enforces @amrita.edu. This module is the
 * minimum session plumbing the 401 interceptor reads from (`supabase.auth`).
 *
 * The anon/publishable key is NOT a secret in the JWT sense — the @amrita.edu gate lives in the
 * Supabase hook + the Go backend, and every request's JWT is verified server-side. Real values
 * come from env (never hardcoded/committed); construction fails fast if they are missing.
 */

import 'react-native-url-polyfill/auto';
import { AppState } from 'react-native';
import { createClient } from '@supabase/supabase-js';
import { secureStorageAdapter } from './secureStorage';

const supabaseUrl = process.env.EXPO_PUBLIC_SUPABASE_URL;
const supabaseAnonKey = process.env.EXPO_PUBLIC_SUPABASE_ANON_KEY;

if (!supabaseUrl || !supabaseAnonKey) {
  // Fail fast, mirroring the backend's config.Load philosophy — a misconfigured build should
  // break loudly at startup, not silently send unauthenticated requests.
  throw new Error(
    'Missing Supabase config: set EXPO_PUBLIC_SUPABASE_URL and EXPO_PUBLIC_SUPABASE_ANON_KEY ' +
      '(see mobile/.env.example).',
  );
}

export const supabase = createClient(supabaseUrl, supabaseAnonKey, {
  auth: {
    storage: secureStorageAdapter,
    autoRefreshToken: true,
    persistSession: true,
    // React Native has no URL to parse a session out of.
    detectSessionInUrl: false,
  },
});

// Register the AppState listener ONCE at module scope: drive Supabase's background auto-refresh
// only while the app is foregrounded. Registering more than once double-refreshes.
AppState.addEventListener('change', (state) => {
  if (state === 'active') {
    supabase.auth.startAutoRefresh();
  } else {
    supabase.auth.stopAutoRefresh();
  }
});

// Arm auto-refresh immediately if the app is ALREADY foregrounded at import time. The 'change'
// event only fires on a transition, so a cold launch straight into `active` would otherwise wait
// for the first background→foreground cycle before background refresh starts.
if (AppState.currentState === 'active') {
  supabase.auth.startAutoRefresh();
}
