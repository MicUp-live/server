import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
    base64urlToBuffer,
    bufferToBase64url,
    prepareCreationOptions,
    prepareAssertionOptions,
    credentialToJSON,
    assertionToJSON,
} from './webauthn.js'

test('bufferToBase64url and base64urlToBuffer roundtrip', () => {
    const raw = 'Hello WebAuthn & Passkeys! 12345-+='
    const encoder = new TextEncoder()
    const decoder = new TextDecoder()
    const buffer = encoder.encode(raw).buffer

    const b64url = bufferToBase64url(buffer)
    assert.ok(!b64url.includes('+'), 'should not contain +')
    assert.ok(!b64url.includes('/'), 'should not contain /')
    assert.ok(!b64url.includes('='), 'should not contain =')

    const decodedBuf = base64urlToBuffer(b64url)
    const decodedStr = decoder.decode(decodedBuf)
    assert.equal(decodedStr, raw)
})

test('prepareCreationOptions converts challenge and user.id to ArrayBuffer', () => {
    const sample = {
        publicKey: {
            challenge: 'dGVzdGNoYWxsZW5nZQ',
            user: {
                id: 'dXNlcmlkMTIz',
                name: 'test@example.com',
                displayName: 'Test User',
            },
            excludeCredentials: [
                { id: 'Y3JlZGlk', type: 'public-key' },
            ],
            pubKeyCredParams: [{ alg: -7, type: 'public-key' }],
        },
    }

    const options = prepareCreationOptions(sample)
    assert.ok(options.challenge instanceof ArrayBuffer)
    assert.ok(options.user.id instanceof ArrayBuffer)
    assert.ok(options.excludeCredentials[0].id instanceof ArrayBuffer)
})

test('prepareAssertionOptions converts challenge and allowCredentials', () => {
    const sample = {
        publicKey: {
            challenge: 'YW5vdGhlcmNoYWxsZW5nZQ',
            allowCredentials: [
                { id: 'ZXhpc3RpbmdjcmVk', type: 'public-key' },
            ],
        },
    }

    const options = prepareAssertionOptions(sample)
    assert.ok(options.challenge instanceof ArrayBuffer)
    assert.ok(options.allowCredentials[0].id instanceof ArrayBuffer)
})

test('credentialToJSON formats correctly', () => {
    const mockCred = {
        id: 'cred-abc',
        rawId: new Uint8Array([1, 2, 3, 4]).buffer,
        type: 'public-key',
        response: {
            clientDataJSON: new Uint8Array([5, 6, 7]).buffer,
            attestationObject: new Uint8Array([8, 9, 10]).buffer,
            getTransports: () => ['internal', 'hybrid'],
        },
    }

    const json = credentialToJSON(mockCred)
    assert.equal(json.id, 'cred-abc')
    assert.equal(json.type, 'public-key')
    assert.equal(typeof json.rawId, 'string')
    assert.equal(typeof json.response.clientDataJSON, 'string')
    assert.equal(typeof json.response.attestationObject, 'string')
    assert.deepEqual(json.response.transports, ['internal', 'hybrid'])
})

test('assertionToJSON formats correctly', () => {
    const mockAssertion = {
        id: 'assert-xyz',
        rawId: new Uint8Array([10, 20, 30]).buffer,
        type: 'public-key',
        response: {
            clientDataJSON: new Uint8Array([40, 50]).buffer,
            authenticatorData: new Uint8Array([60, 70]).buffer,
            signature: new Uint8Array([80, 90]).buffer,
            userHandle: new Uint8Array([100, 110]).buffer,
        },
    }

    const json = assertionToJSON(mockAssertion)
    assert.equal(json.id, 'assert-xyz')
    assert.equal(json.type, 'public-key')
    assert.equal(typeof json.rawId, 'string')
    assert.equal(typeof json.response.clientDataJSON, 'string')
    assert.equal(typeof json.response.authenticatorData, 'string')
    assert.equal(typeof json.response.signature, 'string')
    assert.equal(typeof json.response.userHandle, 'string')
})
