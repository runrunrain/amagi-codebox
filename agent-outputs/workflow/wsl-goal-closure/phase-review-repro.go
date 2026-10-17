//go:build ignore

// Independent review evidence only. Synthetic data and temporary local files; no WSL or real credentials.
package main

import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"

 "amagi-codebox/internal/platform"
 w "amagi-codebox/internal/wslsupply"
)
type source struct { fail bool }
func (s *source) VisionExportBytes() ([]byte,error) { if s.fail { return nil,fmt.Errorf("synthetic source read failure") }; return nil,os.ErrNotExist }
func (*source) ModalityKBBytes() ([]byte,error) { return nil,nil }
type adapter struct { sabotage func() }
func (*adapter) Unit() w.UnitID { return "pi/review" }
func (*adapter) Policy() w.Policy { return w.PolicyTrackedProjection }
func (*adapter) Sensitive() bool { return false }
func (*adapter) RequiresConsent() bool { return false }
func (*adapter) Files() []string { return []string{"review.json"} }
func (*adapter) Plan(_ w.SupplyRequest, s *w.Snapshot, _ *w.StateEntry)(w.PlannedUnit,error) { m,_:=s.File("review.json"); return w.PlannedUnit{Action:w.ActionWriteProjection,RelPath:"review.json",ExpectedTargetHash:m.Hash},nil }
func (a *adapter) Candidate(context.Context,w.TargetRef,*w.StateEntry)(w.ProjectionCandidate,error) { if a.sabotage!=nil { a.sabotage() }; return w.ProjectionCandidate{Content:[]byte(`{"synthetic":true}`),SourceHash:"synthetic"},nil }
func must(err error){if err!=nil { panic(err) }}
func main(){
 ctx:=context.Background(); root,err:=os.MkdirTemp("","phase-review-");must(err);defer os.RemoveAll(root)
 probes:=w.Probes{ Identity:func(string)platform.WSLUserIdentity{return platform.WSLUserIdentity{UID:"1000",Home:"/home/u",MachineID:"fixture",OK:true}},ToUNC:func(d,p string)string{return filepath.Join(root,"targets",d,filepath.FromSlash(strings.TrimPrefix(p,"/")))},Chmod:func(string,string,...string)error{return nil},StatMode:func(_ ,p string)(string,bool){if strings.HasSuffix(p,".amagi-staging"){return "700",true};return "600",true}}
 // Legacy v1 authorization must not be reused, but its already-published secret must still be retired or explicitly blocked.
 cfg:=filepath.Join(root,"legacy");must(os.MkdirAll(cfg,0700)); target:=filepath.Join(root,"targets","A","home/u/.agents");must(os.MkdirAll(target,0700));secret:=[]byte(`{"api_key":"SYNTHETIC-NOT-A-KEY"}`);must(os.WriteFile(filepath.Join(target,w.VisionExportFile),secret,0600))
 legacy:=w.StateFile{SchemaVersion:1,Generation:"old-generation",Distro:"A",Home:"/home/u",Units:[]w.StateEntry{{Unit:w.UnitSharedVisionExport,Policy:w.PolicyManagedNamespace,Generation:"old-generation",Consented:true,LastWrittenHash:"legacy-hash",LastOutcome:"written"}}};b,err:=json.Marshal(legacy);must(err);must(os.WriteFile(filepath.Join(cfg,w.StateFileName),b,0600))
 c:=w.NewCoordinator(cfg,nil,probes);c.RegisterProjection(w.NewVisionExportAdapter(&source{}));r,err:=c.RunUnits(ctx,"A",w.CLICommon,w.ReasonLaunch,[]w.UnitID{w.UnitSharedVisionExport});must(err);_,err=os.Stat(filepath.Join(target,w.VisionExportFile));st:=c.GetStatus("A",w.CLICommon);consented:=false;for _,u:=range st.Units{if u.Unit==w.UnitSharedVisionExport{consented=u.Consented}}
 fmt.Printf("LEGACY_WITHDRAWAL outcome=%s detail=%s old_secret_present=%v visible_consent=%v (App gate accepts all blocked except withdrawal-unconfirmed)\n",r.Results[0].Outcome,r.Results[0].Detail,err==nil,consented)
 // Managed target + transient source failure yields Blocked, not Failed. The App gate continues this branch unconditionally.
 cfg=filepath.Join(root,"managed");c=w.NewCoordinator(cfg,nil,probes);s:=&source{fail:true};c.RegisterProjection(w.NewVisionExportAdapter(s));must(c.GrantUnitConsent(ctx,"A",w.CLICommon,w.UnitSharedVisionExport));r,err=c.RunUnits(ctx,"A",w.CLICommon,w.ReasonLaunch,[]w.UnitID{w.UnitSharedVisionExport});must(err);fmt.Printf("MANAGED_SOURCE_ERROR outcome=%s detail=%s consented=%v (App gate continues)\n",r.Results[0].Outcome,r.Results[0].Detail,c.GetStatus("A",w.CLICommon).Units[0].Consented)
 // Explicit A keep-local while current slot is B mutates B instead of A.
 cfg=filepath.Join(root,"multi");c=w.NewCoordinator(cfg,nil,probes);c.RegisterProjection(&adapter{});for _,d:=range []string{"A","B"}{_,err=c.RunUnits(ctx,d,w.CLIPi,w.ReasonManual,[]w.UnitID{"pi/review"});must(err)};must(c.DropUnitRegistration("A",w.CLIPi,"pi/review"));fmt.Printf("KEEP_LOCAL_REQUEST_A A.detached=%v B.detached=%v\n",c.GetStatus("A",w.CLIPi).Units[0].Detached,c.GetStatus("B",w.CLIPi).Units[0].Detached)
 // State failure after content commit is only logged; public result/status still claim written.
 cfg=filepath.Join(root,"statefail");c=w.NewCoordinator(cfg,nil,probes);c.RegisterProjection(&adapter{sabotage:func(){must(os.Remove(filepath.Join(cfg,w.StateFileName)));must(os.Mkdir(filepath.Join(cfg,w.StateFileName),0700))}});r,err=c.RunUnits(ctx,"A",w.CLIPi,w.ReasonManual,[]w.UnitID{"pi/review"});must(err);st=c.GetStatus("A",w.CLIPi);fmt.Printf("STATE_SAVE_FAILURE outcome=%s detail=%s memory_status=%s error=%q\n",r.Results[0].Outcome,r.Results[0].Detail,st.Units[0].LastOutcome,st.Units[0].LastErrorCode)
 _=time.Second
}
