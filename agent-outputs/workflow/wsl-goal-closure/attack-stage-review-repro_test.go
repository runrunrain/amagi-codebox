//go:build ignore

package wslsupply

// Audit-only counterexamples, injected with -overlay. Never a project test.
// All roots/data are synthetic t.TempDir fixtures. Success means evidence captured,
// not acceptance. Existing package fixtures are reused; no production edits.
import (
 "context"
 "errors"
 "encoding/json"
 "os"
 "path/filepath"
 "testing"
 "amagi-codebox/internal/launcher"
)

func auditWrite(t *testing.T,p,s string) { t.Helper(); if e:=os.MkdirAll(filepath.Dir(p),0700);e!=nil{t.Fatal(e)};if e:=os.WriteFile(p,[]byte(s),0600);e!=nil{t.Fatal(e)} }
func auditExists(p string)bool{_,e:=os.Lstat(p);return e==nil}
func auditSkill(t *testing.T)(*Coordinator,*fakeProbes,UnitID,string){
 t.Helper(); fp:=newFakeProbes(t); c:=newCoordinatorWith(t,fp,nil); host:=t.TempDir();auditWrite(t,filepath.Join(host,"demo/SKILL.md"),"# NEW")
 scope:=NewSkillsScope(host,DefaultSkillsScanLimits());scope.Scan();c.RegisterSkills(scope);u:=SkillUnitID("demo");if e:=c.GrantUnitConsent(context.Background(),"Ubuntu",CLICommon,u);e!=nil{t.Fatal(e)}
 dst:=filepath.Join(fp.uncRoot,"home/u/.agents/skills/demo");auditWrite(t,filepath.Join(dst,"SKILL.md"),"# OLD");return c,fp,u,dst
}
func auditTake(t *testing.T,c *Coordinator,u UnitID,ctx context.Context)UnitResult{ t.Helper();p,e:=c.PreviewUnits(context.Background(),"Ubuntu",CLICommon,[]UnitID{u});if e!=nil{t.Fatal(e)};r,e:=c.TakeOverUnit(ctx,"Ubuntu",CLICommon,u,p[0].TargetHash);if e!=nil{t.Fatal(e)};return r }

func TestStageAuditEvidence(t *testing.T){
 t.Run("backup-path",func(t *testing.T){
  e:=newCommitTestEnv(t,true,[]byte("OLD")); inner:=e.c.probes.Chmod
  e.c.probes.Chmod=func(ctx context.Context,d,m string, ps ...string)error{for _,p:=range ps{if !auditExists(e.fp.probes().ToUNC(d,p)){return errors.New("synthetic chmod: ENOENT "+p)}};return inner(ctx,d,m,ps...)}
  r:=e.c.commitFile(context.Background(),CommitRequest{Target:e.target,Unit:"pi/audit",RelPath:e.rel,ExpectedBefore:hexHash([]byte("OLD")),Candidate:[]byte("NEW"),Sensitive:true,Kind:OpKindWrite})
  t.Logf("BACKUP detail=%s error=%v",r.ErrDetail,r.Err)
 })
 t.Run("legacy-unknown-and-hash",func(t *testing.T){for _,finger:=range []string{GenerationUnknown,legacyGenerationOf("OTHER","1000","/home/u")}{
  e:=newLegacyABEnv(t); e.writeLegacyV1TopB(t,"written",true);st:=e.c.state.load();st.LegacyEvidence[0].Fingerprint=finger;if x:=e.c.state.mutate(func(s *StateFile){s.LegacyEvidence=st.LegacyEvidence});x!=nil{t.Fatal(x)}
  auditWrite(t,e.visionFn("A"),legacyCloneCopy);a:=e.c.AssessWSLSensitiveConsumption(context.Background(),"A",CLICommon,UnitSharedVisionExport,nil);t.Logf("LEGACY finger=%s consumption=%s risk=%v reason=%s",finger,a.Consumption,a.RiskPending,a.Reason)
 }})
 t.Run("v2-migration",func(t *testing.T){
 fp:=newFakeProbes(t);dir:=t.TempDir();c:=NewCoordinator(dir,nil,fp.probes());tr,_:=c.ResolveTarget(context.Background(),"Ubuntu",CLICommon);body:="SYNTHETIC V2 SECRET";auditWrite(t,filepath.Join(tr.UNCRoot,VisionExportFile),body)
 st:=StateFile{SchemaVersion:2,Generation:tr.Generation,Distro:"Ubuntu",Home:tr.Home,Units:[]StateEntry{{Unit:UnitSharedVisionExport,Generation:tr.Generation,Consented:true,LastWrittenHash:hexHash([]byte(body)),LastOutcome:"written"}}};b,_:=json.Marshal(st);auditWrite(t,filepath.Join(dir,StateFileName),string(b));c=NewCoordinator(dir,nil,fp.probes());a:=c.AssessWSLSensitiveConsumption(context.Background(),"Ubuntu",CLICommon,UnitSharedVisionExport,nil);t.Logf("V2_MIGRATION consumption=%s risk=%v reason=%s",a.Consumption,a.RiskPending,a.Reason)
 })
 t.Run("wrong-root-reconcile",func(t *testing.T){
  e:=newCommitTestEnv(t,false,nil);shared,x:=e.c.ResolveTarget(context.Background(),"Ubuntu",CLICommon);if x!=nil{t.Fatal(x)}
  secret:=filepath.Join(shared.UNCRoot,VisionExportFile);auditWrite(t,secret,"SYNTHETIC-SECRET")
  id:=newOpID();x=e.c.state.saveIntent(OperationIntent{OpID:id,Kind:OpKindWrite,TargetKey:targetKeyOf(shared),TargetDistro:"Ubuntu",TargetHome:shared.Home,Unit:UnitSharedVisionExport,RelPath:VisionExportFile,AfterHash:hexHash([]byte("SYNTHETIC-SECRET"))});if x!=nil{t.Fatal(x)}
  c2:=NewCoordinator(filepath.Dir(e.c.state.path),nil,e.fp.probes());rep:=c2.ReconcilePending(context.Background(),e.target)
  _,pending:=c2.PendingSensitiveOperation("Ubuntu",UnitSharedVisionExport);a:=c2.AssessSensitiveConsumption(context.Background(),shared,UnitSharedVisionExport,nil)
  t.Logf("WRONG_ROOT shared_equals_pi_key=%v dispositions=%+v secret_present=%v pending=%v assess=%s",targetKeyOf(shared)==targetKeyOf(e.target),rep.Dispositions,auditExists(secret),pending,a.Consumption)
 })
 t.Run("dir-cas-and-structure",func(t *testing.T){
  c,_,u,dst:=auditSkill(t); edited:=false
  ctx:=boundaryCtx{Context:context.Background(),cancel:func(){},when:func()bool{ps,_:=filepath.Glob(filepath.Join(filepath.Dir(dst),".amagi-supply-skill-*"));if !edited&&len(ps)>0&&auditExists(dst){edited=true;auditWrite(t,filepath.Join(dst,"EXTERNAL"),"UNIQUE EDIT")};return false}}
  r:=auditTake(t,c,u,ctx); t.Logf("DIR_CAS injected=%v outcome=%s external_present=%v undo=%v",edited,r.Outcome,auditExists(filepath.Join(dst,"EXTERNAL")),r.Undo!=nil)
  if r.Undo==nil{t.Fatal("setup did not commit")}
  // Separate case: empty directories in the original tree must survive rollback.
  c,_,u,dst=auditSkill(t);if x:=os.MkdirAll(filepath.Join(dst,"empty/nested"),0700);x!=nil{t.Fatal(x)};r=auditTake(t,c,u,context.Background());if r.Undo==nil{t.Fatal("no undo")};out:=c.Compensate(context.Background(),r.Undo)
  t.Logf("DIR_ABORT disposition=%s empty_nested_preserved=%v",out[0].Disposition,auditExists(filepath.Join(dst,"empty/nested")))
 })
 t.Run("resource-abort",func(t *testing.T){
  c,fp,host:=newS4ResourceCoordinator(t);writeHostResource(t,host,"commands/demo/a.md","NEW");c.resourceScope(CLIClaude).Scan();u:=ResourceUnitID(CLIClaude,"commands/demo");if x:=c.GrantUnitConsent(context.Background(),"Ubuntu",CLIClaude,u);x!=nil{t.Fatal(x)};dst:=claudeTargetPath(fp,"commands/demo");auditWrite(t,filepath.Join(dst,"a.md"),"OLD")
  pv,x:=c.PreviewUnits(context.Background(),"Ubuntu",CLIClaude,[]UnitID{u});if x!=nil{t.Fatal(x)};r,x:=c.TakeOverUnit(context.Background(),"Ubuntu",CLIClaude,u,pv[0].TargetHash);if x!=nil||r.Undo==nil{t.Fatalf("setup %+v %v",r,x)};out:=c.Compensate(context.Background(),r.Undo);b,_:=os.ReadFile(filepath.Join(dst,"a.md"));t.Logf("RESOURCE_ABORT result=%s message=%s bytes=%s prev_exists=%v",out[0].Disposition,out[0].Message,b,auditExists(absOf(r.Undo.target,r.Undo.displacedRel)))
 })
 t.Run("directory-restart",func(t *testing.T){
  c,fp,u,_:=auditSkill(t);r:=auditTake(t,c,u,context.Background());tr,_:=c.ResolveTarget(context.Background(),"Ubuntu",CLICommon);c2:=NewCoordinator(filepath.Dir(c.state.path),nil,fp.probes());rep:=c2.ReconcilePending(context.Background(),tr);t.Logf("DIR_RESTART committed=%s reconcile=%+v",r.Outcome,rep.Dispositions)
 })
 t.Run("corrupt-pending",func(t *testing.T){
  e:=newCommitTestEnv(t,false,nil);auditWrite(t,filepath.Join(filepath.Dir(e.c.state.path),pendingDirName,"broken.json"),"{broken")
  a:=&reviewAdapter{rel:e.rel,source:func()[]byte{return []byte("NEW")}};e.c.RegisterProjection(a);r,x:=e.c.RunUnits(context.Background(),"Ubuntu",CLIPi,ReasonManual,[]UnitID{a.Unit()});t.Logf("CORRUPT_PENDING outcome=%s err=%v health=%+v",r.Results[0].Outcome,x,e.c.state.Health())
 })
 t.Run("plugin-cross-source-cleanup",func(t *testing.T){
  d:=newFakePluginDistro(t);c,tr:=newPluginTestCoordinator(t,d);id:=newOpID();if x:=c.plugins.savePluginPending(tr,id,PluginOperation{Kind:PluginOpInstall,Source:"npm:UNCONFIRMED-A@1"},PluginSourceSpec{});x!=nil{t.Fatal(x)}
  r:=c.RunPluginOperation(context.Background(),"Ubuntu",PluginOperation{Kind:PluginOpInstall,Source:"npm:demo@1.0.0",AnchorRef:"1.0.0"});_,still,x:=c.state.readIntent(id);t.Logf("PLUGIN_OTHER_SUCCESS ok=%v source_A_pending=%v err=%v",r.OK,still,x)
 })
 t.Run("a2-final-save",func(t *testing.T){
  fp:=newFakeProbes(t);c:=newCoordinatorWith(t,fp,nil);c.SetA2Adapter(stageAuditA2{c:c});r,x:=c.RunUnits(context.Background(),"Ubuntu",CLIPi,ReasonLaunch,[]UnitID{UnitPiA2Auth});if x!=nil{t.Fatal(x)};t.Logf("A2_FINAL_SAVE outcome=%s registration=%q pending_fact=%v",r.Results[0].Outcome,r.Results[0].Registration,r.Results[0].PendingOperationFact())
 })
 t.Run("status-without-target",func(t *testing.T){
  fp:=newFakeProbes(t);dir:=t.TempDir();c:=NewCoordinator(dir,nil,fp.probes());tr,_:=c.ResolveTarget(context.Background(),"Ubuntu",CLICommon);id:=newOpID();if x:=c.saveA2PendingSummary(tr,id);x!=nil{t.Fatal(x)};c2:=NewCoordinator(dir,nil,fp.probes());s:=c2.GetStatus("Ubuntu",CLICommon);t.Logf("NO_TARGET_STATUS checked=%v pending_count=%d health=%+v",s.Checked,len(s.PendingIntents),s.StoreHealth)
 })
}
type stageAuditA2 struct{c *Coordinator}
func (a stageAuditA2)Files()[]string{return []string{"auth.json"}}
func (a stageAuditA2)Sync(ctx context.Context,tr TargetRef)(launcher.SyncWSLPiLoginAuthOutcome,error){if e:=os.Remove(a.c.state.path);e!=nil&&!os.IsNotExist(e){return launcher.SyncWSLPiLoginAuthDistroNoAuth,e};if e:=os.Mkdir(a.c.state.path,0700);e!=nil{return launcher.SyncWSLPiLoginAuthDistroNoAuth,e};return launcher.SyncWSLPiLoginAuthCopied,nil}
