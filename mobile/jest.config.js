// jest-expo harness for the OD-Amrita mobile app.
//
// Ready-to-activate: the current NFC test scaffolds are authored as `it.skip(...)`. This
// config is inert until jest-expo is installed and a `test` script is wired into
// package.json. It is intentionally not yet run by .github/workflows/mobile-ci.yml.
//
// jest-expo provides the React Native transform (Metro/babel) + module resolution so
// native modules like react-native-nfc-manager can be mocked in unit tests.

/** @type {import('jest').Config} */
module.exports = {
  preset: 'jest-expo',
  testMatch: ['**/?(*.)+(test).ts?(x)'],
  // react-native-nfc-manager (and its RN deps) ship untranspiled ESM — let jest-expo
  // transform them instead of ignoring them. Confirm the exact allowlist when installing.
  transformIgnorePatterns: [
    'node_modules/(?!((jest-)?react-native|@react-native(-community)?|expo(nent)?|@expo(nent)?/.*|react-native-nfc-manager))',
  ],
};
