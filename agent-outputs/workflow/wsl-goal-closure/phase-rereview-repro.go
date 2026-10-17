//go:build ignore

// Independent review evidence: synthetic data, temporary roots, public APIs only.
// Context seam models cancellation at exact filesystem state; not Windows timing evidence.
package main

import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "amagi-codebox/internal/platform"
 w "amagi-codebox/internal/wslsupply"
)
func must(e error){if e!=nil{panic(e)}}
func exists(p string)bool{_,e:=os.Stat(p);return e==nil}
type src struct{ data []byte; calls int; sabotage func() }
func(s *src)VisionExportBytes()([]byte,error){s.calls++;if s.data==nil{return nil,os.ErrNotExist};if s.calls==2&&s.sabotage!=nil{s.sabotage()};return s.data,nil}
func(*src)ModalityKBBytes()([]byte,error){return nil,nil}
type env struct{root,cfg string;p w.Probes;c *w.Coordinator;s *src}
func newEnv(root,name string)*env{e:=&env{root:filepath.Join(root,name),cfg:filepath.Join(root,name,"state")};e.p=w.Probes{Identity:func(string)platform.WSLUserIdentity{return platform.WSLUserIdentity{UID:"1000",Home:"/home/u",MachineID:"fixture",OK:true}},ToUNC:func(d,p string)string{return e.local(d,p)},Chmod:func(string,string,...string)error{return nil},StatMode:func(_,p string)(string,bool){if strings.HasSuffix(p,".amagi-staging"){return "700",true};return "600",true}};e.s=&src{data:[]byte(`{"version":1,"models":[{"id":"p/m","api_key":"SYNTHETIC"}]}`)};e.c=w.NewCoordinator(e.cfg,nil,e.p);e.c.RegisterProjection(w.NewVisionExportAdapter(e.s));return e}
func(e *env)local(d,p string)string{return filepath.Join(e.root,"targets",d,filepath.FromSlash(strings.TrimPrefix(p,"/")))}
func(e *env)file()string{return e.local("A","/home/u/.agents/"+w.VisionExportFile)}
func(e *env)run()w.UnitResult{r,x:=e.c.RunUnits(context.Background(),"A",w.CLICommon,w.ReasonLaunch,[]w.UnitID{w.UnitSharedVisionExport});must(x);return r.Results[0]}
func(e *env)consent(){must(e.c.GrantUnitConsent(context.Background(),"A",w.CLICommon,w.UnitSharedVisionExport))}
func(e *env)stateFail(){must(os.Remove(filepath.Join(e.cfg,w.StateFileName)));must(os.Mkdir(filepath.Join(e.cfg,w.StateFileName),0700))}
func stateFailure(root string,prior bool){e:=newEnv(root,fmt.Sprintf("state-%v",prior));e.consent();if prior{r:=e.run();if r.Outcome!=w.UnitApplied{panic(r)};e.s.data=[]byte(`{"version":1,"models":[{"id":"p/new","api_key":"SYNTHETIC2"}]}`)};e.s.calls=0;e.s.sabotage=e.stateFail;r:=e.run();st:=e.c.GetStatus("A",w.CLICommon);last:="";for _,u:=range st.Units{if u.Unit==w.UnitSharedVisionExport{last=u.LastOutcome}};fmt.Printf("STATE_FAIL prior=%v outcome=%s detail=%s target_present=%v pending=%v displayed=%q undo=%v\n",prior,r.Outcome,r.Detail,exists(e.file()),e.c.SensitiveCopyPending("A",w.UnitSharedVisionExport),last,r.Undo!=nil);if !prior{must(os.Remove(filepath.Join(e.cfg,w.StateFileName)));e.s.data=nil;e.s.sabotage=nil;r=e.run();fmt.Printf("STATE_FAIL_THEN_WITHDRAW outcome=%s detail=%s secret_remains=%v pending=%v\n",r.Outcome,r.Detail,exists(e.file()),e.c.SensitiveCopyPending("A",w.UnitSharedVisionExport))}}
func legacy(root string){for _,same:=range []bool{true,false}{e:=newEnv(root,fmt.Sprintf("legacy-%v",same));must(os.MkdirAll(e.cfg,0700));ug:="old-A";if same{ug="old-B"};b,x:=json.Marshal(w.StateFile{SchemaVersion:1,Distro:"B",Generation:"old-B",Home:"/home/u",Units:[]w.StateEntry{{Unit:w.UnitSharedVisionExport,Generation:ug,Consented:true,LastWrittenHash:"h",LastOutcome:"written"}}});must(x);must(os.WriteFile(filepath.Join(e.cfg,w.StateFileName),b,0600));fmt.Printf("LEGACY top_B_entry_matches=%v pending_A=%v pending_B=%v\n",same,e.c.LegacySensitiveCopyPending("A"),e.c.LegacySensitiveCopyPending("B"));if !same{must(os.MkdirAll(filepath.Dir(e.file()),0700));must(os.WriteFile(e.file(),[]byte("SYNTHETIC-OLD-COPY"),0600));e.s.data=nil;r:=e.run();fmt.Printf("LEGACY_MISMATCH_A outcome=%s detail=%s old_copy_present=%v pending_A=%v\n",r.Outcome,r.Detail,exists(e.file()),e.c.LegacySensitiveCopyPending("A")||e.c.SensitiveCopyPending("A",w.UnitSharedVisionExport))}}}
type boundaryCtx struct{context.Context;when func()bool;cancel context.CancelFunc}
func(c boundaryCtx)Err()error{if c.when(){c.cancel()};return c.Context.Err()}
type adapter struct{}
func(adapter)Unit()w.UnitID{return "pi/review"};func(adapter)Policy()w.Policy{return w.PolicyManagedNamespace};func(adapter)Sensitive()bool{return true};func(adapter)RequiresConsent()bool{return false};func(adapter)Files()[]string{return []string{"models.json"}}
func(adapter)Plan(_ w.SupplyRequest,s *w.Snapshot,_ *w.StateEntry)(w.PlannedUnit,error){m,_:=s.File("models.json");return w.PlannedUnit{Action:w.ActionWriteProjection,RelPath:"models.json",ExpectedTargetHash:m.Hash},nil}
func(adapter)Candidate(context.Context,w.TargetRef,*w.StateEntry)(w.ProjectionCandidate,error){return w.ProjectionCandidate{Content:[]byte(`{"managed":"new","user":true}`),SourceHash:"synthetic"},nil}
func recovery(root string,canceled bool){e:=newEnv(root,fmt.Sprintf("recovery-%v",canceled));f:=e.local("A","/home/u/.pi/agent/models.json");prev:=[]byte(`{"user":"ORIGINAL-UNIQUE"}`);must(os.MkdirAll(filepath.Dir(f),0700));must(os.WriteFile(f,prev,0600));attempts:=0;e.p.StatMode=func(d,p string)(string,bool){if strings.HasSuffix(p,".amagi-staging"){return "700",true};if strings.Contains(p,".amagi-staging/"){attempts++;must(os.WriteFile(e.local(d,p),[]byte("CORRUPTED"),0600))};return "600",true};c:=w.NewCoordinator(e.cfg,nil,e.p);c.RegisterProjection(adapter{});base,cancel:=context.WithCancel(context.Background());defer cancel();ctx:=boundaryCtx{Context:base,cancel:cancel,when:func()bool{b,_:=os.ReadFile(f);return canceled&&string(b)=="CORRUPTED"}};r,x:=c.RunUnits(ctx,"A",w.CLIPi,w.ReasonManual,[]w.UnitID{"pi/review"});must(x);backs,_:=filepath.Glob(filepath.Join(filepath.Dir(f),".amagi-supply-recover-*"));valid:=false;for _,p:=range backs{b,_:=os.ReadFile(p);valid=valid||string(b)==string(prev)};fmt.Printf("RECOVERY canceled=%v attempts=%d backups=%d valid_preimage=%v outcome=%s error=%v\n",canceled,attempts,len(backs),valid,r.Results[0].Outcome,r.Results[0].Err)}
func dirCancel(root string){e:=newEnv(root,"dir-cancel");host:=filepath.Join(e.root,"host");must(os.MkdirAll(filepath.Join(host,"demo"),0700));must(os.WriteFile(filepath.Join(host,"demo/SKILL.md"),[]byte("# NEW synthetic"),0600));scope:=w.NewSkillsScope(host,w.DefaultSkillsScanLimits());scope.Scan();e.c.RegisterSkills(scope);unit:=w.SkillUnitID("demo");must(e.c.GrantUnitConsent(context.Background(),"A",w.CLICommon,unit));target:=e.local("A","/home/u/.agents/skills/demo");must(os.MkdirAll(target,0700));must(os.WriteFile(filepath.Join(target,"SKILL.md"),[]byte("# USER ORIGINAL"),0600));pv,x:=e.c.PreviewUnits(context.Background(),"A",w.CLICommon,[]w.UnitID{unit});must(x);base,cancel:=context.WithCancel(context.Background());defer cancel();ctx:=boundaryCtx{Context:base,cancel:cancel,when:func()bool{ps,_:=filepath.Glob(filepath.Join(filepath.Dir(target),".amagi-supply-prev-*"));return !exists(target)&&len(ps)>0}};r,x:=e.c.TakeOverUnit(ctx,"A",w.CLICommon,unit,pv[0].TargetHash);must(x);ps,_:=filepath.Glob(filepath.Join(filepath.Dir(target),".amagi-supply-prev-*"));fmt.Printf("DIR_CANCEL outcome=%s target_present=%v hidden_preimages=%d error=%v\n",r.Outcome,exists(target),len(ps),r.Err)}
func main(){root,e:=os.MkdirTemp("","phase-rereview-");must(e);defer os.RemoveAll(root);legacy(root);stateFailure(root,false);stateFailure(root,true);recovery(root,false);recovery(root,true);dirCancel(root)}
