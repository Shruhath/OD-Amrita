/**
 * Where the running code can be read. The app is AGPL-3.0, which obliges a
 * network service to offer its source to the people using it, so the footer
 * carries the offer on every portal screen. A fork deploys its own repository
 * by setting VITE_SOURCE_URL; an unset or blank value falls back to this one.
 */
export const DEFAULT_SOURCE_URL = 'https://github.com/Shruhath/OD-Amrita'

export function sourceCodeURL(configured: unknown = import.meta.env.VITE_SOURCE_URL): string {
  return typeof configured === 'string' && configured.trim() !== '' ? configured.trim() : DEFAULT_SOURCE_URL
}
