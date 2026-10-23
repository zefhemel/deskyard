"""Validate recorded investigation outcomes, including expected denials."""
import json
from pathlib import Path
root=Path(__file__).resolve().parent
acl=json.loads((root/'results.json').read_text())
assert [b['backend'] for b in acl['backends']]==['mac-bind','linux-volume','linux-bind']
mac={r['check']:r for r in acl['backends'][0]['checks']}
assert mac['grant and default ACL']['exit']!=0
assert 'Operation not supported' in mac['grant and default ACL']['stderr']
assert mac['other UID denied by mode 0700']['exit']==0
assert mac['other UID denied by mode 0700']['stdout']=='owned'
for backend in acl['backends'][1:]:
 assert all(r['matches'] for r in backend['checks']), backend['backend']
 rows={r['check']:r for r in backend['checks']}
 assert 'absolute:denied:13' in rows['revocation: absolute denied, retained directory/file FDs usable']['stdout']
 assert 'dirfd:allowed:' in rows['revocation: absolute denied, retained directory/file FDs usable']['stdout']
 assert 'new-open:denied:13' in rows['file ACL revocation cannot revoke open FD']['stdout']
 assert 'openfd:allowed:' in rows['file ACL revocation cannot revoke open FD']['stdout']
assert all(r['exit']==0 for r in acl['cleanup'])
mount=json.loads((root/'mount-results.json').read_text())
for backend in ['mac-bind','linux-volume']:
 rows={r['check'].split(': ',1)[1]:r for r in mount if r['check'].startswith(backend+': ')}
 for label in ['attach RW live','writer writes','attach RO atomically live','reader reads',
               'readonly transition after writer closes','lazy detach with open reader','reattach reader',
               'normal detach after reader closes','other desktop keeps data','reapply intent after restart',
               'data retained after restart','mountinfo after helper removal']:
  assert rows[label]['exit']==0,(backend,label,rows[label])
 for label in ['reader cannot write despite same UID','new writes after transition']:
  assert rows[label]['exit']!=0 and 'Read-only file system' in rows[label]['stderr']
 for label in ['readonly transition with open writer','normal detach with open reader']:
  assert rows[label]['exit']!=0 and 'Errno 16' in rows[label]['stderr']
 for label in ['outsider sees no mount','reader path after detach','manual mounts lost on restart']:
  assert rows[label]['exit']!=0 and 'No such file' in rows[label]['stderr']
 assert rows['reader cannot remount']['exit']!=0
 assert 'open-writer:allowed' in rows['retained writer after transition']['stdout']
 assert 'retained-read:initial' in rows['retained reader after lazy detach']['stdout']
real=json.loads((root/'real-mount-results.json').read_text())
for r in real:
 denied=any(s in r['check'] for s in ['denies','cannot bypass','loses path'])
 assert (r['exit']!=0)==denied,r
 if 'denies' in r['check'] or 'cannot bypass' in r['check']:
  assert 'Read-only file system' in r['stderr'],r
assert 'xfce' in next(r['stdout'] for r in real if r['check']=='MATE reads XFCE update')
uid=json.loads((root/'desktop-results.json').read_text())
assert all(r['exit']==0 for r in uid)
assert '21001:21001' in next(r['stdout'] for r in uid if r['check']=='identity and migrated home ownership')
assert '21001\nexisting\nnew'==next(r['stdout'] for r in uid if r['check']=='UID and files retained after restart')
print(f'Verified {sum(len(b["checks"]) for b in acl["backends"])} ACL observations, {len(mount)} mount observations, {len(real)} real desktop observations and {len(uid)} UID observations; expected failures confirmed.')
