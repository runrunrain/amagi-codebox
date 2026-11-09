# Forensic runner, not project tests. Exact generated script; controlled mv adapter lanes are explicitly labelled.
from pathlib import Path
import os,re,subprocess,shlex,tempfile,sys
REPORT=Path(__file__).resolve().parent;BASE=REPORT.parents[3]
src=(BASE/'internal/platform/wsl_shared_skills_link.go').read_text()
body=src.split('return strings.Join([]string{',1)[1].split('}, "\\n")',1)[0]
lines=[re.search(r'`(.*?)`',x).group(1) for x in body.splitlines() if re.search(r'`(.*?)`',x)]
def script(t):return '\n'.join('T='+shlex.quote(str(t)) if x=='T=' else x for x in lines)
root=Path(tempfile.mkdtemp(prefix='link-evidence-',dir=REPORT));bin=root/'bin';bin.mkdir()
# Adapter: lstat (lexists) no-clobber against already occupied literal destination; os.rename for vacant destination.
# This emulates controlled same-filesystem branches, NOT GNU atomicity under a second race after this check.
(bin/'mv').write_text('#!'+sys.executable+'\n'+r"""import os,sys
from pathlib import Path
a=sys.argv[1:];flag,s,d=a
if flag=='-nT':
    k=os.environ.get('COMPETITOR','')
    if k=='file':Path(d).write_text('COMPETITOR FILE')
    elif k=='dir':Path(d).mkdir()
    elif k=='dirlink':Path(d).symlink_to(os.environ['TARGET'],target_is_directory=True)
    elif k=='dangling':Path(d).symlink_to(os.environ['TARGET']+'/absent')
    if os.path.lexists(d):sys.exit(0)
elif flag!='-T':raise RuntimeError(a)
try:os.rename(s,d)
except OSError as e:print(e,file=sys.stderr);sys.exit(1)
""")
(bin/'mv').chmod(0o755)
def scenario(name,kind='dir',competitor='',adapter=True,nested=False):
 h=root/name;(h/'.agents').mkdir(parents=True);L=h/'.agents/skills';t=root/(name+'-host')/'skills';t.mkdir(parents=True)
 if kind=='dir':L.mkdir();(L/'SKILL.md').write_text('TOP ORIGINAL')
 elif kind=='file':L.write_text('ORIGINAL FILE')
 elif kind=='dangling':L.symlink_to(root/'missing')
 if nested:(L/'skills').mkdir();(L/'skills/SKILL.md').write_text('VALID SKILL NAMED skills')
 e=dict(os.environ,HOME=str(h),PYTHONDONTWRITEBYTECODE='1',TARGET=str(t),COMPETITOR=competitor)
 if adapter:e['PATH']=str(bin)+':'+os.environ['PATH']
 r=subprocess.run(['sh','-c',script(t)],env=e,text=True,capture_output=True)
 print(name,'adapter=',adapter,'exit=',r.returncode,'stdout=',repr(r.stdout),'stderr=',repr(r.stderr))
 backups=list((h/'.agents').glob('skills.pre-shared.*'))
 print('  L_lexists=',os.path.lexists(L),'L_link=',L.is_symlink(),'target_entries=',[x.name for x in t.iterdir()])
 if L.exists() and not L.is_symlink():print('  preserved_L_bytes=',(L/'SKILL.md').read_text() if L.is_dir() else L.read_text())
 for b in backups:
  print('  backup=',b.name,'is_link=',b.is_symlink())
  if b.is_symlink():print('  competitor_link_target=',os.readlink(b))
  elif b.is_dir():print('  backed_up_bytes=',(b/'SKILL.md').read_text() if (b/'SKILL.md').exists() else '', 'nested_bytes=',(b/'skills/SKILL.md').read_text() if (b/'skills/SKILL.md').exists() else '')
  else:print('  backup_file_bytes=',b.read_text())
 return h,t,r
scenario('native-valid-skill',adapter=False,nested=True)
scenario('r3-slot-to-host',competitor='dirlink')
scenario('r3-valid-skill',nested=True)
scenario('slot-file',competitor='file',kind='file')
scenario('slot-real-dir',competitor='dir')
scenario('slot-dangling-dest',competitor='dangling',kind='file')
scenario('normal-dir')
scenario('normal-file',kind='file')
scenario('dangling-source-link',kind='dangling')
# Demonstrate actual test adapter mismatch, without using our semantic adapter or -T stage.
testsrc=(BASE/'internal/platform/wsl_shared_skills_race_test.go').read_text()
testadapter=re.search(r'const mvGNU = `(.*?)`',testsrc,re.S).group(1)
h=root/'actual-test-adapter';h.mkdir();s=h/'source';s.write_text('USER FILE');b=h/'slot';b.symlink_to(h/'missing')
r=subprocess.run(['sh','-c',testadapter+'\n'+'mv -nT '+shlex.quote(str(s))+' '+shlex.quote(str(b))],text=True,capture_output=True,env=dict(os.environ,PYTHONDONTWRITEBYTECODE='1'))
print('ACTUAL_PROJECT_TEST_ADAPTER dangling-slot exit=',r.returncode,'src_exists=',s.exists(),'dest_is_link=',b.is_symlink(),'dest_bytes=',b.read_text() if b.is_file() else '')
print('evidence root',root)
