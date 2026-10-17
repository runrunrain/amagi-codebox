//go:build ignore

// Review-only synthetic fixture. No WSL or real credentials.
package main
import("context";"fmt";"os";"path/filepath";"strings";"amagi-codebox/internal/platform";w "amagi-codebox/internal/wslsupply")
type source struct{}
func(source)VisionExportBytes()([]byte,error){return []byte(`{"models":[{"api_key":"SYNTHETIC-NOT-A-KEY"}]}`),nil}
func(source)ModalityKBBytes()([]byte,error){return nil,nil}
func must(e error){if e!=nil{panic(e)}}
func main(){root,e:=os.MkdirTemp("","review-mode-");must(e);defer os.RemoveAll(root);local:=func(p string)string{return filepath.Join(root,"target",filepath.FromSlash(strings.TrimPrefix(p,"/")))};statCalls:=0;p:=w.Probes{Identity:func(string)platform.WSLUserIdentity{return platform.WSLUserIdentity{UID:"1000",Home:"/home/u",MachineID:"fixture",OK:true}},ToUNC:func(_,p string)string{return local(p)},Chmod:func(_,m string,ps ...string)error{var mode os.FileMode;if m=="700"{mode=0700}else{mode=0600};for _,p:=range ps{if e:=os.Chmod(local(p),mode);e!=nil{return e}};return nil},StatMode:func(_,p string)(string,bool){statCalls++;i,e:=os.Stat(local(p));if e!=nil{return "",false};return fmt.Sprintf("%o",i.Mode().Perm()),true}};c:=w.NewCoordinator(filepath.Join(root,"state"),nil,p);c.RegisterProjection(w.NewVisionExportAdapter(source{}));ctx:=context.Background();must(c.GrantUnitConsent(ctx,"A",w.CLICommon,w.UnitSharedVisionExport));_,e=c.RunUnits(ctx,"A",w.CLICommon,w.ReasonLaunch,[]w.UnitID{w.UnitSharedVisionExport});must(e);f:=local("/home/u/.agents/"+w.VisionExportFile);must(os.Chmod(f,0644));statCalls=0;r,e:=c.RunUnits(ctx,"A",w.CLICommon,w.ReasonLaunch,[]w.UnitID{w.UnitSharedVisionExport});must(e);i,e:=os.Stat(f);must(e);fmt.Printf("UNCHANGED_SECRET outcome=%s mode=%o permission_probe_calls=%d\n",r.Results[0].Outcome,i.Mode().Perm(),statCalls)}
