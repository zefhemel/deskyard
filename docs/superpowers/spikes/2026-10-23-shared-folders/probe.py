#!/usr/bin/env python3
"""Throwaway sharing investigation. Only creates and deletes its own resources."""
import json, os, pathlib, subprocess, tempfile, time, uuid

IMAGE = 'deskyard-sharing-probe:investigation'
PREFIX = 'dy-share-probe-' + uuid.uuid4().hex[:8]
RESULTS = {'run': PREFIX, 'environment': {}, 'backends': [], 'cleanup': []}
CONTAINERS = []
VOLUMES = []

def run(*args, check=True, timeout=120):
    p = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f'{args}: {p.stderr.strip()} {p.stdout.strip()}')
    return p

def record(rows, label, p, expected=None):
    rows.append({'check': label, 'exit': p.returncode, 'stdout': p.stdout.strip(), 'stderr': p.stderr.strip(),
                 'expected_exit': expected, 'matches': expected is None or p.returncode == expected})
    print(label, p.returncode, p.stdout.strip()[:100], p.stderr.strip()[:100], flush=True)

def container(suffix, source, uid=None, readonly=False):
    name = PREFIX + '-' + suffix
    args = ['run', '-d', '--name', name, '--network', 'none', '--label', 'deskyard.probe='+PREFIX,
            '--mount', source + ',dst=/shared' + (',readonly' if readonly else '')]
    if uid:
        args += ['--user', f'{uid}:{uid}', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges']
    CONTAINERS.append(name)
    run(*args, IMAGE)
    return name

def ex(name, *args, check=False):
    return run('exec', name, *args, check=check)

def shell(name, command, check=False):
    return ex(name, 'sh', '-c', command, check=check)

def persistent(reader, admin, body, mutation):
    p = subprocess.Popen(['docker', 'exec', '-i', reader, 'python3', '-u', '-c', body],
                         stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    ready = p.stdout.readline().strip()
    if ready != 'ready':
        p.kill()
        raise RuntimeError('persistent probe not ready: '+ready)
    shell(admin, mutation, check=True)
    out, err = p.communicate('\n', timeout=15)
    return subprocess.CompletedProcess([], p.returncode, out, err)

def probe(label, source):
    rows = []
    result = {'backend': label, 'checks': rows}
    RESULTS['backends'].append(result)
    admin = container(label+'-admin', source)
    writer = container(label+'-writer', source, 21001)
    reader = container(label+'-reader', source, 21002)
    outsider = container(label+'-outsider', source, 21003)
    record(rows, 'filesystem', shell(admin, 'stat -f -c %T /shared; stat -c %u:%g /shared'), 0)
    shell(admin, 'chmod 755 /shared; mkdir /shared/project; chown 0:0 /shared/project; chmod 700 /shared/project', check=True)
    record(rows, 'denied before grant', shell(reader, 'ls /shared/project'), 2)
    grant = shell(admin, 'setfacl -m u:21001:rwx,u:21002:rx /shared/project; setfacl -d -m u::rwx,u:21001:rwx,u:21002:rx,g::---,m::rwx,o::--- /shared/project')
    record(rows, 'grant and default ACL', grant, 0)
    if grant.returncode:
        record(rows, 'ACL readback after failed grant', shell(admin, 'getfacl -pn /shared/project'))
        # Also test ordinary Unix permissions independently of ACL availability.
        shell(admin, 'chown 21001:21001 /shared/project; chmod 700 /shared/project', check=True)
        record(rows, 'owner writes with mode 0700', shell(writer, 'echo owned > /shared/project/owned'), 0)
        record(rows, 'other UID denied by mode 0700', shell(reader, 'cat /shared/project/owned'), 1)
        return
    record(rows, 'writer creates ordinary file', shell(writer, 'echo original > /shared/project/ordinary'), 0)
    record(rows, 'reader reads inherited ACL file', shell(reader, 'cat /shared/project/ordinary'), 0)
    record(rows, 'reader cannot write', shell(reader, 'echo denied >> /shared/project/ordinary'), 2)
    record(rows, 'reader cannot create', shell(reader, 'touch /shared/project/nope'), 1)
    record(rows, 'outsider cannot read', shell(outsider, 'cat /shared/project/ordinary'), 1)
    record(rows, 'unprivileged client cannot switch identity', ex(reader, 'setpriv', '--reuid=21001', 'id'), 127)
    record(rows, 'client cannot change root-owned gate', shell(writer, 'chmod 777 /shared/project'), 1)
    shell(admin, 'setfacl -R -m u:21002:rwX /shared/project; setfacl -d -m u:21002:rwx /shared/project', check=True)
    record(rows, 'live grant write', shell(reader, 'echo by-reader > /shared/project/reader-owned; echo appended >> /shared/project/ordinary'), 0)
    shell(admin, 'setfacl -R -m u:21002:r-X /shared/project; setfacl -d -m u:21002:rx /shared/project', check=True)
    record(rows, 'downgraded reader cannot modify writer file', shell(reader, 'echo no >> /shared/project/ordinary'), 2)
    record(rows, 'downgraded reader STILL writes own file', shell(reader, 'echo owner-bypass >> /shared/project/reader-owned'), 0)
    record(rows, 'owner can override file sharing ACL', shell(writer, 'chmod 600 /shared/project/ordinary'), 0)
    record(rows, 'reader loses access after owner chmod', shell(reader, 'cat /shared/project/ordinary'), 1)
    shell(admin, 'setfacl -m u:21002:r /shared/project/ordinary', check=True)
    record(rows, 'application creates private-mode file', ex(writer, 'python3', '-c', 'import os; f=os.open("/shared/project/private",os.O_WRONLY|os.O_CREAT,0o600); os.write(f,b"secret"); os.close(f)'), 0)
    record(rows, 'default ACL cannot override mode 0600', shell(reader, 'cat /shared/project/private'), 1)
    shell(writer, 'mkdir /shared/project/sub; cp /shared/project/ordinary /shared/project/sub/ordinary', check=True)
    shell(admin, 'setfacl -m u:21002:r /shared/project/sub/ordinary', check=True)
    body = '''import os,sys
fd=os.open('/shared/project/sub', os.O_RDONLY|os.O_DIRECTORY)
f=os.open('/shared/project/ordinary', os.O_RDONLY)
print('ready',flush=True);sys.stdin.readline()
for label,fn in [('absolute',lambda:open('/shared/project/ordinary').read()),('dirfd',lambda:os.read(os.open('ordinary',os.O_RDONLY,dir_fd=fd),100).decode()),('openfd',lambda:os.read(f,100).decode())]:
 try: print(label+':allowed:'+fn().strip())
 except OSError as e: print(label+':denied:'+str(e.errno))
'''
    record(rows, 'revocation: absolute denied, retained directory/file FDs usable', persistent(reader, admin, body, 'setfacl -x u:21002 /shared/project'), 0)
    shell(admin, 'setfacl -m u:21002:rx /shared/project; setfacl -m u:21002:r /shared/project/ordinary', check=True)
    body = '''import os,sys
f=os.open('/shared/project/ordinary',os.O_RDONLY)
print('ready',flush=True);sys.stdin.readline()
try: open('/shared/project/ordinary').read(); print('new-open:allowed')
except OSError as e: print('new-open:denied:'+str(e.errno))
print('openfd:allowed:'+os.read(f,100).decode().strip())
'''
    record(rows, 'file ACL revocation cannot revoke open FD', persistent(reader, admin, body, 'setfacl -m u:21002:--- /shared/project/ordinary'), 0)
    shell(admin, 'setfacl -m u:21002:r /shared/project/ordinary', check=True)
    record(rows, 'symlink still obeys target access', shell(outsider, 'ln -s /shared/project/ordinary /tmp/link; cat /tmp/link'), 1)
    record(rows, 'ACL readback', shell(admin, 'getfacl -pn /shared/project /shared/project/ordinary /shared/project/private'), 0)
    run('restart', reader)
    record(rows, 'grant persists after client restart', shell(reader, 'cat /shared/project/ordinary'), 0)
    run('rm', '-f', reader)
    CONTAINERS.remove(reader)
    reader = container(label+'-reader-recreated', source, 21002)
    record(rows, 'grant persists after client recreation', shell(reader, 'cat /shared/project/ordinary'), 0)
    ro = container(label+'-readonly', source, 21001, readonly=True)
    record(rows, 'readonly mount denies owner write', shell(ro, 'echo no >> /shared/project/ordinary'), 2)
    record(rows, 'readonly mount denies owner chmod', shell(ro, 'chmod 777 /shared/project/ordinary'), 1)
    readonly_id = run('inspect', '-f', '{{.Id}}', ro).stdout.strip()
    before = run('inspect', '-f', '{{.Id}}', writer).stdout.strip()
    shell(admin, 'mkdir /shared/new-share; chmod 700 /shared/new-share; setfacl -m u:21001:rwx /shared/new-share', check=True)
    record(rows, 'new share appears without recreation', shell(writer, 'echo live > /shared/new-share/new'), 0)
    assert before == run('inspect', '-f', '{{.Id}}', writer).stdout.strip()
    assert readonly_id == run('inspect', '-f', '{{.Id}}', ro).stdout.strip()
    run('rm', '-f', writer)
    CONTAINERS.remove(writer)
    record(rows, 'data survives writer removal', shell(reader, 'cat /shared/project/ordinary'), 0)
    # Root cleanup in disposable data only, for host temp cleanup on Linux too.
    shell(admin, 'rm -rf /shared/project /shared/new-share', check=True)

try:
    RESULTS['environment'] = json.loads(run('info', '--format', '{{json .}}').stdout)
    # Keep only non-sensitive engine/platform facts.
    RESULTS['environment'] = {k: RESULTS['environment'][k] for k in ['OperatingSystem','KernelVersion','Architecture','ServerVersion','Driver']}
    desktop = run('--context', 'desktop-linux', 'info', '--format', '{{.OperatingSystem}}', check=False, timeout=15)
    RESULTS['docker_desktop'] = {'available': desktop.returncode == 0, 'error': desktop.stderr.strip()}
    with tempfile.TemporaryDirectory(prefix=PREFIX+'-', dir=str(pathlib.Path(tempfile.gettempdir()).resolve())) as tmp:
        probe('mac-bind', 'type=bind,src='+tmp)
    vol = PREFIX+'-linux-volume'
    VOLUMES.append(vol)
    run('volume','create','--label','deskyard.probe='+PREFIX,vol)
    probe('linux-volume','type=volume,src='+vol)
    vol2 = PREFIX+'-linux-bind'
    VOLUMES.append(vol2)
    run('volume','create','--label','deskyard.probe='+PREFIX,vol2)
    path = run('volume','inspect','-f','{{.Mountpoint}}',vol2).stdout.strip()
    probe('linux-bind','type=bind,src='+path)
finally:
    for name in reversed(CONTAINERS):
        p=run('rm','-f',name,check=False)
        RESULTS['cleanup'].append({'container':name,'exit':p.returncode})
    for name in VOLUMES:
        p=run('volume','rm',name,check=False)
        RESULTS['cleanup'].append({'volume':name,'exit':p.returncode})
    (pathlib.Path(__file__).resolve().parent/'results.json').write_text(json.dumps(RESULTS,indent=2)+'\n')
    print('RESULTS '+str(pathlib.Path(__file__).resolve().parent/'results.json'), flush=True)
