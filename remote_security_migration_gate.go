package main

// remote_security_migration_gate.go — M1-B3c startup orchestration for the
// remote security settings migration (design §C.3 state machine lines 145-162,
// §C.4 fault/rollback lines 234-237; leader-ratification C-1/C-2).
//
// This file owns ONLY the App-layer orchestration. It consumes the frozen APIs
// in internal/settings (RemoteSecurityMigrationStore) and internal/remote
// (Server maintenance wrappers + LoadSecurityState) and never imports remote
// into settings or vice versa (ratification C-2). It performs the unique
// LoadSecurityState exactly once across all startup paths and gates Remote.Start
// on (remoteEnabled && startAllowed). No settings setters/Save are used; the
// migration operates on raw settings.json bytes. Fixed warnings carry no
// paths/values/secrets (ratification C-1: RemoteToken is never persisted state).
//
// Return contract (securityLoaded, startAllowed):
//   - (false, true):  MissingNewInstall / Current — caller does the unique
//                     LoadSecurityState + may Start.
//   - (false, false): FutureOrInvalid / ManualRepair / Detect error — caller
//                     skips LoadSecurityState AND Start (fixed warning recorded).
//   - (true,  *):     gate owned the unique LoadSecurityState (NeedsMigration).
//                     startAllowed is true only on full success; all failure /
//                     rollback / indeterminate paths return false (no Start).

import (
	"context"
	"fmt"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/settings"
)

// migrationWarn* are fixed, secret-free startup warnings. They never embed
// paths, config values, host/port or any credential. Each distinct failure gets
// its own fixed text so the desktop can show the user a precise (but
// non-leaking) reason. The manual-repair variants share a common shape.
const (
	migrationWarnDetectFailed          = "远程安全设置迁移：检测阶段失败，远程功能本次未启动。"
	migrationWarnFutureOrInvalid       = "远程安全设置迁移：检测到未知或无效的设置版本，远程功能本次未启动。"
	migrationWarnManualRepair          = "远程安全设置迁移：检测到未完成的迁移痕迹，需要手动修复后才能启动远程功能。"
	migrationWarnSecurityLoadFailed    = "远程安全设置迁移：安全状态加载失败，远程功能本次未启动。"
	migrationWarnBeginFailed           = "远程安全设置迁移：设备维护会话获取失败，远程功能本次未启动。"
	migrationWarnBackupFailed          = "远程安全设置迁移：设备存储备份失败，远程功能本次未启动。"
	migrationWarnSettingsBeginFailed   = "远程安全设置迁移：设置迁移会话获取失败，远程功能本次未启动。"
	migrationWarnStageFailed           = "远程安全设置迁移：设置暂存失败并已回滚，远程功能本次未启动。"
	migrationWarnCommitFailed          = "远程安全设置迁移：设置提交失败并已回滚，远程功能本次未启动。"
	migrationWarnCommitNotCommitted    = "远程安全设置迁移：设置提交未生效并已回滚，远程功能本次未启动。"
	migrationWarnIndeterminate         = "远程安全设置迁移：设置提交状态不确定，需要手动修复后才能启动远程功能。"
	migrationWarnPostCommitEndFailed   = "远程安全设置迁移：提交后设备维护收尾失败，需要手动修复后才能启动远程功能。"
	migrationWarnFinishFailed          = "远程安全设置迁移：迁移标记清理失败，需要手动修复后才能启动远程功能。"
	migrationWarnCleanupBackupFailed   = "远程安全设置迁移：设备备份清理失败（迁移已完成），远程功能可正常启动。"
	migrationWarnRollbackIndeterminate = "远程安全设置迁移：回滚过程中出现不确定状态，需要手动修复后才能启动远程功能。"
)

// migrationFaultInjector (test-only; nil in production) lets main-package tests
// inject a failure into a specific migration/rollback/finish step to prove the
// App-layer fail-closed orchestration without package-private seams. Production
// leaves it nil so every step runs for real. Recognized step names are documented
// at each call site (rollback_* / finish).
var migrationFaultInjector func(step string) error

// injectFault returns the injected error for step, or nil in production.
func injectFault(step string) error {
	if migrationFaultInjector != nil {
		return migrationFaultInjector(step)
	}
	return nil
}

// doLoadSecurityState is the single LoadSecurityState call site owned by the
// App layer. Both the migration gate (NeedsMigration step a) and the normal
// post-gate path route through it so securityLoadAttempts is the exactly-once
// seam (0 on skip paths, 1 otherwise).
func (a *App) doLoadSecurityState() error {
	a.securityLoadAttempts++
	return a.Remote.LoadSecurityState()
}

// applyRemoteGateResult performs the post-gate remote load+start decision,
// factored out of Startup so the (securityLoaded, startAllowed) contract is
// unit-testable without the full Wails Startup.
//
//   - LoadSecurityState runs exactly once: the gate already ran it when
//     securityLoaded is true; otherwise (and only when startAllowed) it runs
//     here. Future/Manual/Detect-error paths skip it entirely (startAllowed
//     false AND securityLoaded false).
//   - Start runs only when remoteEnabled && startAllowed. Every rollback-success
//     and failure path sets startAllowed=false and thus never Starts.
func (a *App) applyRemoteGateResult(ctx context.Context, securityLoaded, startAllowed, remoteEnabled bool) {
	// LoadSecurityState failure MUST prevent Start (Major-01): a corrupted/missing
	// security state means the v1 device store is fail-closed, so publishing any
	// listener (legacy/static/v1) would expose surfaces the store can no longer
	// guard. allowStart is a local copy because startAllowed is a value parameter;
	// latching it false here propagates to the Start decision below.
	allowStart := startAllowed
	if !securityLoaded && allowStart {
		if err := a.doLoadSecurityState(); err != nil {
			allowStart = false
			a.addStartupWarning(migrationWarnSecurityLoadFailed)
		}
	}
	if remoteEnabled && allowStart {
		if err := a.Remote.Start(ctx); err != nil {
			a.Log.Warn("app", "远程服务器启动失败（不影响主功能）", err.Error())
		} else {
			a.Log.Info("app", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
		}
	} else {
		a.Log.Info("app", "远程服务器未启动；可在设置中显式启动或检查迁移警告")
	}
}

// runRemoteSecurityMigrationGate is the M1-B3c startup gate, called in Startup
// BEFORE Settings.Load(). It classifies the raw settings file and, for a v0
// document containing a legacy remoteToken, performs the linear same-process
// migration. It never widens host/port and never calls settings setters/Save.
func (a *App) runRemoteSecurityMigrationGate() (securityLoaded, startAllowed bool) {
	migStore := settings.NewRemoteSecurityMigrationStore(a.configDir)
	det, err := migStore.Detect()
	if err != nil {
		// Detect filesystem error: do not guess; stop remote + fixed warning.
		a.addStartupWarning(migrationWarnDetectFailed)
		return false, false
	}
	switch det.State {
	case settings.DetectionMissingNewInstall, settings.DetectionCurrent:
		// No migration: caller performs the unique LoadSecurityState and may Start.
		return false, true
	case settings.DetectionFutureOrInvalid:
		a.addStartupWarning(migrationWarnFutureOrInvalid)
		return false, false
	case settings.DetectionManualRepair:
		a.addStartupWarning(migrationWarnManualRepair)
		return false, false
	case settings.DetectionNeedsMigration:
		return a.runNeedsMigrationGate(migStore)
	default:
		// Unreachable: DetectionState is a closed enum. Fail closed.
		a.addStartupWarning(migrationWarnDetectFailed)
		return false, false
	}
}

// runNeedsMigrationGate executes the linear migration state machine (design
// §C.3 steps 3-9, task spec steps a-j). Remote is stopped; step a IS the unique
// LoadSecurityState. Every failure path returns (true, false): the gate owned
// the load so the caller does not retry, and Start is forbidden.
func (a *App) runNeedsMigrationGate(migStore *settings.RemoteSecurityMigrationStore) (securityLoaded, startAllowed bool) {
	// a. Load security state — the unique load for this process. Remote is not
	//    started. On error no mutation happens and we must not Start.
	if err := a.doLoadSecurityState(); err != nil {
		a.addStartupWarning(migrationWarnSecurityLoadFailed)
		return true, false
	}

	// b. Begin device store maintenance (blocks normal security ops for the epoch).
	sess, err := a.Remote.BeginDeviceStoreMaintenance()
	if err != nil {
		a.addStartupWarning(migrationWarnBeginFailed)
		return true, false
	}

	// c. Complete device store backup. On failure abort maintenance (no store
	//    write occurred) — device backup dir is cleaned by Abort's terminal state.
	backup, err := a.Remote.BackupDeviceStoreForMigration(sess)
	if err != nil {
		_ = a.Remote.AbortDeviceStoreMaintenance(sess)
		a.addStartupWarning(migrationWarnBackupFailed)
		return true, false
	}

	// d. Settings migration capability. On failure clean the device backup +
	//    abort maintenance (nothing staged yet).
	mig, err := migStore.Begin()
	if err != nil {
		_ = a.Remote.CleanupMigrationBackup(backup)
		_ = a.Remote.AbortDeviceStoreMaintenance(sess)
		a.addStartupWarning(migrationWarnSettingsBeginFailed)
		return true, false
	}

	// e. Stage the candidate (txn dir + backup + prepared marker + candidate).
	//    On failure run the full recoverable rollback. Even on rollback success
	//    this process must not Start (design §C.4).
	if err := mig.Stage(); err != nil {
		if a.migrationRecoverableRollback(sess, backup, mig) {
			a.addStartupWarning(migrationWarnStageFailed)
		}
		return true, false
	}

	// f. Commit: rename candidate→settings.json + byte readback classification.
	res, err := mig.Commit()
	if err != nil {
		if a.migrationRecoverableRollback(sess, backup, mig) {
			a.addStartupWarning(migrationWarnCommitFailed)
		}
		return true, false
	}
	switch res.Outcome {
	case settings.CommitNotCommitted:
		// Rename did not replace settings.json — full rollback, no Start.
		if a.migrationRecoverableRollback(sess, backup, mig) {
			a.addStartupWarning(migrationWarnCommitNotCommitted)
		}
		return true, false
	case settings.CommitIndeterminate:
		// Latch: keep marker + settings backup (settings Abort) AND keep the
		// device backup dir (remote Abort). Next boot Detect → ManualRepair.
		_ = mig.Abort() // capability already killed by Commit; harmless no-op
		_ = a.Remote.AbortDeviceStoreMaintenance(sess)
		a.addStartupWarning(migrationWarnIndeterminate)
		return true, false
	case settings.CommitCommitted:
		// exact-new — proceed to post-commit validate+end (step g).
	}

	// g. Validate + End device store maintenance (store was never written by the
	//    migration; this freezes the validated generation). On failure latch:
	//    settings Abort (keep marker+backup) + remote Abort (keep device backup).
	if vErr := a.Remote.ValidateDeviceStoreMaintenance(sess); vErr != nil {
		_ = mig.Abort()
		_ = a.Remote.AbortDeviceStoreMaintenance(sess)
		a.addStartupWarning(migrationWarnPostCommitEndFailed)
		return true, false
	}
	if eErr := a.Remote.EndDeviceStoreMaintenance(sess); eErr != nil {
		_ = mig.Abort()
		_ = a.Remote.AbortDeviceStoreMaintenance(sess)
		a.addStartupWarning(migrationWarnPostCommitEndFailed)
		return true, false
	}

	// h. Finish: delete the token-bearing settings backup → candidate → marker →
	//    txn dir → parent Sync (settings capability). Every step is checked; a
	//    failure keeps the remaining artifacts + the device backup — next boot
	//    Detect → ManualRepair, no Start. The injector lets App-layer tests prove
	//    this gate branch without the package-private settings seams.
	if err := injectFault("finish"); err != nil {
		a.addStartupWarning(migrationWarnFinishFailed)
		return true, false
	}
	if err := mig.Finish(); err != nil {
		a.addStartupWarning(migrationWarnFinishFailed)
		return true, false
	}

	// i. Cleanup device store backup. Migration is already complete (marker
	//    cleared); a cleanup failure is warn-only and Start is still allowed.
	if err := a.Remote.CleanupMigrationBackup(backup); err != nil {
		a.addStartupWarning(migrationWarnCleanupBackupFailed)
	}

	// j. Success. Settings.Load (caller) now reads the v1 file; host/port/enabled
	//    preserved by the candidate; Start decision left to caller.
	return true, true
}

// migrationRecoverableRollback runs the full same-process rollback for the
// Stage-failure / Commit-error / NotCommitted paths (design §C.4, task spec
// steps e & f-NotCommitted). Order follows the authority §C.4 exactly, with the
// R2-Major-02 fix splitting the settings restore from the artifact cleanup:
//
//  1. device restore            — RestoreDeviceStoreMigrationBackup
//  2. Validate                   — ValidateDeviceStoreMaintenance
//  3. settings exact-old restore — mig.RestoreExactOld (restores settings.json,
//     KEEPS marker/backup/candidate/txn — capability NOT killed)
//  4. End                        — EndDeviceStoreMaintenance
//  5. settings discard           — mig.DiscardTransaction (marker/backup/
//     candidate/txn removed + Sync + capability killed; ONLY after End success)
//  6. device backup cleanup      — CleanupMigrationBackup (best-effort)
//
// Critical section (steps 1-4): until End succeeds BOTH the settings marker/
// backup/txn AND the device backup are preserved. Any failure here Abort/
// latches both capabilities, records the manual-repair warning, and returns
// false — next Detect sees the txn dir → ManualRepair.
//
// Step 5 (DiscardTransaction) runs only after End: a failure leaves the settings
// txn dir (or part of it) on disk → ManualRepair; the device backup is also kept
// (cleanup never reached).
//
// Step 6 (device backup cleanup) is best-effort: by then the settings marker is
// gone and settings.json is restored to exact-old (next Detect → NeedsMigration,
// re-migratable). A cleanup failure leaves an orphan device backup dir (storage
// leak, not ManualRepair) and is warn-only; the rollback is still considered
// successful (returns true) — the caller always forbids Start regardless.
//
// Each step's error is checked. The test-only migrationFaultInjector can
// short-circuit any step to prove the App-layer fail-closed orchestration
// (production leaves it nil). The recovery Abort calls in the critical section
// are intentionally not error-checked: AbortDeviceStoreMaintenance is an
// idempotent terminal operation (always returns nil; latches if invalid), and
// mig.Abort is a no-op once RestoreExactOld/DiscardTransaction killed the
// settings capability (returns ErrMigrationClosed).
func (a *App) migrationRecoverableRollback(sess remote.MaintenanceSession, backup remote.DeviceStoreBackup, mig *settings.Migration) bool {
	critical := []struct {
		name string
		run  func() error
	}{
		{"rollback_device_restore", func() error { return a.Remote.RestoreDeviceStoreMigrationBackup(sess, backup) }},
		{"rollback_validate", func() error { return a.Remote.ValidateDeviceStoreMaintenance(sess) }},
		{"rollback_settings_restore", func() error { return mig.RestoreExactOld() }},
		{"rollback_end", func() error { return a.Remote.EndDeviceStoreMaintenance(sess) }},
	}
	for _, s := range critical {
		if err := injectFault(s.name); err != nil {
			a.rollbackAbort(sess, mig)
			a.addStartupWarning(migrationWarnRollbackIndeterminate)
			return false
		}
		if err := s.run(); err != nil {
			a.rollbackAbort(sess, mig)
			a.addStartupWarning(migrationWarnRollbackIndeterminate)
			return false
		}
	}
	// End succeeded: the remote maintenance epoch is closed. Now it is safe to
	// discard the settings transaction artifacts. The capability is killed by
	// DiscardTransaction, so no mig.Abort is needed here. A failure leaves the
	// settings txn dir (or part of it) on disk → ManualRepair; the device backup
	// is also retained (cleanup below never reached).
	if err := injectFault("rollback_settings_discard"); err != nil {
		a.addStartupWarning(migrationWarnRollbackIndeterminate)
		return false
	}
	if err := mig.DiscardTransaction(); err != nil {
		a.addStartupWarning(migrationWarnRollbackIndeterminate)
		return false
	}
	// DiscardTransaction succeeded: settings marker/backup/txn gone, settings.json
	// restored to exact-old → next Detect → NeedsMigration (re-migratable). The
	// device backup cleanup is best-effort: a failure leaves an orphan backup dir
	// (storage leak, not ManualRepair) and is warn-only.
	if err := injectFault("rollback_cleanup"); err != nil {
		a.addStartupWarning(migrationWarnCleanupBackupFailed)
		return true
	}
	if err := a.Remote.CleanupMigrationBackup(backup); err != nil {
		a.addStartupWarning(migrationWarnCleanupBackupFailed)
	}
	return true
}

// rollbackAbort is the indeterminate-rollback recovery: Abort/latch both
// capabilities so the settings marker + device backup are preserved on disk for
// manual repair (next Detect → ManualRepair). It is safe at any rollback step:
// mig.Abort is a no-op once Rollback/Commit killed the settings capability, and
// AbortDeviceStoreMaintenance latches + terminates an already-ended session
// idempotently.
func (a *App) rollbackAbort(sess remote.MaintenanceSession, mig *settings.Migration) {
	_ = mig.Abort()                                // keep settings marker+backup; ErrMigrationClosed if already killed
	_ = a.Remote.AbortDeviceStoreMaintenance(sess) // latch if store invalid; keep device backup
}
