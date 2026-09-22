/**
 * webui → 宿主「打开外部链接」消息解析（跨仓契约 v2.7.4）：
 * token 凭证严格相等、type 精确匹配、http(s) 白名单、长度上限。
 */
import { describe, expect, it } from 'vitest'
import { OPEN_URL_MESSAGE_TYPE, parseOpenUrlMessage } from '../../../components/terminal/quickPathInsert'

const TOKEN = 'AbCdEfGhIjKlMnOpQrStUv'

describe('parseOpenUrlMessage（webui → 宿主 open-url 桥）', () => {
  it('合法消息：type/token/url 全对 → 返回 url', () => {
    const msg = { type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 'https://example.com/docs' }
    expect(parseOpenUrlMessage(msg, TOKEN)).toBe('https://example.com/docs')
  })

  it('token 不符 / 宿主无 token → null（静默拒绝伪造与过期消息）', () => {
    const msg = { type: OPEN_URL_MESSAGE_TYPE, token: 'WRONG'.repeat(6), url: 'https://example.com' }
    expect(parseOpenUrlMessage(msg, TOKEN)).toBeNull()
    expect(parseOpenUrlMessage(msg, null)).toBeNull()
  })

  it('type 不匹配 / 非对象载荷 → null', () => {
    expect(parseOpenUrlMessage({ type: 'amagi:insert-input', token: TOKEN, url: 'https://x.com' }, TOKEN)).toBeNull()
    expect(parseOpenUrlMessage('amagi:open-url', TOKEN)).toBeNull()
    expect(parseOpenUrlMessage(null, TOKEN)).toBeNull()
  })

  it('非 http(s) scheme 与超长 URL → null', () => {
    for (const url of ['javascript:alert(1)', 'file:///etc/passwd', 'mailto:a@b.c', '/relative', 'ftp://x.com']) {
      expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url }, TOKEN)).toBeNull()
    }
    const long = `https://example.com/${'a'.repeat(2100)}`
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: long }, TOKEN)).toBeNull()
  })

  it('字段类型异常（url 非 string / 空）→ null', () => {
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 42 }, TOKEN)).toBeNull()
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: '' }, TOKEN)).toBeNull()
  })
})
