//go:build ignore

// Forensic public-API reproducer. All writes confined to this report's temp directory.
package main
import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "time"
 s "amagi-codebox/internal/wslsupply"
 "amagi-codebox/internal/platform"
)
func must(e error) { if e!=nil {panic(e)} }
func hash(b []byte) string { h:=sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func put(p string,v any) { must(os.MkdirAll(filepath.Dir(p),0700)); b,e:=json.Marshal(v);must(e);must(os.WriteFile(p,b,0600)) }
func main(){
 root,e:=os.MkdirTemp("agent-outputs/review/2026-11-09-wsl-shared-skills","retired-evidence-");must(e)
 root,e=filepath.Abs(root);must(e)
 cfg:=filepath.Join(root,"state"); targetRoot:=filepath.Join(root,"target")
 unit:=s.UnitID("shared/skill/alpha")
 target:=s.TargetRef{Distro:"Ubuntu",CLI:s.CLICommon,Home:"/home/u",UNCRoot:targetRoot,Generation:"fixture-gen"}
 f:=s.StateFile{SchemaVersion:3,Generation:target.Generation,Distro:"Ubuntu",Home:target.Home,Targets:map[string]s.TargetRecord{target.Generation:{Distro:"Ubuntu",Home:target.Home,LastSeen:time.Now()}}, Units:[]s.StateEntry{{Unit:unit,Generation:target.Generation,LastOutcome:"written"}}}
 put(filepath.Join(cfg,s.StateFileName),f)
 data:=[]byte("# alpha")
 p:=filepath.Join(targetRoot,"skills/alpha/SKILL.md");must(os.MkdirAll(filepath.Dir(p),0700));must(os.WriteFile(p,data,0600))
 after:=hash([]byte(fmt.Sprintf("SKILL.md%c%d%c%s\n",0,len(data),0,hash(data))))
 op:=s.OperationIntent{Version:1,OpID:"retired-op",Kind:s.OpKindDirSwap,TargetKey:target.Generation+"|"+string(target.CLI),TargetDistro:"Ubuntu",TargetHome:target.Home,TargetCLI:string(target.CLI),Unit:unit,RelPath:"skills/alpha",AfterHash:after}
 put(filepath.Join(cfg,"wsl-config-supply-pending/retired-op.json"),op)
 c:=s.NewCoordinator(cfg,nil,s.Probes{Identity:func(context.Context,string)platform.WSLUserIdentity{return platform.WSLUserIdentity{}},ToUNC:func(string,string)string{return targetRoot},Chmod:func(context.Context,string,string,...string)error{return nil}})
 before:=c.GetStatus("Ubuntu",s.CLICommon)
 fmt.Printf("before reconcile: units=%d pending=%d\n",len(before.Units),len(before.PendingIntents))
 report:=c.ReconcilePending(context.Background(),target);fmt.Printf("reconcile=%+v\n",report.Dispositions)
 afterStatus:=c.GetStatus("Ubuntu",s.CLICommon)
 fmt.Printf("after reconcile: units=%d pending=%d\n",len(afterStatus.Units),len(afterStatus.PendingIntents))
 for _,u:=range afterStatus.Units {fmt.Printf("RESURRECTED %s outcome=%s\n",u.Unit,u.LastOutcome)}
 fmt.Println("evidence root",root)
}
