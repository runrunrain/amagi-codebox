import {describe,it,expect} from 'vitest';
import {validateCodemodeConfig} from '../../components/provider/piCodemode';
describe('CodeMode runtime preferences',()=>{
 it('accepts defaults, preferences and legacy config without independent authorization',()=>{for(const codemode of [undefined,{}, {mode:'off',roles:{leader:'off'}},{preference:'prefer',budget:{maxOutputTokensPerScript:4000,maxTimeoutMsPerScript:30000}}])expect(validateCodemodeConfig({codemode})).toBe('');});
 it('rejects malformed defaults without leaking values or modifying data',()=>{for(const codemode of [null,{preference:'SECRET'},{budget:{maxOutputTokensPerScript:0}},{budget:{maxTimeoutMsPerScript:1.5}}]){const source=JSON.stringify(codemode);expect(validateCodemodeConfig({codemode})).not.toBe('');expect(validateCodemodeConfig({codemode})).not.toContain('SECRET');expect(JSON.stringify(codemode)).toBe(source);}});
});
