package remote

import (
	"errors"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// R04：依赖缺失/指纹计算失败不得静默回退零指纹；取得 admission 后
// absence 复核失败则必须释放，不能泄漏阻塞后续 mutation。
func TestCredentialAbsenceFingerprintErrorsFailClosed(t *testing.T) {
	adapter := &RemoteSessionAdapter{sharedCoord: newTestCoordinator()}
	adapter.SetClaudeCredentialsAbsenceCheck(func() error { return nil })
	if release, err := adapter.reserveCredentialPlanAbsence(contract.CLITypeClaudeCode, false); err == nil {
		release()
		t.Fatal("未接指纹函数时必须 fail closed")
	}
	fingerprintErr := errors.New("fixture: canonical fingerprint unavailable")
	adapter.SetClaudeCredentialsFingerprint(func() ([32]byte, error) { return [32]byte{}, fingerprintErr })
	if _, err := adapter.reserveCredentialPlanAbsence(contract.CLITypeClaudeCode, false); !errors.Is(err, fingerprintErr) {
		t.Fatalf("指纹失败须上抛: %v", err)
	}
	adapter.SetClaudeCredentialsFingerprint(func() ([32]byte, error) { return fingerprintForGeneration(0), nil })
	adapter.SetClaudeCredentialsAbsenceCheck(func() error { return errSharedCredentialPlanStale })
	if _, err := adapter.reserveCredentialPlanAbsence(contract.CLITypeClaudeCode, false); !errors.Is(err, errSharedCredentialPlanStale) {
		t.Fatalf("absent 复核失败须上抛: %v", err)
	}
	mutation, err := adapter.sharedCoord.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop)
	if err != nil {
		t.Fatalf("错误路径不得遗留 reservation: %v", err)
	}
	adapter.sharedCoord.ReleaseMutationAdmission(mutation)
}
