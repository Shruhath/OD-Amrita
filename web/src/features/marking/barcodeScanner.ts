/**
 * Camera capture adapter — the single place this app touches `BarcodeDetector`
 * and `navigator.mediaDevices`.
 *
 * WHAT THE CAMERA ACTUALLY READS: not a sealed token. The card populator writes
 * the token to the NFC tag only; nothing in this MVP ever prints one. What the
 * camera sees is the barcode already printed on the physical university ID card,
 * which carries the ROLL NUMBER. This module therefore decodes TEXT, and the
 * screen above it resolves that text to a student through the search endpoint.
 *
 * Nothing above this module may reference `BarcodeDetector` or
 * `navigator.mediaDevices`. CI Chromium and jsdom have neither a camera nor the
 * detector — on Android it is backed by Google Play services, and on desktop
 * Chrome it is frequently absent — so a leak upward makes the marking screen
 * untestable without hardware.
 *
 * Sources: https://developer.chrome.com/docs/capabilities/shape-detection ·
 * https://developer.mozilla.org/en-US/docs/Web/API/Barcode_Detection_API
 */

/** Whether the camera mode can be offered, or precisely why it cannot. */
export type BarcodeScannerStatus =
  /** A detector exists on a secure origin and reports at least one format. */
  | 'available'
  /** Not a secure context, so neither the detector nor `getUserMedia` is exposed. */
  | 'insecure'
  /** No `BarcodeDetector` — typically desktop Chrome, or the feature is disabled. */
  | 'unsupported'
  /** The detector exists but decodes nothing, so offering the mode would mislead. */
  | 'no-formats'

export type BarcodeScannerErrorKind =
  | 'unsupported'
  | 'insecure'
  | 'no-formats'
  | 'permission-denied'
  | 'camera-failed'
  | 'decode-failed'

/** A camera failure the UI can render distinctly. */
export class BarcodeScannerError extends Error {
  readonly kind: BarcodeScannerErrorKind

  constructor(kind: BarcodeScannerErrorKind, message: string) {
    super(message)
    this.name = 'BarcodeScannerError'
    this.kind = kind
  }
}

export type BarcodeScanner = {
  status(): Promise<BarcodeScannerStatus>
  /**
   * Opens the camera and decodes until `signal` aborts, calling `onDecode` with
   * each decoded text. Resolves once the session has ended.
   */
  start(
    onDecode: (text: string) => void,
    onError: (error: BarcodeScannerError) => void,
    signal: AbortSignal,
  ): Promise<void>
}

// ---------------------------------------------------------------------------
// Minimal ambient shape for the Shape Detection API.
//
// TypeScript's DOM lib does not declare `BarcodeDetector`. These declarations
// are deliberately narrow — only what this adapter calls — rather than a new
// dependency, so there is nothing to keep in sync with a moving spec.
// ---------------------------------------------------------------------------

type DetectedBarcodeLike = { rawValue: string }

type BarcodeDetectorLike = {
  detect(source: CanvasImageSource): Promise<readonly DetectedBarcodeLike[]>
}

type BarcodeDetectorConstructor = {
  new (options?: { formats?: string[] }): BarcodeDetectorLike
  getSupportedFormats?: () => Promise<string[]>
}

type BarcodeWindow = Window & { BarcodeDetector?: BarcodeDetectorConstructor }

/**
 * Builds a scanner bound to `windowRef` and `navigatorRef`.
 *
 * Both are parameters rather than global reads so tests can supply fakes without
 * touching jsdom's real globals — the same shape the NFC adapter uses.
 */
export function createBarcodeScanner(windowRef: Window, navigatorRef: Navigator): BarcodeScanner {
  const target = windowRef as BarcodeWindow

  const status = async (): Promise<BarcodeScannerStatus> => {
    // Order matters, and matches the NFC adapter for the same reason: on an
    // insecure origin the API is never exposed, so checking support first would
    // report every plain-HTTP page as "unsupported" and send the coordinator
    // hunting for the wrong problem.
    if (!target.isSecureContext) return 'insecure'
    if (typeof target.BarcodeDetector !== 'function') return 'unsupported'

    // `'BarcodeDetector' in window` is not proof it can decode anything: on
    // Android the implementation is backed by Google Play services and can be
    // present while supporting no formats at all.
    const getSupportedFormats = target.BarcodeDetector.getSupportedFormats
    if (typeof getSupportedFormats !== 'function') return 'available'
    try {
      const formats = await getSupportedFormats.call(target.BarcodeDetector)
      return formats.length > 0 ? 'available' : 'no-formats'
    } catch {
      return 'no-formats'
    }
  }

  return {
    status,

    async start(onDecode, onError, signal) {
      const current = await status()
      if (current !== 'available') {
        onError(statusError(current))
        return
      }
      if (signal.aborted) return

      let stream: MediaStream
      try {
        stream = await navigatorRef.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } })
      } catch (error) {
        onError(toScannerError(error))
        return
      }

      // The camera may have been closed while the permission prompt was open.
      if (signal.aborted) {
        stopTracks(stream)
        return
      }

      // The preview element the detector reads frames from. Attaching it is
      // best-effort: a browser that refuses the stream here should still let the
      // decode loop run and report its own failures, rather than taking the
      // whole camera mode down with an exception from a display concern.
      let video: HTMLVideoElement | undefined
      try {
        video = target.document?.createElement('video')
        if (video) {
          video.srcObject = stream
          video.playsInline = true
          video.muted = true
          // A play() rejection is not fatal either: some browsers still deliver
          // frames without an explicit play.
          void video.play?.().catch(() => {})
        }
      } catch {
        video = undefined
      }

      const detector = new (target.BarcodeDetector as BarcodeDetectorConstructor)()

      let frameID: number | undefined
      let stopped = false
      // The in-flight guard. requestAnimationFrame fires far faster than a
      // decode completes, so without this the frames stack up and the phone
      // stalls under the coordinator's thumb.
      let decoding = false

      const stop = () => {
        if (stopped) return
        stopped = true
        if (frameID !== undefined) target.cancelAnimationFrame?.(frameID)
        signal.removeEventListener('abort', stop)
        if (video) {
          video.srcObject = null
          video.pause?.()
        }
        // A camera light left on after the coordinator switches modes is both a
        // battery cost across a long event and an obvious trust problem.
        stopTracks(stream)
      }

      signal.addEventListener('abort', stop)

      const tick = () => {
        if (stopped) return
        if (decoding) {
          frameID = target.requestAnimationFrame(tick)
          return
        }
        decoding = true
        detector
          .detect(video as unknown as CanvasImageSource)
          .then((results) => {
            if (stopped) return
            for (const result of results) {
              const text = result.rawValue?.trim()
              if (text) onDecode(text)
            }
          })
          .catch(() => {
            // A single failed frame is normal — the lens is moving, the card is
            // half in view. Reporting each one would bury the coordinator in red.
          })
          .finally(() => {
            decoding = false
            if (!stopped) frameID = target.requestAnimationFrame(tick)
          })
      }

      frameID = target.requestAnimationFrame(tick)
    },
  }
}

function stopTracks(stream: MediaStream) {
  for (const track of stream.getTracks()) track.stop()
}

function statusError(status: BarcodeScannerStatus): BarcodeScannerError {
  switch (status) {
    case 'insecure':
      return new BarcodeScannerError('insecure', 'The camera needs an HTTPS page. Open this screen over HTTPS and try again.')
    case 'no-formats':
      return new BarcodeScannerError('no-formats', 'This device cannot decode ID card barcodes. Use NFC or search instead.')
    default:
      return new BarcodeScannerError('unsupported', 'This browser cannot scan barcodes. Use Chrome on Android, or search instead.')
  }
}

function toScannerError(error: unknown): BarcodeScannerError {
  const name = error instanceof Error ? error.name : ''
  if (name === 'NotAllowedError' || name === 'SecurityError') {
    return new BarcodeScannerError('permission-denied', 'Camera permission was refused. Allow the camera for this site and retry.')
  }
  if (name === 'NotFoundError' || name === 'OverconstrainedError') {
    return new BarcodeScannerError('camera-failed', 'No usable camera was found on this device. Use NFC or search instead.')
  }
  return new BarcodeScannerError('camera-failed', 'The camera could not be started. Close other apps using it and retry.')
}
