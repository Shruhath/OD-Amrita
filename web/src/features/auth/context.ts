import { createContext, useContext } from 'react'
import type { Identity, EventApi } from '../../api/client'

export type AuthState = {
  api: EventApi
  identity: Identity | null
  loading: boolean
  error: string | null
  signIn(): Promise<void>
  signOut(): Promise<void>
  reload(): Promise<void>
}

export const AuthContext = createContext<AuthState | null>(null)

export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used within AuthProvider')
  return value
}
