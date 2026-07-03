/**
 * NFC capability fallback decision — failing scaffolds (implementation pending).
 *
 * AC4: the app exposes a capability probe and a thin `nfcAvailableOrCamera()` decision.
 * When the NFC entitlement/hardware is absent in a given build, the decision routes to the
 * CAMERA capability fallback — a BUILD/DEVICE-level fallback that is DISTINCT from the
 * per-scan session-failure fallback handled elsewhere. Do not conflate the two.
 *
 * These scaffolds are `it.skip(...)` and assert the EXPECTED behavior of
 * `mobile/src/lib/nfc/capability.ts`, which does not exist yet. Implement `capability.ts`,
 * then un-skip these one at a time: fail first → implement → pass.
 *
 * Harness: jest-expo (not yet installed — see the harness activation notes). The module
 * under test is imported dynamically INSIDE each test body so the (skipped) suite loads
 * clean until activated.
 *
 * Assumed contract for capability.ts (confirm/adjust when implementing):
 *   type NfcCapability =
 *     | { status: 'available' }
 *     | { status: 'unsupported-hardware' }
 *     | { status: 'entitlement-absent' };
 *   getNfcCapability(): Promise<NfcCapability>;
 *   nfcAvailableOrCamera(): Promise<'use-nfc' | 'offer-camera'>;  // any non-available → 'offer-camera'
 */
import { describe, it, expect, jest, beforeEach } from '@jest/globals';

// Manual mock of the native module — the unit test NEVER touches a real radio.
// Confirm the exact API surface (isSupported / isEnabled / readingAvailable) against the
// installed react-native-nfc-manager version when implementing.
jest.mock('react-native-nfc-manager', () => ({
  __esModule: true,
  default: {
    start: jest.fn(async () => undefined),
    isSupported: jest.fn(async () => false),
    isEnabled: jest.fn(async () => false),
  },
}));

async function loadWithNfc(opts: { isSupported: boolean; isEnabled?: boolean }) {
  jest.resetModules();
  const NfcManager = (await import('react-native-nfc-manager')).default as {
    isSupported: jest.Mock;
    isEnabled: jest.Mock;
  };
  NfcManager.isSupported.mockResolvedValue(opts.isSupported);
  NfcManager.isEnabled.mockResolvedValue(opts.isEnabled ?? opts.isSupported);
  return await import('./capability');
}

describe('nfcAvailableOrCamera — capability fallback decision', () => {
  beforeEach(() => jest.clearAllMocks());

  it.skip('[P0] readingAvailable/isSupported=false → offer camera', async () => {
    const { nfcAvailableOrCamera } = await loadWithNfc({ isSupported: false });
    await expect(nfcAvailableOrCamera()).resolves.toBe('offer-camera');
  });

  it.skip('[P0] NFC fully available → use NFC', async () => {
    const { nfcAvailableOrCamera } = await loadWithNfc({ isSupported: true, isEnabled: true });
    await expect(nfcAvailableOrCamera()).resolves.toBe('use-nfc');
  });

  it.skip('[P1] unsupported hardware → offer camera', async () => {
    const { getNfcCapability, nfcAvailableOrCamera } = await loadWithNfc({ isSupported: false });
    await expect(getNfcCapability()).resolves.toEqual({ status: 'unsupported-hardware' });
    await expect(nfcAvailableOrCamera()).resolves.toBe('offer-camera');
  });

  it.skip('[P1] entitlement absent (supported hardware, no session) → offer camera', async () => {
    // A build missing the com.apple.developer.nfc.readersession.formats entitlement reports
    // supported hardware but cannot open a session. capability.ts must map that to
    // `entitlement-absent`, and the decision must still offer camera.
    const { getNfcCapability, nfcAvailableOrCamera } = await loadWithNfc({
      isSupported: true,
      isEnabled: false,
    });
    await expect(getNfcCapability()).resolves.toEqual({ status: 'entitlement-absent' });
    await expect(nfcAvailableOrCamera()).resolves.toBe('offer-camera');
  });

  it.skip('[P2] typed union distinguishes all three statuses', async () => {
    // Guard against a future refactor collapsing the union to a boolean — callers
    // (precise messaging + the deferred session-fallback seam) rely on the exact status.
    const { getNfcCapability } = await loadWithNfc({ isSupported: true, isEnabled: true });
    await expect(getNfcCapability()).resolves.toEqual({ status: 'available' });
  });
});
