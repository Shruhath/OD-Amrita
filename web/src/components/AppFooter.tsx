/**
 * The university lockup and the developer credit, as the previous app's footer
 * carried them. The old version fetched the logo from Firestore at runtime and
 * fell back to a placeholder when that failed; the asset is a static file here,
 * so there is no fetch and nothing to fall back to.
 */
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
          <span>POC:</span>
          <a href="tel:+917995944735" aria-label="Call Shruhath on 7995944735">
            Shruhath
          </a>
        </p>
      </div>
    </footer>
  )
}
