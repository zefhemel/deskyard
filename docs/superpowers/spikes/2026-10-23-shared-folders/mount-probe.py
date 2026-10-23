import json,pathlib,subprocess,tempfile,uuid
P='dy-mount-probe-'+uuid.uuid4().hex[:8]
IMAGE='deskyard-sharing-probe:investigation'
root=pathlib.Path(__file__).resolve().parent
results=[];containers=[];volumes=[]
def d(*args,check=True):
    p=subprocess.run(['docker',*args],text=True,capture_output=True,timeout=60)
    if check and p.returncode: raise RuntimeError(str(args)+':'+p.stderr+p.stdout)
    return p

def record(label,p):
    r={'check':label,'exit':p.returncode,'stdout':p.stdout.strip(),'stderr':p.stderr.strip()};results.append(r);print(json.dumps(r),flush=True)

def mount(target,source,op):
    name=P+'-helper-'+uuid.uuid4().hex[:4]
    containers.append(name)
    return d('run','--rm','--name',name,'--network','none','--pid','container:'+target,
             '--cap-add','SYS_ADMIN','--security-opt','apparmor=unconfined',
             '--mount',source+',dst=/source', '--mount','type=bind,src='+str(root/'mount.py')+',dst=/probe.py,readonly',
             IMAGE,'python3','/probe.py',op,check=False)

def probe(label,source):
    writer=P+'-'+label+'-writer';reader=P+'-'+label+'-reader';outside=P+'-'+label+'-outside'
    for name in [writer,reader,outside]:
        containers.append(name)
        d('run','-d','--name',name,'--network','none','--cap-drop','ALL',IMAGE)
    def ex(c,s):return d('exec','-u','1000:1000',c,'sh','-c',s,check=False)
    # Initialize shared content as root in admin helper, no client sees the root.
    d('run','--rm','--network','none','--mount',source+',dst=/data',IMAGE,'sh','-c','chmod 777 /data; echo initial > /data/file; chmod 666 /data/file')
    record(label+': attach RW live',mount(writer,source,'rw'))
    record(label+': writer writes',ex(writer,'echo writer >> /shared/project/file'))
    record(label+': attach RO atomically live',mount(reader,source,'ro'))
    record(label+': reader reads',ex(reader,'cat /shared/project/file'))
    record(label+': reader cannot write despite same UID',ex(reader,'echo forbidden >> /shared/project/file'))
    record(label+': outsider sees no mount',ex(outside,'cat /shared/project/file'))
    record(label+': reader cannot remount',ex(reader,'mount -o remount,rw /shared/project'))
    # An open writer is retained while manager attempts read-only transition.
    script="""import os,sys
f=os.open('/shared/project/file',os.O_WRONLY|os.O_APPEND)
print('ready',flush=True);sys.stdin.readline()
try: os.write(f,b'after-downgrade\\n'); print('open-writer:allowed')
except OSError as e: print('open-writer:denied:'+str(e.errno))
"""
    proc=subprocess.Popen(['docker','exec','-i','-u','1000:1000',writer,'python3','-u','-c',script],text=True,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    assert proc.stdout.readline().strip()=='ready'
    record(label+': readonly transition with open writer',mount(writer,source,'readonly'))
    out,err=proc.communicate('\n',timeout=15)
    record(label+': retained writer after transition',subprocess.CompletedProcess([],proc.returncode,out,err))
    record(label+': readonly transition after writer closes',mount(writer,source,'readonly'))
    record(label+': new writes after transition',ex(writer,'echo no >> /shared/project/file'))
    readscript="""import os,sys
f=os.open('/shared/project/file',os.O_RDONLY)
print('ready',flush=True);sys.stdin.readline()
print('retained-read:'+os.read(f,100).decode().strip())
"""
    proc=subprocess.Popen(['docker','exec','-i','-u','1000:1000',reader,'python3','-u','-c',readscript],text=True,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    assert proc.stdout.readline().strip()=='ready'
    record(label+': normal detach with open reader',mount(reader,source,'detach'))
    record(label+': lazy detach with open reader',mount(reader,source,'unmount'))
    out,err=proc.communicate('\n',timeout=15)
    record(label+': retained reader after lazy detach',subprocess.CompletedProcess([],proc.returncode,out,err))
    record(label+': reattach reader',mount(reader,source,'ro'))
    record(label+': normal detach after reader closes',mount(reader,source,'detach'))
    record(label+': reader path after detach',ex(reader,'cat /shared/project/file'))
    record(label+': other desktop keeps data',ex(writer,'cat /shared/project/file'))
    d('restart',writer)
    record(label+': manual mounts lost on restart',ex(writer,'cat /shared/project/file'))
    record(label+': reapply intent after restart',mount(writer,source,'rw'))
    record(label+': data retained after restart',ex(writer,'cat /shared/project/file'))
    # Plain bind remount can otherwise expose source submounts; final design needs recursive restrictions.
    record(label+': mountinfo after helper removal',d('exec',writer,'findmnt','-T','/shared/project','-o','TARGET,FSTYPE,OPTIONS',check=False))
try:
    with tempfile.TemporaryDirectory(prefix=P,dir=str(pathlib.Path(tempfile.gettempdir()).resolve())) as tmp:
        probe('mac-bind','type=bind,src='+tmp)
    vol=P+'-volume';volumes.append(vol);d('volume','create',vol)
    probe('linux-volume','type=volume,src='+vol)
finally:
    for name in reversed(containers):d('rm','-f',name,check=False)
    for name in volumes:d('volume','rm',name,check=False)
    (root/'mount-results.json').write_text(json.dumps(results,indent=2)+'\n')
