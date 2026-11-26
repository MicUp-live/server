/**
 * WebAuthn / Passkey client utility for MicUp.live.
 *
 * Handles base64url encoding/decoding, options preparation,
 * credential serialization, and capability detection for FIDO2/WebAuthn passkeys.
 */

/** Check if WebAuthn / Passkeys are supported in current browser/environment. */
export function isPasskeySupported() {
    return (
        typeof window !== 'undefined' &&
        window.PublicKeyCredential !== undefined &&
        typeof window.PublicKeyCredential === 'function' &&
        navigator.credentials !== undefined &&
        typeof navigator.credentials.create === 'function' &&
        typeof navigator.credentials.get === 'function'
    )
}

/** Check if a platform authenticator (TouchID, FaceID, Windows Hello) is available. */
export async function isPlatformAuthenticatorAvailable() {
    if (!isPasskeySupported()) return false
    try {
        if (typeof PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable === 'function') {
            return await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()
        }
    } catch {
        return false
    }
    return false
}

/** Convert a base64url string to an ArrayBuffer. */
export function base64urlToBuffer(base64url) {
    if (!base64url) return new ArrayBuffer(0)
    let base64 = String(base64url).replace(/-/g, '+').replace(/_/g, '/')
    while (base64.length % 4 !== 0) {
        base64 += '='
    }
    const binary = typeof atob === 'function' ? atob(base64) : Buffer.from(base64, 'base64').toString('binary')
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i)
    }
    return bytes.buffer
}

/** Convert an ArrayBuffer or Uint8Array to a base64url string. */
export function bufferToBase64url(buffer) {
    if (!buffer) return ''
    const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer)
    let binary = ''
    for (let i = 0; i < bytes.byteLength; i++) {
        binary += String.fromCharCode(bytes[i])
    }
    const base64 = typeof btoa === 'function' ? btoa(binary) : Buffer.from(binary, 'binary').toString('base64')
    return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

/**
 * Prepares server-provided CredentialCreation options for navigator.credentials.create().
 */
export function prepareCreationOptions(serverOptions) {
    const pk = { ...serverOptions.publicKey }

    if (typeof pk.challenge === 'string') {
        pk.challenge = base64urlToBuffer(pk.challenge)
    }
    if (pk.user && typeof pk.user.id === 'string') {
        pk.user = {
            ...pk.user,
            id: base64urlToBuffer(pk.user.id),
        }
    }
    if (Array.isArray(pk.excludeCredentials)) {
        pk.excludeCredentials = pk.excludeCredentials.map((cred) => ({
            ...cred,
            id: typeof cred.id === 'string' ? base64urlToBuffer(cred.id) : cred.id,
        }))
    }

    return pk
}

/**
 * Prepares server-provided CredentialAssertion options for navigator.credentials.get().
 */
export function prepareAssertionOptions(serverOptions) {
    const pk = { ...serverOptions.publicKey }

    if (typeof pk.challenge === 'string') {
        pk.challenge = base64urlToBuffer(pk.challenge)
    }
    if (Array.isArray(pk.allowCredentials)) {
        pk.allowCredentials = pk.allowCredentials.map((cred) => ({
            ...cred,
            id: typeof cred.id === 'string' ? base64urlToBuffer(cred.id) : cred.id,
        }))
    }

    return pk
}

/**
 * Formats a newly created PublicKeyCredential into JSON for the server verification endpoint.
 */
export function credentialToJSON(credential) {
    const transports = typeof credential.response?.getTransports === 'function'
        ? credential.response.getTransports()
        : []

    return {
        id: credential.id,
        rawId: bufferToBase64url(credential.rawId),
        type: credential.type,
        response: {
            clientDataJSON: bufferToBase64url(credential.response.clientDataJSON),
            attestationObject: bufferToBase64url(credential.response.attestationObject),
            transports: transports,
        },
    }
}

/**
 * Formats an asserted PublicKeyCredential into JSON for the server verification endpoint.
 */
export function assertionToJSON(assertion) {
    return {
        id: assertion.id,
        rawId: bufferToBase64url(assertion.rawId),
        type: assertion.type,
        response: {
            clientDataJSON: bufferToBase64url(assertion.response.clientDataJSON),
            authenticatorData: bufferToBase64url(assertion.response.authenticatorData),
            signature: bufferToBase64url(assertion.response.signature),
            userHandle: assertion.response.userHandle ? bufferToBase64url(assertion.response.userHandle) : null,
        },
    }
}
