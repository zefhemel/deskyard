"""Execute only after READY: real emulator Chrome picker smoke, independent shell PID proofs."""
import argparse,json,time,uuid
from pathlib import Path
from playwright.sync_api import sync_playwright
parser=argparse.ArgumentParser();parser.add_argument('--state-dir',type=Path,required=True);parser.add_argument('--cdp-url',default='http://127.0.0.1:9227');args=parser.parse_args()
root=args.state_dir;a=json.loads((root/'access.json').read_text());nonce=uuid.uuid4().hex[:8]
host=root/'data'/a['name']/('android-final-'+nonce);host.mkdir();host.chmod(0o777);base='/workspace/'+host.name;fresh='android-'+nonce
def read(name):
 for _ in range(100):
  if (host/name).exists():return(host/name).read_text()
  time.sleep(.1)
 raise AssertionError('Missing proof '+name)
with sync_playwright() as pw:
 b=pw.chromium.connect_over_cdp(args.cdp_url);page=next(x for c in b.contexts for x in c.pages if a['name']+'/terminal/' in x.url);page.bring_to_front();page.goto(a['url']+'?session=android-qa')
 def command(text):page.get_by_text('Connected',exact=True).wait_for(state='attached');page.wait_for_timeout(250);page.locator('.xterm-helper-textarea').focus();page.keyboard.insert_text(text);page.keyboard.press('Enter')
 page.get_by_role('button',name='Show keyboard',exact=True).wait_for();command("printf '%s' \"$$\" > "+base+'/original')
 original=read('original');page.get_by_role('button',name='Sessions: android-qa',exact=True).click();page.get_by_label('New session name',exact=True).fill(fresh);page.get_by_label('Create session',exact=True).click();page.get_by_role('button',name='Sessions: '+fresh,exact=True).wait_for()
 command("printf '%s' \"$$\" > "+base+'/created');created=read('created');assert created!=original
 page.get_by_role('button',name='Sessions: '+fresh,exact=True).click();page.get_by_label('Join android-qa',exact=True).click();page.get_by_role('button',name='Sessions: android-qa',exact=True).wait_for();command("printf '%s' \"$$\" > "+base+'/joined');assert read('joined')==original
 command("clear; printf 'Deskyard shared terminal\\nAndroid session create and join verified\\n'")
 page.get_by_role('button',name='Show keyboard',exact=True).click();time.sleep(1)
 (root/'android-final-picker-proof.json').write_text(json.dumps({'nonce':nonce,'created_session':fresh,'original_pid':original,'created_pid':created,'joined_pid':read('joined'),'note':'Real emulator Chrome; picker and command entry via CDP. Earlier native Gboard/control proofs retained separately.'},indent=2)+'\n')
 print('Android final picker passed')
