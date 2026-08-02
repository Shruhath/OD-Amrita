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
        <p className="app-footer__credit">
          <span>Developed by</span>
          <a href="https://www.ushodayanetworks.com" target="_blank" rel="noopener noreferrer">
            UshodayaNetworks
          </a>
        </p>
      </div>
    </footer>
  )
}
