import json,pathlib,subprocess,tempfile,time,uuid
REPO=next((p for p in pathlib.Path(__file__).resolve().parents if (p/'images/Dockerfile').is_file()),pathlib.Path.cwd())
P='dy-uid-probe-'+uuid.uuid4().hex[:8]
results=[];containers=[]
def d(*args,check=True,timeout=120):
    p=subprocess.run(['docker',*args],text=True,capture_output=True,timeout=timeout)
    if check and p.returncode:raise RuntimeError(str(args)+': '+p.stderr+p.stdout)
    return p

def record(label,p):
    r={'check':label,'exit':p.returncode,'stdout':p.stdout.strip(),'stderr':p.stderr.strip()};results.append(r);print(json.dumps(r),flush=True)

def ex(n,s,user='user'):return d('exec','-u',user,n,'bash','-lc',s,check=False)
try:
    with tempfile.TemporaryDirectory(prefix=P,dir=str(pathlib.Path(tempfile.gettempdir()).resolve())) as tmp:
        # Simulate an existing workspace belonging to the current image UID.
        d('run','--rm','--network','none','--mount','type=bind,src='+tmp+',dst=/workspace','deskyard-sharing-probe:investigation','sh','-c','echo existing > /workspace/kept; chown -R 1000:1000 /workspace')
        n=P+'-xfce';containers.append(n)
        d('run','-d','--name',n,'--shm-size','1g','--network','none',
          '--mount','type=bind,src='+tmp+',dst=/workspace',
          '--security-opt','seccomp='+str(REPO/'supervisor/internal/engine/docker/flatpak-seccomp.json'),
          '--security-opt','apparmor=unconfined','--security-opt','systempaths=unconfined',
          '-e','WORKSPACE_PROXY_TOKEN=probe-only','-e','DESKYARD_PROBE_UID=21001','deskyard-sharing-desktop-probe:xfce')
        deadline=time.monotonic()+90
        while time.monotonic()<deadline:
            p=ex(n,'desktop-ready')
            if p.returncode==0:break
            time.sleep(1)
        record('desktop-ready with UID 21001',p)
        record('identity and migrated home ownership',ex(n,'id; stat -c "%u:%g %a %n" /workspace /workspace/kept "$XDG_RUNTIME_DIR"; cat /workspace/kept'))
        record('desktop service UIDs',ex(n,'ps -eo uid,gid,comm | head -35'))
        record('home writes and runtime tools',ex(n,'echo new > ~/new; mise --version; flatpak --user list; stat -c %u:%g ~/new'))
        record('Chromium CDP responds',ex(n,'curl --max-time 70 -fsS http://127.0.0.1:9221/json/version'))
        d('restart','-t','15',n)
        deadline=time.monotonic()+90
        while time.monotonic()<deadline:
            p=ex(n,'desktop-ready')
            if p.returncode==0:break
            time.sleep(1)
        record('desktop-ready after restart',p)
        record('UID and files retained after restart',ex(n,'id -u; cat ~/kept ~/new'))
        record('desktop logs',d('logs','--tail','35',n,check=False))
        d('rm','-f',n);containers.remove(n)
finally:
    for name in containers:d('rm','-f',name,check=False)
    (pathlib.Path(__file__).resolve().parent/'desktop-results.json').write_text(json.dumps(results,indent=2)+'\n')
