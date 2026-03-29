describe('vitest infrastructure', () => {
  it('runs in jsdom environment', () => {
    expect(typeof window).toBe('object')
    expect(typeof document).toBe('object')
  })

  it('can import Vue reactivity', async () => {
    const { ref } = await import('vue')
    const count = ref(0)
    expect(count.value).toBe(0)
  })
})
