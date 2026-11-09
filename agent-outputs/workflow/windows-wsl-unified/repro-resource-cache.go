// Forensic reproducer, not a project test. All writes confined to os.MkdirTemp.
// （P3-S3 共享技能作用域已随 1.3.130「WSL 内共享 Windows 技能根」退役——本
// 复现器改由 claude 资源目录单元承载同一资源缓存/增量对账语义。）
package main

import (
 "context"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"
 "amagi-codebox/internal/platform"
 "amagi-codebox/internal/wslsupply"
)
func must(err error) { if err != nil { panic(err) } }
func main() {
 root,err:=os.MkdirTemp("", "codebox-review-resource-"); must(err); defer os.RemoveAll(root)
 source:=filepath.Join(root,"source")
 put:=func(name,body string) { p:=filepath.Join(source,"skills",name,"SKILL.md"); must(os.MkdirAll(filepath.Dir(p),0755)); must(os.WriteFile(p,[]byte(body),0644)) }
 put("alpha","# alpha v1")
 unc:=func(_ string,p string)string{return filepath.Join(root,"target",strings.TrimPrefix(p,"/"))}
 c:=wslsupply.NewCoordinator(filepath.Join(root,"state"),nil,wslsupply.Probes{
 Identity:func(context.Context,string)platform.WSLUserIdentity{return platform.WSLUserIdentity{OK:true,UID:"1000",Home:"/home/u",MachineID:"review"}},
 ToUNC:unc,
 Chmod:func(context.Context,string,string,...string)error{return nil},
 StatMode:func(context.Context,string,string)(string,bool){return "644",true},
 })
 c.RegisterResources(wslsupply.NewResourceScope(wslsupply.CLIClaude,wslsupply.ResourceLayout{
 HostRoot:source,DirUnits:[]string{"skills"},TargetPrefix:".claude/",
 },wslsupply.DefaultSkillsScanLimits()))
 ctx:=context.Background(); must(c.SetTargetUnified(ctx,"Review",true))
 wait:=func(){for i:=0;i<1000;i++ {if s:=c.CliResourcesSnapshot(wslsupply.CLIClaude);s.Started&&!s.Running{return};time.Sleep(time.Millisecond)};panic("scan timeout")}
 reconcile:=func(){var units []wslsupply.UnitID;for _,m:=range c.CliResourcesSnapshot(wslsupply.CLIClaude).Skills{if m.NotPortableReason()==""{units=append(units,wslsupply.ResourceUnitID(wslsupply.CLIClaude,m.Name))}}; _,e:=c.AutoConsentIfUnified(ctx,"Review",wslsupply.CLIClaude,units);must(e);r,e:=c.RunUnits(ctx,"Review",wslsupply.CLIClaude,wslsupply.ReasonSave,units);must(e);for _,u:=range r.Results{fmt.Printf("%s: %s %s\n",u.Unit,u.Outcome,u.Detail)}}
 fmt.Println("initial StartCliResourcesScan:",c.StartCliResourcesScan(wslsupply.CLIClaude));wait();reconcile()
 targetAlpha:=unc("Review","/home/u/.claude/skills/alpha/SKILL.md")
 b,e:=os.ReadFile(targetAlpha);must(e);fmt.Println("initial target:",string(b))
 put("alpha","# alpha v2");put("beta","# beta")
 fmt.Println("after source edits StartCliResourcesScan:",c.StartCliResourcesScan(wslsupply.CLIClaude));reconcile()
 b,e=os.ReadFile(targetAlpha);must(e);_,betaErr:=os.Stat(unc("Review","/home/u/.claude/skills/beta/SKILL.md"))
 fmt.Printf("after automatic reconcile: alpha=%q beta_exists=%v snapshot_count=%d\n",b,betaErr==nil,len(c.CliResourcesSnapshot(wslsupply.CLIClaude).Skills))
 fmt.Println("explicit RescanCliResources:",c.RescanCliResources(wslsupply.CLIClaude));wait();reconcile()
 b,e=os.ReadFile(targetAlpha);must(e);_,betaErr=os.Stat(unc("Review","/home/u/.claude/skills/beta/SKILL.md"))
 fmt.Printf("after manual rescan: alpha=%q beta_exists=%v\n",b,betaErr==nil)
}
