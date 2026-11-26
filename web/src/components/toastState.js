import { signal } from '@preact/signals'

export const toasts = signal([])

export function showToast(message, type = 'info') {
    const id = Date.now() + Math.random()
    toasts.value = [...toasts.value, { id, message, type }]

    const duration = (type === 'error' || type === 'warning') ? 6000 : 4000
    setTimeout(() => {
        toasts.value = toasts.value.filter(t => t.id !== id)
    }, duration)
}
