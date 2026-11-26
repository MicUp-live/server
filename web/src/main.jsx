import { render } from 'preact'
import './styles/global.css'
import { App } from './app'
import { registerServiceWorker } from './utils/notifications'

if (typeof window !== 'undefined') {
    registerServiceWorker().catch(() => {})
}

render(<App />, document.getElementById('app'))

