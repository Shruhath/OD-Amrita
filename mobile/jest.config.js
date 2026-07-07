// jest-expo runner for mobile/ (Story 1.6, Task 6). First test runner in this package.
// Dev-deps (jest-expo, jest, @types/jest) are installed and the suite is green; run it with
// `pnpm --filter od-amrita-mobile test`. `@types/jest` also lets `tsc --noEmit` see the
// describe/test/expect/jest globals.

/** @type {import('jest').Config} */
module.exports = {
  preset: 'jest-expo',
  testMatch: ['**/__tests__/**/*.test.ts?(x)'],
};
