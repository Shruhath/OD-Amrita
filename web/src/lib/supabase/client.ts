import { createClient } from '@supabase/supabase-js'

const testMode = import.meta.env.MODE === 'test'
const supabaseUrl = import.meta.env.VITE_SUPABASE_URL ?? (testMode ? 'http://127.0.0.1:54321' : '')
const supabaseAnonKey = import.meta.env.VITE_SUPABASE_ANON_KEY ?? (testMode ? 'test-anon-key' : '')

if (!supabaseUrl || !supabaseAnonKey) {
  throw new Error(
    'Missing browser auth configuration. Set VITE_SUPABASE_URL and VITE_SUPABASE_ANON_KEY.',
  )
}

export const supabase = createClient(supabaseUrl, supabaseAnonKey, {
  auth: {
    autoRefreshToken: true,
    persistSession: true,
    detectSessionInUrl: true,
    flowType: 'pkce',
    storageKey: 'sb-test-auth-token',
  },
})
