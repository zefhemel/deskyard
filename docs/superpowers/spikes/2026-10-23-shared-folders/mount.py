"""Run only in a throwaway helper, sharing target PID namespace."""
import ctypes, os, sys
libc=ctypes.CDLL(None,use_errno=True)
class Attr(ctypes.Structure):
    _fields_=[('attr_set',ctypes.c_uint64),('attr_clr',ctypes.c_uint64),('propagation',ctypes.c_uint64),('userns_fd',ctypes.c_uint64)]
def check(n):
    if n < 0:
        e=ctypes.get_errno(); raise OSError(e,os.strerror(e))
    return n
op=sys.argv[1]
fd=None
if op in ['rw','ro']:
    fd=check(libc.open_tree(-100,b'/source',1|os.O_CLOEXEC))
    if op=='ro': check(libc.mount_setattr(fd,b'',0x1000,ctypes.byref(Attr(1|2|4,0,0,0)),ctypes.sizeof(Attr)))
check(libc.setns(os.open('/proc/1/ns/mnt',os.O_RDONLY),0x20000))
os.chroot('/proc/1/root');os.chdir('/')
os.makedirs('/shared',mode=0o755,exist_ok=True)
os.makedirs('/shared/project',mode=0o755,exist_ok=True)
if fd is not None:
    check(libc.move_mount(fd,b'',-100,b'/shared/project',4));os.close(fd)
elif op=='readonly':
    check(libc.mount_setattr(-100,b'/shared/project',0,ctypes.byref(Attr(1,0,0,0)),ctypes.sizeof(Attr)))
elif op=='detach': check(libc.umount2(b'/shared/project',0))
elif op=='unmount': check(libc.umount2(b'/shared/project',2))
print('ok:'+op)
