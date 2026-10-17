//go:build ignore

// Independent fault injection: mutate staged bytes at the mode-probe seam to force
// actual post-rename read-back mismatches. Bound recursion via cancellation at attempt 4.
// This proves error-path control flow, not real WSL/concurrent-editor timing.
package main
import("context";"fmt";"os";"path/filepath";"strings";"amagi-codebox/internal/platform";w "amagi-codebox/internal/wslsupply")
type adapter struct{}
func(adapter)Unit()w.UnitID{return "pi/review"};func(adapter)Policy()w.Policy{return w.PolicyManagedNamespace};func(adapter)Sensitive()bool{return true};func(adapter)RequiresConsent()bool{return false};func(adapter)Files()[]string{return []string{"models.json"}}
func(adapter)Plan(_ w.SupplyRequest,s *w.Snapshot,_ *w.StateEntry)(w.PlannedUnit,error){m,_:=s.File("models.json");return w.PlannedUnit{Action:w.ActionWriteProjection,RelPath:"models.json",ExpectedTargetHash:m.Hash},nil}
func(adapter)Candidate(context.Context,w.TargetRef,*w.StateEntry)(w.ProjectionCandidate,error){return w.ProjectionCandidate{Content:[]byte(`{"managed":"new","userSetting":true}`),SourceHash:"synthetic"},nil}
func must(e error){if e!=nil{panic(e)}}
func main(){root,e:=os.MkdirTemp("","review-restore-");must(e);defer os.RemoveAll(root);local:=func(p string)string{return filepath.Join(root,"target",filepath.FromSlash(strings.TrimPrefix(p,"/")))};f:=local("/home/u/.pi/agent/models.json");must(os.MkdirAll(filepath.Dir(f),0700));must(os.WriteFile(f,[]byte(`{"userSetting":true}`),0600));ctx,cancel:=context.WithCancel(context.Background());defer cancel();attempts:=0;p:=w.Probes{Identity:func(string)platform.WSLUserIdentity{return platform.WSLUserIdentity{UID:"1000",Home:"/home/u",MachineID:"fixture",OK:true}},ToUNC:func(_,p string)string{return local(p)},Chmod:func(string,string,...string)error{return nil},StatMode:func(_,p string)(string,bool){if strings.HasSuffix(p,".amagi-staging"){return "700",true};attempts++;if attempts==4{cancel()}else{must(os.WriteFile(local(p),[]byte("synthetic read-back mismatch"),0600))};return "600",true}};c:=w.NewCoordinator(filepath.Join(root,"state"),nil,p);c.RegisterProjection(adapter{});r,e:=c.RunUnits(ctx,"A",w.CLIPi,w.ReasonManual,[]w.UnitID{"pi/review"});must(e);_,e=os.Stat(f);fmt.Printf("REPEATED_READBACK_MISMATCH staging_attempts=%d canceled_at=4 outcome=%s mixed_user_file_deleted=%v error=%v\n",attempts,r.Results[0].Outcome,os.IsNotExist(e),r.Results[0].Err)}
