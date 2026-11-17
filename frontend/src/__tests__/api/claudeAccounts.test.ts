import { beforeEach, describe, expect, it, vi } from 'vitest';

// 在 wailsjs 绑定边界打桩（仓库惯例：测试不真调 wailsjs）。
const {
  GetClaudeAccounts,
  BeginClaudeAccountEnrollment,
  CaptureClaudeAccount,
  CancelClaudeAccountEnrollment,
  SwitchClaudeAccount,
  GetClaudeAccountOperation,
  SetClaudeAccountRotation,
  ReconcileClaudeAccounts,
  RemoveClaudeAccount,
  RenameClaudeAccount,
} = vi.hoisted(() => ({
  GetClaudeAccounts: vi.fn(),
  BeginClaudeAccountEnrollment: vi.fn(),
  CaptureClaudeAccount: vi.fn(),
  CancelClaudeAccountEnrollment: vi.fn(),
  SwitchClaudeAccount: vi.fn(),
  GetClaudeAccountOperation: vi.fn(),
  SetClaudeAccountRotation: vi.fn(),
  ReconcileClaudeAccounts: vi.fn(),
  RemoveClaudeAccount: vi.fn(),
  RenameClaudeAccount: vi.fn(),
}));

vi.mock('../../../wailsjs/go/main/App', () => ({
  GetClaudeAccounts,
  BeginClaudeAccountEnrollment,
  CaptureClaudeAccount,
  CancelClaudeAccountEnrollment,
  SwitchClaudeAccount,
  GetClaudeAccountOperation,
  SetClaudeAccountRotation,
  ReconcileClaudeAccounts,
  RemoveClaudeAccount,
  RenameClaudeAccount,
}));

import {
  CLAUDE_ACCOUNT_ERROR_MESSAGES,
  CLAUDE_ROTATION_RISK_ACK_VERSION,
  claudeAccountErrorMessage,
  parseClaudeAccountError,
  beginClaudeAccountEnrollment,
  captureClaudeAccount,
  cancelClaudeAccountEnrollment,
  getClaudeAccountOperation,
  reconcileClaudeAccounts,
  removeClaudeAccount,
  renameClaudeAccount,
  setClaudeAccountRotation,
  switchClaudeAccount,
} from '../../api/claudeAccounts';

/** 1b 错误码全集（design §7.2 + facade OPERATION_NOT_FOUND）。 */
const EXPECTED_CODES = [
  'UNSUPPORTED_PLATFORM',
  'POOL_LOCKED',
  'POOL_MISSING',
  'POOL_CORRUPT',
  'REVISION_CONFLICT',
  'IDENTITY_MISMATCH',
  'IDENTITY_UNVERIFIED',
  'SESSIONS_ACTIVE',
  'EXTERNAL_SESSION_UNCERTAIN',
  'NATIVE_LOCK_BUSY',
  'EXTERNAL_ACCOUNT_CHANGE',
  'NEEDS_RECONCILE',
  'REFRESH_UNCERTAIN',
  'NEEDS_LOGIN',
  'UNSUPPORTED_CREDENTIAL_SCHEMA',
  'OPERATION_NOT_FOUND',
] as const;

describe('api/claudeAccounts（错误码映射 + 绑定包装）', () => {
  beforeEach(() => {
    [
      GetClaudeAccounts,
      BeginClaudeAccountEnrollment,
      CaptureClaudeAccount,
      CancelClaudeAccountEnrollment,
      SwitchClaudeAccount,
      GetClaudeAccountOperation,
      SetClaudeAccountRotation,
      ReconcileClaudeAccounts,
      RemoveClaudeAccount,
      RenameClaudeAccount,
    ].forEach((fn) => fn.mockReset());
  });

  describe('错误码 → 中文文案映射（全集）', () => {
    it('1b 错误码全集每个都有非空中文文案且不含英文码原文', () => {
      for (const code of EXPECTED_CODES) {
        const msg = CLAUDE_ACCOUNT_ERROR_MESSAGES[code];
        expect(msg, `缺少 ${code} 的文案`).toBeTruthy();
        expect(msg).not.toContain(code);
        // 中文文案（含 CJK 字符）
        expect(/[\u4e00-\u9fff]/.test(msg)).toBe(true);
      }
    });

    it('映射表不含未知码（与 1b 全集一致，避免拼写漂移）', () => {
      const extra = Object.keys(CLAUDE_ACCOUNT_ERROR_MESSAGES).filter(
        (k) => !(EXPECTED_CODES as readonly string[]).includes(k),
      );
      expect(extra).toEqual([]);
    });
  });

  describe('parseClaudeAccountError（正向 + 负向配对）', () => {
    it('解析 "CODE: detail" 形态的 Error（正向）', () => {
      const { code, detail } = parseClaudeAccountError(new Error('SESSIONS_ACTIVE: 共享凭据 admission 被拒'));
      expect(code).toBe('SESSIONS_ACTIVE');
      expect(detail).toBe('共享凭据 admission 被拒');
    });

    it('解析无 detail 的裸错误码（正向）', () => {
      expect(parseClaudeAccountError(new Error('POOL_MISSING')).code).toBe('POOL_MISSING');
      expect(parseClaudeAccountError('POOL_MISSING').detail).toBe('');
    });

    it('普通中文消息不误判为错误码（负向）', () => {
      const { code, detail } = parseClaudeAccountError(new Error('网络错误：连接被重置'));
      expect(code).toBe('');
      expect(detail).toBe('网络错误：连接被重置');
    });

    it('非 Error 输入与空值安全（负向）', () => {
      expect(parseClaudeAccountError(undefined).code).toBe('');
      expect(parseClaudeAccountError(undefined).detail).toBe('');
      expect(parseClaudeAccountError(null).detail).toBe('');
      expect(parseClaudeAccountError(123).detail).toBe('123');
    });
  });

  describe('claudeAccountErrorMessage', () => {
    it('已知码 → 中文文案 + 后端补充信息（正向）', () => {
      const msg = claudeAccountErrorMessage(new Error('IDENTITY_MISMATCH: accountUuid 不一致'));
      expect(msg).toContain('不是同一账号');
      expect(msg).toContain('accountUuid 不一致');
    });

    it('已知码无补充信息 → 仅中文文案（正向）', () => {
      expect(claudeAccountErrorMessage(new Error('NEEDS_LOGIN'))).toBe('账号凭据已失效，需要重新登录');
    });

    it('未知码 → 兜底文案（负向）', () => {
      const msg = claudeAccountErrorMessage(new Error('SOMETHING_ELSE: 玄学失败'));
      expect(msg).toContain('操作失败');
      expect(msg).toContain('玄学失败');
    });

    it('无码普通错误 → 兜底文案 + 原文（负向）', () => {
      expect(claudeAccountErrorMessage(new Error('boom'))).toContain('boom');
    });
  });

  describe('facade 包装参数顺序（与 wailsjs 绑定一致）', () => {
    it('begin/capture/cancel/switch/operation/rotation/reconcile/remove/rename 逐一核对', async () => {
      BeginClaudeAccountEnrollment.mockResolvedValue('enr-1');
      await beginClaudeAccountEnrollment(7);
      expect(BeginClaudeAccountEnrollment).toHaveBeenCalledWith(7);

      CaptureClaudeAccount.mockResolvedValue('op-1');
      await captureClaudeAccount('enr-1', '别名', 8);
      expect(CaptureClaudeAccount).toHaveBeenCalledWith('enr-1', '别名', 8);

      CancelClaudeAccountEnrollment.mockResolvedValue(undefined);
      await cancelClaudeAccountEnrollment(9);
      expect(CancelClaudeAccountEnrollment).toHaveBeenCalledWith(9);

      SwitchClaudeAccount.mockResolvedValue('op-sw');
      await switchClaudeAccount('acc-b', 3, 'req-1');
      expect(SwitchClaudeAccount).toHaveBeenCalledWith('acc-b', 3, 'req-1');

      GetClaudeAccountOperation.mockResolvedValue({ id: 'op-1', state: 'running' });
      await getClaudeAccountOperation('op-1');
      expect(GetClaudeAccountOperation).toHaveBeenCalledWith('op-1');

      SetClaudeAccountRotation.mockResolvedValue(undefined);
      await setClaudeAccountRotation(true, 10, CLAUDE_ROTATION_RISK_ACK_VERSION);
      expect(SetClaudeAccountRotation).toHaveBeenCalledWith(true, 10, 'claude-rotation-v1');

      ReconcileClaudeAccounts.mockResolvedValue({ inspect: null });
      await reconcileClaudeAccounts('adopt-current', 11);
      expect(ReconcileClaudeAccounts).toHaveBeenCalledWith('adopt-current', 11);

      RemoveClaudeAccount.mockResolvedValue(undefined);
      await removeClaudeAccount('acc-a', 12);
      expect(RemoveClaudeAccount).toHaveBeenCalledWith('acc-a', 12);

      RenameClaudeAccount.mockResolvedValue(undefined);
      await renameClaudeAccount('acc-a', '新别名', 13);
      expect(RenameClaudeAccount).toHaveBeenCalledWith('acc-a', '新别名', 13);
    });

    it('包装失败原样 rethrow 并带上下文日志（callApi 惯例）', async () => {
      const err = new Error('POOL_LOCKED: 已被占用');
      GetClaudeAccountOperation.mockRejectedValueOnce(err);
      const warn = vi.spyOn(console, 'error').mockImplementation(() => {});
      await expect(getClaudeAccountOperation('op-x')).rejects.toThrow('POOL_LOCKED');
      expect(warn).toHaveBeenCalled();
      warn.mockRestore();
    });
  });
});
