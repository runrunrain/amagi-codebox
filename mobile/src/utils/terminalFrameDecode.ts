export function decodeBase64ToUint8(base64: string): Uint8Array {
  const normalized = base64.trim()
  if (!normalized || normalized.length % 4 === 1 || /[^A-Za-z0-9+/=]/.test(normalized)) {
    throw new Error('Invalid base64 terminal frame data')
  }

  const binary = atob(normalized)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}
