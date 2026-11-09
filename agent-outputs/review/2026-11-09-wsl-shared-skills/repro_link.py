# Forensic script: executes exact Go raw-string script on isolated local POSIX homes; not a project test.
from pathlib import Path
import os, re, shlex, subprocess, tempfile, time
BASE=Path(__file__).resolve().parents[3]
src=(BASE/'internal/platform/wsl_shared_skills_link.go').read_text()
body=src.split('return strings.Join([]string{',1)[1].split('}, "\\n")',1)[0]
lines=[]
for line in body.splitlines():
    m=re.search(r'`(.*?)`', line)
    if m: lines.append(m.group(1))
def script(target):
    return "\n".join('T='+shlex.quote(str(target)) if x=='T=' else x for x in lines)
root=Path(tempfile.mkdtemp(prefix='link-evidence-',dir=Path(__file__).parent))
def setup(name):
    home=root/name; (home/'.agents').mkdir(parents=True)
    target=root/(name+'-host')/'skills'; target.mkdir(parents=True)
    return home,target
# Injection-safe target + dangling-link replacement + idempotence.
h,t=setup('normal'); t2=t/"中文 it's $(false)"; t2.mkdir()
(h/'.agents/skills').symlink_to(root/'missing')
env=dict(os.environ,HOME=str(h))
for i in range(2):
    r=subprocess.run(['sh','-c',script(t2)],env=env,text=True,capture_output=True)
    print('dangling/idempotence',i,'exit',r.returncode,repr(r.stdout),repr(r.stderr))
# Collision: a real file is explicitly supported; fixed timestamp matches a previous backup.
h,t=setup('collision'); bin=root/'bin'; bin.mkdir()
(bin/'date').write_text('#!/bin/sh\nprintf 20261109010101\n'); (bin/'date').chmod(0o755)
old=h/'.agents/skills.pre-shared.20261109010101'; old.write_text('PREVIOUS USER BACKUP')
(h/'.agents/skills').write_text('NEW LOCAL CONTENT')
r=subprocess.run(['sh','-c',script(t)],env=dict(os.environ,HOME=str(h),PATH=str(bin)+':'+os.environ['PATH']),text=True,capture_output=True)
print('collision exit',r.returncode,repr(r.stdout),'backup bytes=',repr(old.read_text()))
# Two real invocations paused at A ln to deterministically exercise the unlocked window.
h,t=setup('concurrent'); (h/'.agents/skills').mkdir(); (h/'.agents/skills/local').write_text('KEEP')
ready=root/'ready'; release=root/'release'
(bin/'ln').write_text('#!/bin/sh\nif [ "$PAUSE" = yes ]; then touch "$READY"; while [ ! -e "$RELEASE" ]; do sleep 0.01; done; fi\nexec /bin/ln "$@"\n'); (bin/'ln').chmod(0o755)
e=dict(os.environ,HOME=str(h),PATH=str(bin)+':'+os.environ['PATH'],READY=str(ready),RELEASE=str(release))
a=subprocess.Popen(['sh','-c',script(t)],env=dict(e,PAUSE='yes'),text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
for _ in range(500):
    if ready.exists(): break
    time.sleep(.01)
else: raise RuntimeError('pause not reached')
b=subprocess.run(['sh','-c',script(t)],env=e,text=True,capture_output=True)
release.touch(); out,err=a.communicate(timeout=10)
print('concurrent A exit',a.returncode,repr(out),repr(err),'B exit',b.returncode,repr(b.stdout),repr(b.stderr))
print('UNEXPECTED HOST WRITE', (t/'skills').is_symlink(), 'host skills/skills target=', os.readlink(t/'skills') if (t/'skills').is_symlink() else '')
print('evidence root',root)
