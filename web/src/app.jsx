import Router from 'preact-router'
import { useEffect, useState } from 'preact/hooks'
import { Header } from './components/Header'
import { Toast } from './components/Toast'
import { ConnectionStatus } from './components/ConnectionStatus'
import { Spinner } from './components/Spinner'
import { KjAuthGate } from './components/KjAuthGate'
import { Login } from './pages/Login'
import { Songbooks } from './pages/kj/Songbooks'
import { LiveQueue } from './pages/kj/LiveQueue'
import { Home } from './pages/Home'
import { PublicBook } from './pages/singer/PublicBook'
import { Stage } from './pages/Stage'
import { Kiosk } from './pages/Kiosk'
import { VerifyEmail } from './pages/VerifyEmail'
import { ResetPassword } from './pages/ResetPassword'
import { AcceptInvitation } from './pages/AcceptInvitation'
import { Docs } from './pages/Docs'
import { WhyMicUp } from './pages/WhyMicUp'
import { Favorites } from './pages/Favorites'
import { NotFound } from './pages/NotFound'
import { loading, restoreSession } from './auth/store'

export function App() {
  const [path, setPath] = useState(() => window.location.pathname)
  useEffect(() => {
    void restoreSession()
  }, [])

  if (loading.value) {
    return (
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        height: '100dvh',
      }}>
        <Spinner size="lg" label="Loading..." />
      </div>
    )
  }

  return (
    <div class="app-shell">
      <ConnectionStatus />
      {!path.startsWith('/kiosk') && !path.startsWith('/stage') && <Header currentPath={path} />}
      <Toast />
      <main class="page-fade">
        <Router onChange={event => setPath(event.url)}>
          <Home path="/" />
          <PublicBook path="/e/:code" />
          <PublicBook path="/event/:eventId" eventMode={true} />
          <PublicBook path="/book/:id" />
          <Stage path="/stage/event/:id" />
          <Stage path="/stage/:id" />
          <Kiosk path="/kiosk/event/:id" />
          <Kiosk path="/kiosk/:code" />
          <Login path="/login" />
          <VerifyEmail path="/verify-email" />
          <ResetPassword path="/reset-password" />
          <AcceptInvitation path="/invitations/accept" />
          <Docs path="/docs" />
          <Docs path="/docs/:section" />
          <WhyMicUp path="/why" />
          <Favorites path="/favorites" />
          <KjAuthGate path="/kj/songbooks"><Songbooks /></KjAuthGate>
          <KjAuthGate path="/kj/queue"><LiveQueue /></KjAuthGate>
          <KjAuthGate path="/kj/queue/:id"><LiveQueue /></KjAuthGate>
          <KjAuthGate path="/kj/queue/event/:id"><LiveQueue /></KjAuthGate>
          <NotFound default />
        </Router>
      </main>
    </div>
  )
}
