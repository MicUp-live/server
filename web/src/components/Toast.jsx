import { toasts, showToast } from './toastState.js'
import './Toast.css'

export { showToast }

export function Toast() {
    if (toasts.value.length === 0) return null

    return (
        <div class="toast-container">
            {toasts.value.map(toast => (
                <div key={toast.id} class={`toast toast-${toast.type}`}>
                    {toast.message}
                </div>
            ))}
        </div>
    )
}
