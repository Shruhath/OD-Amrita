import type { Session } from '@supabase/supabase-js'
import { supabase } from '../../lib/supabase/client'

export type SessionPort = {
  accessToken(): Promise<string | null>
  refresh(): Promise<string | null>
  signIn(): Promise<void>
  signOut(): Promise<void>
  subscribe(listener: (token: string | null) => void): () => void
}

const hasTestSession = () =>
  import.meta.env.MODE === 'test' && window.localStorage.getItem('od-amrita-atdd-session') === 'authenticated'

export const browserSession: SessionPort = {
  async accessToken() {
    if (hasTestSession()) return 'atdd-access-token'
    const { data, error } = await supabase.auth.getSession()
    if (error) throw error
    return data.session?.access_token ?? null
  },

  async refresh() {
    if (hasTestSession()) return 'atdd-access-token'
    const { data, error } = await supabase.auth.refreshSession()
    if (error) return null
    return data.session?.access_token ?? null
  },

  async signIn() {
    const { error } = await supabase.auth.signInWithOAuth({
      provider: 'azure',
      options: {
        redirectTo: new URL('/portal/events', window.location.origin).toString(),
        scopes: 'openid email profile',
      },
    })
    if (error) throw error
  },

  async signOut() {
    window.localStorage.removeItem('od-amrita-atdd-session')
    const { error } = await supabase.auth.signOut()
    if (error) throw error
  },

  subscribe(listener) {
    const { data } = supabase.auth.onAuthStateChange((_event, session: Session | null) => {
      listener(session?.access_token ?? null)
    })
    return () => data.subscription.unsubscribe()
  },
}
