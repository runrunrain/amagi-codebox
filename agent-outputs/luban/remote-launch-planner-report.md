# Remote Launch Planner/Executor Implementation Report (v2 — diting FAIL fixes)

- Status: completed
- Task: Close C-01, M-01~M-05, m-01 from diting final review (remote-real-sessions-final-review.md)

## Summary

Implemented all 7 findings from the diting FAIL:
- C-01: Bootstrap write effect with control-gated path before composite publish
- M-01: SharedStartSpec carries non-secret upstream URL + listen port; executor consumes them
- M-02: Pi/Omp provider ID = PiProviderID/OmpProviderID on config success; builder failure = typed FailureLaunchContext
- M-03: Authority restart with exact old binding close, OriginRestart plan, H1/H3 boundary, same-ID switch
- M-04: Config mutation exact CAS rollback (existed/nonexistent/concurrent-edit)
- M-05: Shared admission before effects, lease promotion with admission, transfer to session owner
- m-01: All planner tests use t.Setenv HOME/USERPROFILE; effect spies verify Start parameters
- Derived: DisposeSecrets zeroes derived env copies

## Finding → Changed Lines → Test Mapping

| Finding | Changed Files & Lines | Test Evidence |
|---|---|---|
| C-01 Windows bootstrap | `launch_planner.go`: buildBootstrapEffect for all 5 CLIs; `internal/launchplan/types.go`: BootstrapWriteSpec.StartupCommand; `launch_executor.go`: bootstrapWriteEffect; `remote_session_adapter.go`: bootstrap through DesktopBootstrap before composite | TestBuildPlanFiveCLICanonicalStructure (asserts hasBootstrap), TestBootstrapEffectCarriesStartupCommand |
| M-01 SharedStartSpec upstream/port | `internal/launchplan/types.go`: UpstreamURL+ListenPort fields; `launch_planner.go`: Claude headroom/proxy populate upstream+port; `launch_executor.go`: headroom/proxy consume shared.UpstreamURL/ListenPort | TestBuildPlanClaudeProxyHeadroomUpstreamPort, TestExecutorHeadroomProxyStartWithUpstreamPort (fake service spies) |
| M-02 Pi/Omp provider ID | `launch_planner.go`: customProviderID() sets PiProviderID/OmpProviderID; builder failure → FailureLaunchContext | TestBuildPlanPiProviderIDMatchesConfig, TestBuildPlanPiBuilderFailureFailsClosed |
| M-03 Authority restart | `remote_session_adapter.go`: authorityRestart() — PrepareLifecycle(restart), DoDeviceLifecycle(restart) with seal/close/stage/start/activate, BindPreparedRestartResult, CommitPreparedRestart | Existing TestAuthorityRestartFailsClosedWithoutReplacingBinding (now succeeds); full flow tested via integration |
| M-04 Config CAS rollback | `launch_executor.go`: configMutationEffect records existed/mode/preimage/writtenDigest; compensate only restores on CAS match, exact-delete for nonexistent, debt for mismatch | TestConfigMutationCASExisted, TestConfigMutationCASNonexistent, TestConfigMutationCASConcurrentEdit |
| M-05 Shared admission/lease | `remote_session_adapter.go`: AcquireLaunchAdmission before effects, AcquireForRunWithAdmission after, rememberSharedLeases transfer; `shared_coordinator.go`: proxy admission support | Existing integration tests pass; lifecycle verified via full suite |
| m-01 HOME isolation + spies | `launch_planner_executor_test.go`: isolatedHome() helper with t.Setenv HOME+USERPROFILE; fakeProxyService/fakeHeadroomService spies | All tests use isolatedHome; TestExecutorHeadroomProxyStartWithUpstreamPort asserts Start params |
| Derived env zeroing | `launch_executor.go`: DisposeSecrets zeroes ptyStartEffect.spec.Env.Variables | TestDisposeSecretsZeroesEnv |

## Changed Files

### New files
- `launch_planner.go` — Production planner (v2: M-01 upstream/port, M-02 provider ID, C-01 bootstrap)
- `launch_executor.go` — Production executor (v2: M-01 consume spec, M-04 CAS rollback, C-01 bootstrap effect, derived env zeroing)
- `launch_planner_executor_test.go` — L2 tests (v2: m-01 HOME isolation, effect spies, all finding coverage)

### Modified files
- `internal/launchplan/types.go` — SharedStartSpec.UpstreamURL/ListenPort; BootstrapWriteSpec.StartupCommand; exported CanonicalEffectRank
- `internal/remote/remote_session_adapter.go` — authorityCreateSession (M-05 admission/lease, C-01 bootstrap gating); authorityRestart (M-03); SetSharedLeaseRegistrar/SetRestartPtyStart/setLaunchExecutor
- `internal/remote/shared_coordinator.go` — AcquireLaunchAdmission supports proxy; isHeadroomKind helper
- `app.go` — Production planner/executor wiring; SetSharedLeaseRegistrar; SetRestartPtyStart

## Verification/Self-Test Results

| Check | Result |
|---|---|
| gofmt -l | clean |
| go vet ./... | exit 0 (only known macOS Keychain warnings) |
| go test ./... -count=1 | 32 packages PASS, 0 FAIL |
| go test -race (remote/session/launchplan/.) | all PASS |
| GOOS=windows compile | 32 packages PASS |
| GOOS=linux compile | 32 packages PASS |
| TODO/FIXME/HACK/placeholder scan | 0 matches |
| Existing tests not broken | all pass |
| .claude/ not modified | confirmed |

## Suggested Next Steps

1. diting incremental review of this diff (C-01 through M-05 + m-01)
2. wukong: Windows ConPTY runtime bootstrap E2E, mobile REST create→attach→stop E2E
3. Real CLI smoke with actual provider/key for all 5 CLIs
