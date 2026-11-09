from pathlib import Path
import os,re,shlex,subprocess,tempfile,time,json
REPORT=Path(__file__).resolve().parent
BASE=REPORT.parents[3]
src=(BASE/'internal/platform/wsl_shared_skills_link.go').read_text()
body=src.split('return strings.Join([]string{',1)[1].split('}, "\\n")',1)[0]
lines=[re.search(r'`(.*?)`',l).group(1) for l in body.splitlines() if re.search(r'`(.*?)`',l)]
def script(target):return '\n'.join('T='+shlex.quote(str(target)) if x=='T=' else x for x in lines)
root=Path(tempfile.mkdtemp(prefix='link-evidence-',dir=REPORT))
bin=root/'bin';bin.mkdir()
# Native mv -n is used for the collision. Darwin does not implement GNU mv -T.
# Adapter lane ONLY emulates same-filesystem GNU mv -T with os.rename, not Linux/WSL integration.
(bin/'mv').write_text('#!/usr/bin/env python3\n'+r"""import os,sys,subprocess
from pathlib import Path
a=sys.argv[1:]
if a[0]=='-n' and os.environ.get('INJECT_SLOT')=='yes':
    kind=os.environ.get('INJECT_KIND','file')
    p=Path(a[-1])
    if kind=='dir':p.mkdir()
    elif kind=='dir-link':p.symlink_to(os.environ['COMPETITOR_DIR'],target_is_directory=True)
    elif kind=='broken-link':p.symlink_to(os.environ['COMPETITOR_DIR']+'/missing')
    else:p.write_text('COMPETING BACKUP')
if a[0]=='-T' and os.environ.get('RENAME_ADAPTER')=='yes':
    try:os.rename(a[1],a[2]);sys.exit(0)
    except OSError as e:print(str(e),file=sys.stderr);sys.exit(1)
r=subprocess.run(['/bin/mv']+a);sys.exit(r.returncode)
""")
(bin/'mv').chmod(0o755)
(bin/'date').write_text('#!/bin/sh\nprintf 20261109010101\n');(bin/'date').chmod(0o755)
(bin/'ln').write_text('#!/bin/sh\nif [ "$PAUSE" = yes ]; then touch "$READY"; while [ ! -e "$RELEASE" ]; do sleep 0.01; done; fi\nexec /bin/ln "$@"\n');(bin/'ln').chmod(0o755)
def setup(name):
 h=root/name;(h/'.agents').mkdir(parents=True)
 t=root/(name+'-host')/'skills';t.mkdir(parents=True)
 e=dict(os.environ,HOME=str(h),PATH=str(bin)+':'+os.environ['PATH'],PYTHONDONTWRITEBYTECODE='1')
 return h,t,e
def run(t,e):return subprocess.run(['sh','-c',script(t)],env=e,text=True,capture_output=True)
def show(name,r):print(name,'exit',r.returncode,'stdout',repr(r.stdout),'stderr',repr(r.stderr))
# Literal script on macOS: no -T support, so report FAIL-PUBLISH rather than fake a native happy path.
h,t,e=setup('native');show('native literal source',run(t,e))
# R1 collision counterexample: the original backup and numbered slot are both preserved.
h,t,e=setup('sequential-collision');e['RENAME_ADAPTER']='yes'
old=h/'.agents/skills.pre-shared.20261109010101';old.write_text('R1 BACKUP')
old0=h/'.agents/skills.pre-shared.20261109010101.0';old0.write_text('NUMBERED BACKUP')
(h/'.agents/skills').write_text('NEW LOCAL CONTENT')
r=run(t,e);show('same-second pre-existing slots (rename adapter)',r)
print('preserved old=',old.read_text(),'old0=',old0.read_text(),'new slot=',(h/'.agents/skills.pre-shared.20261109010101.1').read_text())
# New slot race: a competitor fills the slot between [ ! -e C ] and mv -n.
for adapter in [False,True]:
 h,t,e=setup('slot-race-'+str(adapter));e['INJECT_SLOT']='yes'
 if adapter:e['RENAME_ADAPTER']='yes'
 L=h/'.agents/skills';L.write_text('CURRENT USER FILE MUST SURVIVE')
 r=run(t,e);show('slot race adapter='+str(adapter),r)
 B=h/'.agents/skills.pre-shared.20261109010101.0'
 print('backup bytes=',repr(B.read_text()),'current-is-link=',L.is_symlink(),'current-file-survived=',L.is_file() and L.read_text()=='CURRENT USER FILE MUST SURVIVE')
# R1 concurrent publish window; exact source, only -T adapter due Darwin host.
h,t,e=setup('concurrent');e['RENAME_ADAPTER']='yes';(h/'.agents/skills').mkdir();(h/'.agents/skills/local').write_text('KEEP')
ready=root/'ready';release=root/'release';e.update(READY=str(ready),RELEASE=str(release))
a=subprocess.Popen(['sh','-c',script(t)],env=dict(e,PAUSE='yes'),text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
for _ in range(500):
 if ready.exists():break
 time.sleep(.01)
else:raise RuntimeError('pause not reached')
b=run(t,e);release.touch();ao,ae=a.communicate(timeout=10)
print('concurrent A=',a.returncode,repr(ao),repr(ae));show('concurrent B',b)
print('HOST_CHILD_LINK_CREATED=',(t/'skills').is_symlink(),'FINAL_READLINK=',os.readlink(h/'.agents/skills'))
# New publication, already-linked, and dangling old-link replacement with hostile literal target.
h,t,e=setup('quoted');e['RENAME_ADAPTER']='yes';q=t/"中文 it's $(false)";q.mkdir();(h/'.agents/skills').symlink_to(root/'missing')
show('quoted dangling replacement',run(q,e));show('quoted idempotence',run(q,e))
# Target source dir migration and retained bytes.
h,t,e=setup('directory');e['RENAME_ADAPTER']='yes';(h/'.agents/skills').mkdir();(h/'.agents/skills/keep').write_text('USER DIR')
show('directory backup',run(t,e));print('directory preimage=',list((h/'.agents').glob('skills.pre-shared.*/keep'))[0].read_text())

# Directly related new predicate: a legitimate root already containing a skill named skills.
for name,child in [('nested-skill-dir','dir'),('nested-skill-file','file'),('nested-skill-dangling-link','dangling')]:
 h,t,e=setup(name);e['RENAME_ADAPTER']='yes';L=h/'.agents/skills';L.mkdir()
 (L/'SKILL.md').write_text('TOP ORIGINAL')
 if child=='dir':(L/'skills').mkdir();(L/'skills/SKILL.md').write_text('VALID SKILL NAMED skills')
 elif child=='file':(L/'skills').write_text('VALID FILE NAMED skills')
 else:(L/'skills').symlink_to(root/'nonexistent')
 r=run(t,e);show(name,r)
 print(name,'L_exists=',os.path.lexists(L),'backups=',[str(p) for p in (h/'.agents').glob('skills.pre-shared.*')])
# No-adapter reproduction: legitimate child named skills fails before publication.
h,t,e=setup('native-valid-skill');L=h/'.agents/skills';(L/'skills').mkdir(parents=True);(L/'skills/SKILL.md').write_text('VALID SKILL NAMED skills')
r=run(t,e);show('native valid skill named skills',r)
print('native-valid-L-absent=',not os.path.lexists(L),'native-valid-bytes=',list((h/'.agents').glob('skills.pre-shared.*/skills/SKILL.md'))[0].read_text())
# Requested B symlink scenario: competitor slot points at host skill root.
h,t,e=setup('native-slot-to-host');e.update(INJECT_SLOT='yes',INJECT_KIND='dir-link',COMPETITOR_DIR=str(t))
L=h/'.agents/skills';L.mkdir();(L/'SKILL.md').write_text('LOCAL TREE CONTENT')
r=run(t,e);show('native backup slot symlink to host root',r)
print('HOST_TREE_CREATED_BEFORE_FAILURE=',(t/'skills/SKILL.md').exists(),'host-bytes=',(t/'skills/SKILL.md').read_text() if (t/'skills/SKILL.md').exists() else '')
# Slot-race bypass review: directory, symlink to directory, dangling symlink.
for kind in ['dir','dir-link','broken-link']:
 h,t,e=setup('race-'+kind);e.update(RENAME_ADAPTER='yes',INJECT_SLOT='yes',INJECT_KIND=kind)
 other=root/('external-'+kind);other.mkdir();e['COMPETITOR_DIR']=str(other)
 L=h/'.agents/skills';L.mkdir();(L/'SKILL.md').write_text('KEEP RACE BYTES')
 r=run(t,e);show('slot '+kind,r)
 paths=[p for p in h.rglob('SKILL.md')]+[p for p in other.rglob('SKILL.md')]
 print('preserved=',[(str(p),p.read_text()) for p in paths if p.is_file()])
print('evidence root',root)

