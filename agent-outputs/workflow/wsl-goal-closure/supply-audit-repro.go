//go:build ignore

// Historical reproduction for the pre-G1 probe API; retain its original body as audit evidence.
// Audit-only reproduction, not a project test. All data synthetic; all writes in os.MkdirTemp.
package main
import (
 "context"
 "fmt"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "amagi-codebox/internal/platform"
 w "amagi-codebox/internal/wslsupply"
)
type src struct{}
func(src) ClaudeJSON()([]byte,error){return []byte(`{"mcpServers":{"demo":{"url":"https://example.invalid/mcp"}}}`),nil}
func(src) SettingsJSON()([]byte,error){return []byte(`{"model":"demo"}`),nil}
func(src) ConfigTOML()([]byte,error){return []byte("[profiles.demo]\nmodel = \"demo\"\napproval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\nunknown_host_field = \"copied\"\n"),nil}
func(src) VisionExportBytes()([]byte,error){return []byte(`{"version":1,"models":[{"api_key":"SYNTHETIC-NOT-A-KEY"}]}`),nil}
func(src) ModalityKBBytes()([]byte,error){return nil,nil}
func(src) DesiredPiManagedModels()w.PiManagedModelsSpec{return w.PiManagedModelsSpec{Providers:map[string]any{"providers":map[string]any{"amagi-demo":map[string]any{"apiKey":"SYNTHETIC"}}}}}
func must(e error){if e!=nil{panic(e)}}
func main(){
 base,e:=os.MkdirTemp("","supply-audit-");must(e);defer os.RemoveAll(base)
 ctx:=context.Background(); machine:="machine-A"; failChmod:=false
 mk:=func(label string)*w.Coordinator{
  p:=w.Probes{Identity:func(d string)platform.WSLUserIdentity{return platform.WSLUserIdentity{OK:true,UID:"1000",Home:"/home/test",MachineID:machine}},ToUNC:func(d,l string)string{return filepath.Join(base,label,d,filepath.FromSlash(l))},Chmod:func(string,string,...string)error{if failChmod{return errors.New("injected chmod failure")};return nil}}
  c:=w.NewCoordinator(filepath.Join(base,label,"state"),nil,p)
  c.RegisterProjection(w.NewPiManagedModelsAdapter(src{}));c.RegisterProjection(w.NewClaudeMCPAdapter(src{},nil));c.RegisterProjection(w.NewClaudeSettingsAdapter(src{},nil));c.RegisterProjection(w.NewCodexConfigAdapter(src{},nil));c.RegisterProjection(w.NewVisionExportAdapter(src{}));return c
 }
 run:=func(c *w.Coordinator,d string,cli w.CLI,u w.UnitID)w.RunResult{r,e:=c.RunUnits(ctx,d,cli,w.ReasonManual,[]w.UnitID{u});must(e);return r}
 // UI sends full preview keys, backend selection expects leaf names.
 c:=mk("selection");must(c.SetUnitSelection("A",w.CLIClaude,w.UnitClaudeMCP,[]string{"mcpServers/demo"}));must(c.GrantUnitConsent(ctx,"A",w.CLIClaude,w.UnitClaudeMCP));r:=run(c,"A",w.CLIClaude,w.UnitClaudeMCP);t,_:=c.ResolveTarget(ctx,"A",w.CLIClaude);_,e=os.Stat(filepath.Join(t.UNCRoot,".claude.json"));fmt.Printf("UI_SELECTION outcome=%s target_missing=%v\n",r.Results[0].Outcome,os.IsNotExist(e))
 // Stable identity changed after plan, same distro/uid/home.
 c=mk("generation");must(c.GrantUnitConsent(ctx,"A",w.CLIClaude,w.UnitClaudeSettings));t,_=c.ResolveTarget(ctx,"A",w.CLIClaude);sn,_:=c.Inspect(ctx,t);plan,e:=c.Plan(w.SupplyRequest{Target:t,SelectedUnits:[]w.UnitID{w.UnitClaudeSettings}},sn);must(e);machine="machine-B";r,e=c.Apply(ctx,plan);must(e);fmt.Printf("GENERATION_SWAP outcome=%s detail=%s\n",r.Results[0].Outcome,r.Results[0].Detail)
 // Unknown generation across different distro inherits secret-export authorization.
 machine="";c=mk("unknown");must(c.GrantUnitConsent(ctx,"A",w.CLICommon,w.UnitSharedVisionExport));r=run(c,"B",w.CLICommon,w.UnitSharedVisionExport);fmt.Printf("UNKNOWN_CROSS_DISTRO outcome=%s (B never consented)\n",r.Results[0].Outcome)
 // Switching between normal generations destroys earlier target ownership.
 machine="machine-A";c=mk("multitarget");must(c.GrantUnitConsent(ctx,"A",w.CLIClaude,w.UnitClaudeSettings));run(c,"A",w.CLIClaude,w.UnitClaudeSettings);machine="machine-B";must(c.GrantUnitConsent(ctx,"B",w.CLIClaude,w.UnitClaudeSettings));run(c,"B",w.CLIClaude,w.UnitClaudeSettings);machine="machine-A";r=run(c,"A",w.CLIClaude,w.UnitClaudeSettings);fmt.Printf("RETURN_TO_A outcome=%s detail=%s\n",r.Results[0].Outcome,r.Results[0].Detail)
 // Codex permission policy and unknown keys cross into target profile.
 c=mk("codex");must(c.SetUnitSelection("A",w.CLICodex,w.UnitCodexConfig,[]string{"demo"}));must(c.GrantUnitConsent(ctx,"A",w.CLICodex,w.UnitCodexConfig));r=run(c,"A",w.CLICodex,w.UnitCodexConfig);t,_=c.ResolveTarget(ctx,"A",w.CLICodex);b,e:=os.ReadFile(filepath.Join(t.UNCRoot,"config.toml"));must(e);fmt.Printf("CODEX_POLICY outcome=%s unsafe_policy_copied=%v unknown_copied=%v\n",r.Results[0].Outcome,strings.Contains(string(b),"danger-full-access"),strings.Contains(string(b),"unknown_host_field"))
 must(c.DropUnitRegistration("A",w.CLICodex,w.UnitCodexConfig));pv,e:=c.PreviewUnits(ctx,"A",w.CLICodex,[]w.UnitID{w.UnitCodexConfig});must(e);rr,e:=c.TakeOverUnit(ctx,"A",w.CLICodex,w.UnitCodexConfig,pv[0].TargetHash);must(e);fmt.Printf("CODEX_DETACH_TAKEOVER outcome=%s detail=%s\n",rr.Outcome,rr.Detail)
 // Normal aliases materialize excluded credentials inside the skill.
 host:=filepath.Join(base,"skills");must(os.MkdirAll(filepath.Join(host,"demo"),0700));must(os.WriteFile(filepath.Join(host,"demo","SKILL.md"),[]byte("# synthetic"),0600));must(os.WriteFile(filepath.Join(host,"demo",".env"),[]byte("SYNTHETIC_SECRET=not-real"),0600));must(os.Symlink(".env",filepath.Join(host,"demo","alias.txt")));scope:=w.NewSkillsScope(host,w.DefaultSkillsScanLimits());scope.Scan();m:=scope.Snapshot().Skills[0];alias:=false;for _,f:=range m.Files{alias=alias||f.RelPath=="alias.txt"};fmt.Printf("SECRET_ALIAS portable=%v alias_in_manifest=%v excluded=%v\n",m.Portable(),alias,m.Excluded)
 // Claude command and agent Markdown files disappear from inventory.
 root:=filepath.Join(base,"resources");must(os.MkdirAll(filepath.Join(root,"commands"),0700));must(os.MkdirAll(filepath.Join(root,"agents"),0700));must(os.WriteFile(filepath.Join(root,"commands","review.md"),[]byte("# review"),0600));must(os.WriteFile(filepath.Join(root,"agents","helper.md"),[]byte("# helper"),0600));rs:=w.NewResourceScope(w.CLIClaude,w.ResourceLayout{HostRoot:root,DirUnits:[]string{"commands","agents"},TargetPrefix:".claude/"},w.DefaultSkillsScanLimits());rs.Scan();fmt.Printf("CLAUDE_RESOURCES normal_markdown_units=%d\n",len(rs.Snapshot().Skills))
 // Sensitive commit failure removes pre-existing user-owned provider config.
 c=mk("chmod");t,_=c.ResolveTarget(ctx,"A",w.CLIPi);must(os.MkdirAll(t.UNCRoot,0700));must(os.WriteFile(filepath.Join(t.UNCRoot,"models.json"),[]byte(`{"providers":{"local-user":{"models":[]}},"userSetting":true}`),0600));run(c,"A",w.CLIPi,w.UnitPiManagedModels);old,err:=os.ReadFile(filepath.Join(t.UNCRoot,"models.json"));must(err);must(os.WriteFile(filepath.Join(t.UNCRoot,"models.json"),append(old,byte(32)),0600));failChmod=true;r=run(c,"A",w.CLIPi,w.UnitPiManagedModels);_,e=os.Stat(filepath.Join(t.UNCRoot,"models.json"));fmt.Printf("CHMOD_FAILURE outcome=%s preexisting_mixed_config_deleted=%v\n",r.Results[0].Outcome,os.IsNotExist(e));failChmod=false;st:=c.GetStatus("A",w.CLIPi);fmt.Printf("FAILURE_STATUS lastOutcome=%s lastError=%q\n",st.Units[0].LastOutcome,st.Units[0].LastErrorCode)
 // Already-canceled context does not stop a projection commit.
 c=mk("cancel");must(c.GrantUnitConsent(ctx,"A",w.CLIClaude,w.UnitClaudeSettings));cx,cancel:=context.WithCancel(ctx);cancel();r,e=c.RunUnits(cx,"A",w.CLIClaude,w.ReasonManual,[]w.UnitID{w.UnitClaudeSettings});must(e);fmt.Printf("CANCELED_PROJECTION outcome=%s\n",r.Results[0].Outcome)
}
