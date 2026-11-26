/**
 * Spinner — CSS-only loading indicator with optional label.
 *
 * Sizes: 'sm' (16px), default (24px), 'lg' (36px)
 *
 * Usage:
 *   <Spinner />
 *   <Spinner size="lg" />
 *   <Spinner label="Loading songs..." />
 */

import './Spinner.css'

export function Spinner({ size, label, class: className, ...props }) {
    if (label) {
        return (
            <div class="spinner-block" {...props}>
                <div class={`spinner${size ? ` spinner-${size}` : ''}${className ? ` ${className}` : ''}`} />
                <span>{label}</span>
            </div>
        )
    }

    return (
        <div
            class={`spinner${size ? ` spinner-${size}` : ''}${className ? ` ${className}` : ''}`}
            role="status"
            aria-label="Loading"
            {...props}
        />
    )
}
