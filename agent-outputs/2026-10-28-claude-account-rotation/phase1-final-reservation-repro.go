// Standalone review reproduction, not a project test. Synthetic paths only.
package main
import (
 "context"
 "crypto/sha256"
 "fmt"
 "amagi-codebox/internal/remote"
)
func main() {
 // Same byte layout as claudeCredentialsFingerprintFor, generation=0.
 fp := sha256.Sum256(append([]byte("claude-credentials-v1\x00/synthetic/.claude.json\x00/synthetic/auth.json\x00"), make([]byte,8)...))
 c := remote.NewSharedServiceCoordinator()
 claim, err := c.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials,fp)
 if err != nil { panic(err) }
 run, err := c.MintExternalRunIdentity("synthetic-desktop")
 if err != nil { panic(err) }
 _, err = c.AcquireForExternalRunWithAdmission(context.Background(),run,remote.SharedServiceClaudeCredentials,fp,claim)
 if err != nil { panic(err) }
 c.ReleaseLaunchAdmission(claim)
 _, remoteErr := c.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials,[32]byte{})
 fmt.Printf("absent desktop active -> remote zero fingerprint: %v\n",remoteErr)
 same, sameErr := c.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials,fp)
 fmt.Printf("same state, same fingerprint control: %v\n",sameErr)
 if remoteErr == nil || sameErr != nil { panic("counterexample not reproduced") }
 c.ReleaseLaunchAdmission(same)
 c2 := remote.NewSharedServiceCoordinator()
 pending, err := c2.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials,[32]byte{})
 if err != nil { panic(err) }
 _, desktopErr := c2.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials,fp)
 fmt.Printf("absent remote pending -> desktop canonical fingerprint: %v\n",desktopErr)
 if desktopErr == nil { panic("reverse counterexample not reproduced") }
 c2.ReleaseLaunchAdmission(pending)
}
