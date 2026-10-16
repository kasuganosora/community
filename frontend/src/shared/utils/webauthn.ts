import { t } from '@/shared/i18n'
function bufferToBase64url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

function base64urlToBuffer(value: string): ArrayBuffer {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(value.length / 4) * 4, '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

type PublicKeyJSON = Record<string, unknown>

function decodeCreationOptions(publicKey: PublicKeyJSON): PublicKeyCredentialCreationOptions {
  return {
    ...publicKey,
    challenge: base64urlToBuffer(String(publicKey.challenge)),
    user: {
      ...(publicKey.user as Record<string, unknown>),
      id: base64urlToBuffer(String((publicKey.user as { id: string }).id)),
    },
    excludeCredentials: Array.isArray(publicKey.excludeCredentials)
      ? (publicKey.excludeCredentials as { id: string }[]).map((item) => ({ ...item, id: base64urlToBuffer(item.id) }))
      : undefined,
  } as unknown as PublicKeyCredentialCreationOptions
}

function decodeRequestOptions(publicKey: PublicKeyJSON): PublicKeyCredentialRequestOptions {
  return {
    ...publicKey,
    challenge: base64urlToBuffer(String(publicKey.challenge)),
    allowCredentials: Array.isArray(publicKey.allowCredentials)
      ? (publicKey.allowCredentials as { id: string }[]).map((item) => ({ ...item, id: base64urlToBuffer(item.id) }))
      : undefined,
  } as unknown as PublicKeyCredentialRequestOptions
}

function credentialToJSON(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response
  const common = {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults(),
  }
  if ('attestationObject' in response) {
    const attestation = response as AuthenticatorAttestationResponse
    return {
      ...common,
      response: {
        clientDataJSON: bufferToBase64url(attestation.clientDataJSON),
        attestationObject: bufferToBase64url(attestation.attestationObject),
        transports: typeof attestation.getTransports === 'function' ? attestation.getTransports() : undefined,
      },
    }
  }
  const assertion = response as AuthenticatorAssertionResponse
  return {
    ...common,
    response: {
      clientDataJSON: bufferToBase64url(assertion.clientDataJSON),
      authenticatorData: bufferToBase64url(assertion.authenticatorData),
      signature: bufferToBase64url(assertion.signature),
      userHandle: assertion.userHandle ? bufferToBase64url(assertion.userHandle) : undefined,
    },
  }
}

interface CeremonyOptions {
  publicKey: PublicKeyJSON
}

export async function createPasskey(options: CeremonyOptions): Promise<Record<string, unknown>> {
  const credential = await navigator.credentials.create({
    publicKey: decodeCreationOptions(options.publicKey),
  })
  if (!(credential instanceof PublicKeyCredential)) throw new Error(t('shared.webauthn.s1'))
  return credentialToJSON(credential)
}

export async function getPasskey(options: CeremonyOptions): Promise<Record<string, unknown>> {
  const credential = await navigator.credentials.get({
    publicKey: decodeRequestOptions(options.publicKey),
  })
  if (!(credential instanceof PublicKeyCredential)) throw new Error(t('shared.webauthn.s1'))
  return credentialToJSON(credential)
}

export function passkeySupported(): boolean {
  return typeof window !== 'undefined' && typeof window.PublicKeyCredential === 'function' && !!navigator.credentials
}
