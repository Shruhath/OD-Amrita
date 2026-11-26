/**
 * The university lockup and the developer credit, as the previous app's footer
 * carried them. The old version fetched the logo from Firestore at runtime and
 * fell back to a placeholder when that failed; the asset is a static file here,
 * so there is no fetch and nothing to fall back to.
 */
import { sourceCodeURL } from '../lib/sourceCode'
import { InstallButton } from './InstallPrompt'

export function AppFooter() {
  return (
    <footer className="app-footer">
      <div className="app-footer__inner">
        <img
          className="app-footer__logo"
          src="/brand/amrita-logo.png"
          alt="Amrita Vishwa Vidyapeetham"
          width={600}
          height={155}
          loading="lazy"
          decoding="async"
        />
        {/*
          A tel: link, so it dials on the phone this is mostly used from. The
          number is in E.164 (+91) because that form works from any handset,
          including one roaming; the label stays the name.
        */}
        <p className="app-footer__credit">
          <span>Contact:</span>
          <a href="tel:+917995944735" aria-label="Call Shruhath on 7995944735">
            Shruhath
          </a>
          {/* The way back for anyone who dismissed the banner. Renders nothing
              once the app is installed, and nothing at all on a browser that
              cannot install it. */}
          <InstallButton className="app-footer__install" />
          {/* The AGPL source offer. A new tab, so an installed app does not
              lose its own window to the repository page. */}
          <a className="app-footer__source" href={sourceCodeURL()} target="_blank" rel="noopener noreferrer">
            Source code
          </a>
        </p>
      </div>
    </footer>
  )
}
