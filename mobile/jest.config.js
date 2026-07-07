// Red-phase ATDD runner for mobile/ (Story 1.6, Task 6). First test runner in this package.
//
// DEV — install these dev-deps in green (version-matched to Expo 57), then `pnpm install`
// so the lockfile updates (do NOT hand-edit pnpm-lock.yaml):
//
//     npx expo install jest-expo
//     pnpm --filter od-amrita-mobile add -D jest @types/jest
//
// `@types/jest` is required for `tsc --noEmit` to see describe/test/expect/jest globals,
// and for the interceptor/session modules to exist so the scaffold imports resolve.
// Until then this suite is intentionally RED (see the ATDD checklist).

/** @type {import('jest').Config} */
module.exports = {
  preset: 'jest-expo',
  testMatch: ['**/__tests__/**/*.test.ts?(x)'],
};
