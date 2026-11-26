/**
 * ConnectionStatus — shows a fixed banner when the browser is offline
 * while the browser is offline.
 */

import { useState, useEffect } from 'preact/hooks'
import './ConnectionStatus.css'

export function ConnectionStatus() {
    const [online, setOnline] = useState(typeof navigator !== 'undefined' ? navigator.onLine : true)

    useEffect(() => {
        const goOnline = () => setOnline(true)
        const goOffline = () => setOnline(false)
        window.addEventListener('online', goOnline)
        window.addEventListener('offline', goOffline)
        return () => {
            window.removeEventListener('online', goOnline)
            window.removeEventListener('offline', goOffline)
        }
    }, [])

    if (!online) {
        return (
            <div class="connection-banner connection-banner-offline">
                You're offline — loaded songbook search still works
            </div>
        )
    }

    return null
}
