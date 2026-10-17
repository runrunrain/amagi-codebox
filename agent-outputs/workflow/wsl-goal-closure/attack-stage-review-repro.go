//go:build ignore

// Audit runner: injects synthetic counterexamples through Go overlay only.
// Run from repository root; no production/test sources are changed.
package main

import (
 "encoding/json"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
)
func main(){
 root,e:=os.Getwd();if e!=nil{panic(e)}
 p:=filepath.Join(root,"agent-outputs/workflow/wsl-goal-closure/attack-stage-review-repro_test.go")
 b,e:=os.ReadFile(p);if e!=nil{panic(e)}
 dir,e:=os.MkdirTemp("","attack-stage-review-overlay-");if e!=nil{panic(e)};defer os.RemoveAll(dir)
 src:=filepath.Join(dir,"audit.txt");if e=os.WriteFile(src,[]byte(strings.TrimPrefix(string(b),"//go:build ignore\n")),0600);e!=nil{panic(e)}
 mapping:=map[string]any{"Replace":map[string]string{filepath.Join(root,"internal/wslsupply/attack_stage_audit_test.go"):src}}
 raw,e:=json.Marshal(mapping);if e!=nil{panic(e)};overlay:=filepath.Join(dir,"overlay.json");if e=os.WriteFile(overlay,raw,0600);e!=nil{panic(e)}
 c:=exec.Command("go","test","-overlay",overlay,"./internal/wslsupply","-run","^TestStageAuditEvidence$","-count=1","-v");c.Stdout=os.Stdout;c.Stderr=os.Stderr
 if e=c.Run();e!=nil{fmt.Fprintln(os.Stderr,e);os.Exit(1)}
}
