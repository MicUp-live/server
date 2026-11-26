/**
 * Banner — reusable error/success/warning/info message component.
 *
 * Usage:
 *   <ErrorBanner message={error} />
 *   <SuccessBanner message={success} />
 *   <Banner type="warning" message="Something happened" />
 */

import './Banner.css'

export function Banner({ type = 'info', message, children }) {
    if (!message && !children) return null

    return (
        <div class={`banner banner-${type}`}>
            {message || children}
        </div>
    )
}

export function ErrorBanner({ message, children }) {
    return <Banner type="error" message={message}>{children}</Banner>
}

export function SuccessBanner({ message, children }) {
    return <Banner type="success" message={message}>{children}</Banner>
}
