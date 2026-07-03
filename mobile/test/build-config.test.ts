/**
 * iOS NFC entitlement present in the resolved Expo config — failing scaffolds (pending).
 *
 * AC1 + AC5: the react-native-nfc-manager config plugin must inject the iOS entitlement
 * `com.apple.developer.nfc.readersession.formats` (["NDEF","TAG"]), the
 * `NFCReaderUsageDescription` Info.plist key, and the Android NFC uses-permission — all as
 * config-as-code (no hand-edited native dirs). This test introspects the RESOLVED Expo config
 * and fails loudly if any are missing. It is the machine-guard (AC5): a future dependency bump
 * or plugin-config regression that drops the entitlement re-fails CI.
 *
 * AC3: `NDEF` in the entitlement is what also banks the iOS NDEF *write* capability used by
 * the admin card-writer — asserting it here covers that too.
 *
 * These are `it.skip(...)`. Today `app.json` only has `plugins: ["expo-dev-client"]`, so the
 * entitlement is ABSENT — these assertions fail when un-skipped. Wire the plugin, then they
 * pass. The real on-device CoreNFC-session check is a manual milestone (no NFC radio in CI)
 * and is intentionally NOT here.
 *
 * Introspection: `npx expo config --type introspect --json` resolves plugins WITHOUT a full
 * prebuild. Confirm the exact key paths below against the installed expo-cli output when
 * implementing (the introspect schema can shift across Expo SDKs). If introspect does not
 * surface entitlements on this SDK, fall back to running `expo prebuild` into a temp dir in
 * CI and reading the generated `ios/**/*.entitlements` + `AndroidManifest.xml`.
 */
import { describe, it, expect, beforeAll } from '@jest/globals';
import { execFileSync } from 'node:child_process';
import * as path from 'node:path';

const MOBILE_ROOT = path.resolve(__dirname, '..');

function introspectExpoConfig(): any {
  const out = execFileSync('npx', ['expo', 'config', '--type', 'introspect', '--json'], {
    cwd: MOBILE_ROOT,
    encoding: 'utf8',
    maxBuffer: 1024 * 1024 * 32,
  });
  return JSON.parse(out);
}

describe('NFC entitlement + platform config (resolved Expo config)', () => {
  let config: any;
  beforeAll(() => {
    config = introspectExpoConfig();
  });

  it.skip('[P0] iOS entitlement com.apple.developer.nfc.readersession.formats includes NDEF and TAG', () => {
    const formats = config?.ios?.entitlements?.['com.apple.developer.nfc.readersession.formats'];
    expect(formats).toEqual(expect.arrayContaining(['NDEF', 'TAG']));
  });

  it.skip('[P0] Info.plist NFCReaderUsageDescription is present and non-empty', () => {
    const usage = config?.ios?.infoPlist?.NFCReaderUsageDescription;
    expect(typeof usage).toBe('string');
    expect(String(usage).trim().length).toBeGreaterThan(0);
  });

  it.skip('[P1] Android declares the android.permission.NFC uses-permission', () => {
    const perms: string[] = config?.android?.permissions ?? [];
    expect(perms).toEqual(expect.arrayContaining(['android.permission.NFC']));
  });

  it.skip('[P1] bundleIdentifier / package stay in.amrita.odamrita (App ID contract)', () => {
    // The App ID registered with Apple must match exactly; the card-writer and all future iOS
    // builds depend on it. Guard against an accidental rename alongside the NFC wiring.
    expect(config?.ios?.bundleIdentifier).toBe('in.amrita.odamrita');
    expect(config?.android?.package).toBe('in.amrita.odamrita');
  });
});
