import { decodeBase64ToUint8 } from '../../utils/terminalFrameDecode'

describe('decodeBase64ToUint8', () => {
  it('decodes valid base64 output', () => {
    expect(Array.from(decodeBase64ToUint8('aGVsbG8='))).toEqual([104, 101, 108, 108, 111])
  })

  it('rejects invalid base64 instead of returning raw frame data', () => {
    expect(() => decodeBase64ToUint8('!!!not-valid-base64!!!')).toThrow(/Invalid base64/)
  })
})
