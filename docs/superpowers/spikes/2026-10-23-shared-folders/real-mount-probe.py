import json,pathlib,subprocess,tempfile,time,uuid
REPO=next((p for p in pathlib.Path(__file__).resolve().parents if (p/'images/Dockerfile').is_file()),pathlib.Path.cwd())
P='dy-real-share-'+uuid.uuid4().hex[:8];rows=[];names=[]
root=pathlib.Path(__file__).resolve().parent
def d(*a,check=True):
 p=subprocess.run(['docker',*a],text=True,capture_output=True,timeout=90)
 if check and p.returncode:raise RuntimeError(str(a)+':'+p.stderr+p.stdout)
 return p

def record(label,p):
 r={'check':label,'exit':p.returncode,'stdout':p.stdout.strip(),'stderr':p.stderr.strip()};rows.append(r);print(json.dumps(r),flush=True)

def ex(n,s):return d('exec','-u','user',n,'bash','-c',s,check=False)
def attach(n,src,op):
 return d('run','--rm','--network','none','--pid','container:'+n,'--cap-add','SYS_ADMIN',
 '--security-opt','apparmor=unconfined','--mount','type=bind,src='+src+',dst=/source',
 '--mount','type=bind,src='+str(root/'mount.py')+',dst=/probe.py,readonly',
 'deskyard-sharing-probe:investigation','python3','/probe.py',op,check=False)
try:
 with tempfile.TemporaryDirectory(prefix=P,dir=str(pathlib.Path(tempfile.gettempdir()).resolve())) as tmp:
  src=pathlib.Path(tmp)/'shared';src.mkdir();(src/'file').write_text('shared\n');src.chmod(0o777);(src/'file').chmod(0o666)
  for env in ['xfce','mate']:
   n=P+'-'+env;names.append(n);home=pathlib.Path(tmp)/env;home.mkdir()
   d('run','-d','--name',n,'--network','none','--shm-size','1g',
    '--mount','type=bind,src='+str(home)+',dst=/workspace',
    '--security-opt','seccomp='+str(REPO/'supervisor/internal/engine/docker/flatpak-seccomp.json'),
    '--security-opt','apparmor=unconfined','--security-opt','systempaths=unconfined',
    '-e','WORKSPACE_PROXY_TOKEN=probe-only','deskyard-debian-'+env+':test')
   deadline=time.monotonic()+60
   while time.monotonic()<deadline:
    p=ex(n,'desktop-ready')
    if p.returncode==0:break
    time.sleep(1)
   record(env+': desktop-ready before attach',p)
   record(env+': live attach',attach(n,str(src),'rw' if env=='xfce' else 'ro'))
  writer,reader=names
  record('XFCE writes through live mount',ex(writer,'echo xfce >> /shared/project/file'))
  record('MATE reads XFCE update',ex(reader,'cat /shared/project/file'))
  record('MATE read-only denies write',ex(reader,'echo nope >> /shared/project/file'))
  record('MATE read-only denies chmod',ex(reader,'chmod 777 /shared/project/file'))
  record('MATE cannot bypass RO in user namespace',ex(reader,"unshare -Ur -m sh -c 'mount -o remount,rw /shared/project; echo bypass >> /shared/project/file'"))
  record('live detach MATE',attach(reader,str(src),'unmount'))
  record('MATE loses path after detach',ex(reader,'cat /shared/project/file'))
  record('XFCE retains data after MATE detach',ex(writer,'cat /shared/project/file'))
  record('XFCE desktop-ready after sharing operations',ex(writer,'desktop-ready'))
  record('MATE desktop-ready after sharing operations',ex(reader,'desktop-ready'))
finally:
 for n in names:d('rm','-f',n,check=False)
 (root/'real-mount-results.json').write_text(json.dumps(rows,indent=2)+'\n')
