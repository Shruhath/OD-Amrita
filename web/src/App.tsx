import { Application } from './Application'
import { browserSession } from './features/auth/session'
import { createPortalBrowserRouter } from './router'

const router = createPortalBrowserRouter()

export default function App() {
  return <Application session={browserSession} router={router} />
}
