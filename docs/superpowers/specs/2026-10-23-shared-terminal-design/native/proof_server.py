import json,os,socket,subprocess,uuid
from http.server import HTTPServer,BaseHTTPRequestHandler
from pathlib import Path
import argparse
parser=argparse.ArgumentParser(); parser.add_argument('--workspace',type=Path,required=True); parser.add_argument('--container',required=True); parser.add_argument('--session',default='ios-qa'); parser.add_argument('--output',type=Path,required=True); args=parser.parse_args()
workspace=args.workspace; nonce=uuid.uuid4().hex[:8]
proof=workspace/('native-'+nonce);proof.mkdir();proof.chmod(0o777)
reader='''import os,sys,termios,tty,select,time\nfrom pathlib import Path\np=Path(sys.argv[1]); label=sys.argv[2]; count=int(sys.argv[3]); old=termios.tcgetattr(0); data=b''\ntry:\n tty.setraw(0); (p/(label+'-ready')).write_text('ready'); deadline=time.time()+45\n while len(data)<count and time.time()<deadline:\n  if select.select([0],[],[],1)[0]: data+=os.read(0,count-len(data))\n (p/label).write_text(data.hex())\nfinally:\n termios.tcsetattr(0,termios.TCSADRAIN,old); (p/(label+'-done')).write_text('done')\n'''
(proof/'read.py').write_text(reader); (proof/'completion-unique').write_text('native-tab')
class H(BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_GET(self):
  key=self.path.removeprefix('/')
  if key=='bash':
   r=subprocess.run(['docker','exec','-u','user',args.container,'tmux','-S','/run/workspace/deskyard-terminal/tmux.sock','display-message','-p','-t','='+args.session+':','#{pane_current_command}'],capture_output=True,text=True);data=r.stdout.strip()
  elif '/' not in key and (proof/key).is_file(): data=(proof/key).read_text()
  else: self.send_response(404);self.end_headers();return
  self.send_response(200);self.end_headers();self.wfile.write(data.encode())
s=HTTPServer(('127.0.0.1',0),H)
c={'nonce':nonce,'proof_url':f'http://127.0.0.1:{s.server_port}','proof_path':'/workspace/'+proof.name,'host_path':str(proof),'pid':os.getpid()};args.output.write_text(json.dumps(c));s.serve_forever()
