import { describe, expect, it } from 'vitest';
import { advisorMode, validateAdvisorConfig } from '../../components/provider/piAdvisor';
describe('Advisor configuration', () => {
  it('distinguishes inherit, disabled, pi and specified desktop chat', () => {
    expect(advisorMode(undefined)).toBe('unset'); expect(advisorMode({model:null})).toBe('off');
    expect(advisorMode({model:'p/m:high'})).toBe('pi'); expect(advisorMode({backend:'codex-chat'})).toBe('codex-chat');
    for (const advisor of [{model:'p/m:high',roles:['luban'],maxCalls:5},{model:null},{backend:'codex-chat',codex:{socketPath:'/tmp/desktop.sock',threadId:'test-thread-123'}}]) expect(validateAdvisorConfig({advisor})).toBe('');
  });
  it('rejects invalid targets, budgets and unknown fields without losing source data', () => {
    for (const advisor of [{backend:'other'}, {model:'p/m',maxCalls:0}, {model:'p/m',leader:'yes'}, {model:'p/m',roles:[true]}, {backend:'codex-chat',codex:{socketPath:'relative',threadId:'test-thread-123'}}, {backend:'codex-chat',codex:{socketPath:'/tmp/d.sock',threadId:''}}, {model:'p/m',secret:'PRIVATE'}]) {
      const source=JSON.stringify(advisor); expect(validateAdvisorConfig({advisor})).not.toBe(''); expect(JSON.stringify(advisor)).toBe(source); expect(validateAdvisorConfig({advisor})).not.toContain('PRIVATE');
    }
  });
});
