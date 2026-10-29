"""Real image tests for the shared tmux runtime (no browser required)."""

def test_shared_terminal(sandbox):
    sandbox.exec("python3 - <<'PY'\n" + r'''
import fcntl, os, pty, select, signal, struct, subprocess, termios, time
socket = '/run/workspace/deskyard-terminal/tmux.sock'
initial_modes = {}
def tmux(*args):
    return subprocess.check_output(['tmux', '-S', socket, *args], text=True).strip()
def wait(f):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            if f(): return
        except (subprocess.CalledProcessError, FileNotFoundError): pass
        time.sleep(.05)
    raise AssertionError('terminal state did not become ready')
def attach(name, cols=80, rows=24, public=False):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ['TERM'] = 'xterm-256color'
        os.execvp('deskyard', ['deskyard', 'terminal' if public else 'terminal-helper', name])
    initial_modes[pid] = termios.tcgetattr(fd)
    assert initial_modes[pid][3] & (termios.ICANON | termios.ECHO) == (termios.ICANON | termios.ECHO)
    os.set_inheritable(fd, False)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    return pid, fd
def detach(client):
    pid,fd=client
    os.close(fd)
    os.waitpid(pid,0)
clients=[]
user_config='/workspace/.tmux.conf'
with open(user_config,'w') as f: f.write('set -g status on\n')
try:
    prefix=attach('main-prefix', public=True)
    wait(lambda: tmux('list-sessions','-F','#{session_name}') == 'main-prefix')
    detach(prefix)
    clients=[attach('main', public=i == 0) for i in range(6)]
    wait(lambda: tmux('list-clients','-F','#{client_pid}').count('\n') == 5)
    assert set(tmux('list-sessions','-F','#{session_name}').splitlines()) == {'main','main-prefix'}
    assert tmux('show-options','-g','-v','status') == 'off'
    assert open(user_config).read() == 'set -g status on\n'
    assert tmux('show-window-options','-g','-v','window-size') == 'latest'
    # A browser xterm sends SGR mouse-wheel reports, not arrow/history keys.
    tmux('send-keys','-t','=main:', "printf 'trackpad-row-%03d\\n' {1..300}", 'Enter')
    wait(lambda: int(tmux('display-message','-p','-t','=main:','#{history_size}')) > 200)
    os.write(clients[0][1], b'\x1b[<64;10;10M')
    wait(lambda: tmux('display-message','-p','-t','=main:','#{pane_in_mode}') == '1')
    assert int(tmux('display-message','-p','-t','=main:','#{scroll_position}')) > 0
    tmux('send-keys','-t','=main:','-X','cancel')
    wait(lambda: tmux('display-message','-p','-t','=main:','#{pane_in_mode}') == '0')
    assert oct(os.stat(os.path.dirname(socket)).st_mode & 0o777) == '0o700'
    assert os.stat(socket).st_mode & 0o077 == 0
    tmux('send-keys','-t','=main:','pwd > /workspace/terminal-cwd-proof','Enter')
    wait(lambda: os.path.exists('/workspace/terminal-cwd-proof') and open('/workspace/terminal-cwd-proof').read().strip() == '/workspace')
    assert open('/workspace/terminal-cwd-proof').read().strip() == '/workspace'
    shell=tmux('display-message','-p','-t','=main:','#{pane_pid}')
    for attempt in range(20):
        canceled_pid, canceled_fd = clients.pop(0) if attempt == 0 else attach('main', public=True)
        wait(lambda: not (termios.tcgetattr(canceled_fd)[3] & termios.ICANON))
        os.kill(canceled_pid, signal.SIGTERM)
        os.waitpid(canceled_pid,0)
        try:
            assert termios.tcgetattr(canceled_fd) == initial_modes[canceled_pid], "canceling terminal attachment left native PTY attributes changed"
        finally:
            os.close(canceled_fd)
    assert tmux('display-message','-p','-t','=main:','#{pane_pid}') == shell
    tmux('send-keys','-t','=main:','echo shared-terminal-proof','Enter')
    wait(lambda: 'shared-terminal-proof' in tmux('capture-pane','-p','-t','=main:'))
    tmux('send-keys','-t','=main:','sleep 2; echo survived > /workspace/terminal-job-proof','Enter')
    for client in clients: detach(client)
    clients=[]
    wait(lambda: os.path.exists('/workspace/terminal-job-proof'))
    assert open('/workspace/terminal-job-proof').read() == 'survived\n'
    assert tmux('display-message','-p','-t','=main:','#{pane_pid}') == shell
    clients=[attach('main',101,37)]
    wait(lambda: tmux('display-message','-p','-t','=main:','#{pane_width} #{pane_height}')=='101 37')
    clients.append(attach('main',117,41))
    wait(lambda: tmux('display-message','-p','-t','=main:','#{pane_width} #{pane_height}')=='117 41')
    fcntl.ioctl(clients[0][1], termios.TIOCSWINSZ, struct.pack('HHHH', 33, 99, 0, 0))
    os.write(clients[0][1], b'echo active-client\r')
    wait(lambda: tmux('display-message','-p','-t','=main:','#{pane_width} #{pane_height}')=='99 33')
    clients.append(attach('main-prefix'))
    wait(lambda: set(tmux('list-sessions','-F','#{session_name}').splitlines()) == {'main','main-prefix'})
    assert tmux('display-message','-p','-t','=main:','#{pane_pid}') == shell
    assert tmux('display-message','-p','-t','=main-prefix:','#{pane_pid}') != shell
finally:
    for client in clients: detach(client)
    subprocess.run(['tmux','-S',socket,'kill-server'], check=False)
PY''')


def test_terminal_cancellation_order_and_deadline(sandbox):
    sandbox.exec("python3 - <<'PY'\n" + r'''
import os, pty, signal, subprocess, termios, time
socket='/run/workspace/deskyard-terminal/tmux.sock'
def tmux(*args):
    return subprocess.check_output(['tmux','-S',socket,*args],text=True).strip()
def wait(f, timeout=10):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        try:
            result=f()
            if result: return result
        except (subprocess.CalledProcessError, FileNotFoundError): pass
        time.sleep(.01)
    raise AssertionError('terminal condition timed out')
for mode in ['graceful','deadline']:
    pid,fd=pty.fork()
    if pid==0:
        os.environ['TERM']='xterm-256color'
        os.execvp('deskyard',['deskyard','terminal','cancel-'+mode])
    os.set_inheritable(fd,False)
    original=termios.tcgetattr(fd)
    assert original[3] & termios.ICANON
    server=None
    reaped=False
    try:
        wait(lambda: not (termios.tcgetattr(fd)[3] & termios.ICANON))
        server=int(tmux('display-message','-p','#{pid}'))
        shell=tmux('display-message','-p','-t','=cancel-'+mode+':','#{pane_pid}')
        os.kill(server,signal.SIGSTOP)
        started=time.monotonic()
        os.kill(pid,signal.SIGTERM)
        if mode=='graceful':
            time.sleep(.15)
            result=os.waitpid(pid,os.WNOHANG)
            reaped=result[0]==pid
            assert not reaped, 'wrapper returned before stopped tmux server could restore PTY'
            os.kill(server,signal.SIGCONT)
        # Poll pid explicitly: (0, 0) is truthy and cannot be used with wait().
        deadline=started+4
        while not reaped and time.monotonic()<deadline:
            result=os.waitpid(pid,os.WNOHANG)
            reaped=result[0]==pid
            if not reaped: time.sleep(.01)
        assert reaped, 'cancellation exceeded its bounded deadline'
        elapsed=time.monotonic()-started
        assert os.waitstatus_to_exitcode(result[1])==130
        assert termios.tcgetattr(fd)==original, (mode,'PTY not restored',original,termios.tcgetattr(fd))
        if mode=='deadline':
            assert 1.5 < elapsed < 4, elapsed
            os.kill(server,signal.SIGCONT)
        else:
            assert elapsed < 2, elapsed
        wait(lambda: not tmux('list-clients','-F','#{client_pid}'))
        assert tmux('display-message','-p','-t','=cancel-'+mode+':','#{pane_pid}')==shell
        print(mode,'exit130 restored; shell preserved; clients removed',round(elapsed,3),flush=True)
    finally:
        if server:
            os.kill(server,signal.SIGCONT)
        if not reaped:
            os.kill(pid,signal.SIGKILL)
            os.waitpid(pid,0)
        os.close(fd)
subprocess.run(['tmux','-S',socket,'kill-server'],check=False)
PY''')


def test_terminal_utf8_defaults_and_profile_override(sandbox):
    sandbox.exec("python3 - <<'PY'\n" + r'''
import os, pty, select, subprocess, time
socket='/run/workspace/deskyard-terminal/tmux.sock'
def tmux(*args):
    return subprocess.check_output(['tmux','-S',socket,*args],text=True).strip()
def attach(name):
    pid,fd=pty.fork()
    if pid==0:
        for key in list(os.environ):
            if key=='LANG' or key.startswith('LC_'): os.environ.pop(key)
        os.environ['TERM']='xterm-256color'
        os.execvp('deskyard',['deskyard','terminal',name])
    os.set_inheritable(fd,False)
    return pid,fd
def wait(f):
    deadline=time.monotonic()+10
    while time.monotonic()<deadline:
        try:
            result=f()
            if result: return result
        except (subprocess.CalledProcessError,FileNotFoundError): pass
        time.sleep(.02)
    raise AssertionError('terminal UTF8 condition timed out')
clients=[]
try:
    clients.append(attach('utf8'))
    wait(lambda: 'utf8' in tmux('list-sessions','-F','#{session_name}').splitlines())
    # ASCII shell input produces a UTF8 checkmark, independent of IME/input encoding.
    tmux('send-keys','-t','=utf8:',r"locale charmap > /workspace/utf8-charmap; printf '\342\234\223' | wc -m > /workspace/utf8-count; printf '\342\234\223\n'",'Enter')
    wait(lambda: os.path.exists('/workspace/utf8-count') and open('/workspace/utf8-count').read().strip())
    charmap=open('/workspace/utf8-charmap').read().strip()
    count=open('/workspace/utf8-count').read().strip()
    assert charmap=='UTF-8' and count=='1', ('shell locale/character count',charmap,count)
    output=b''
    deadline=time.monotonic()+5
    while time.monotonic()<deadline and b'\xe2\x9c\x93' not in output:
        if select.select([clients[0][1]],[],[],.1)[0]: output+=os.read(clients[0][1],65536)
    assert b'\xe2\x9c\x93' in output, ('UTF8 absent from real PTY output',output)
    # User login configuration is evaluated after the image-owned default.
    with open('/workspace/.bash_profile','w') as f: f.write('export LANG=C\n')
    clients.append(attach('user-locale'))
    wait(lambda: 'user-locale' in tmux('list-sessions','-F','#{session_name}').splitlines())
    tmux('send-keys','-t','=user-locale:','locale charmap > /workspace/user-charmap','Enter')
    wait(lambda: os.path.exists('/workspace/user-charmap') and open('/workspace/user-charmap').read().strip())
    assert open('/workspace/user-charmap').read().strip()=='ANSI_X3.4-1968'
finally:
    for pid,fd in clients:
        os.close(fd)
        os.waitpid(pid,0)
    subprocess.run(['tmux','-S',socket,'kill-server'],check=False)
PY''')


def test_native_terminal_defaults(sandbox):
    sandbox.exec("python3 - <<'PY'\n" + r'''
import os, pty, subprocess, time
socket='/run/workspace/deskyard-terminal/tmux.sock'
def sessions():
    return {l.split('\t')[0] for l in subprocess.check_output(['deskyard','terminal-list'],text=True).splitlines()}
def wait(f):
    end=time.monotonic()+15
    while time.monotonic()<end:
        if f():return
        time.sleep(.1)
    raise AssertionError('native defaults did not become ready')
assert not sessions()
os.makedirs('/workspace/native-cwd',exist_ok=True)
clients=[]
try:
    for i in range(4):
        pid,fd=pty.fork()
        if pid==0:
            os.chdir('/workspace/native-cwd')
            os.environ['TERM']='xterm-256color'
            os.execvp('deskyard',['deskyard','terminal-new'])
        os.set_inheritable(fd, False)
        clients.append((pid,fd))
    wait(lambda:len(sessions())==4)
    names=sessions()
    assert names == {'terminal-1', 'terminal-2', 'terminal-3', 'terminal-4'}, names
    for name in names:
        cwd=subprocess.check_output(['tmux','-S',socket,'display-message','-p','-t','='+name+':','#{pane_current_path}'],text=True).strip()
        assert cwd=='/workspace/native-cwd',cwd
    for pid,fd in clients:os.close(fd);os.waitpid(pid,0)
    clients=[]
    assert sessions()==names, 'detached sessions disappeared'
    subprocess.run(['tmux','-S',socket,'kill-server'],check=True)
    wait(lambda:not sessions())
finally:
    for pid,fd in clients:os.close(fd);os.waitpid(pid,0)
    subprocess.run(['tmux','-S',socket,'kill-server'],check=False)
PY''')


def test_emulator_shared_defaults_tabs_cwd_and_explicit_commands(sandbox):
    """Exercise installed defaults, Debian alternatives, new tabs and explicit commands."""
    from sandbox import wait_until
    import shlex
    terminal = 'xfce4-terminal'
    cwd_flag = '--working-directory'
    socket = '/run/workspace/deskyard-terminal/tmux.sock'
    def names():
        return {l.split('\t')[0] for l in sandbox.exec('deskyard terminal-list').stdout.splitlines()}
    def launch(cmd):
        sandbox.exec('setsid -f ' + cmd + ' >/dev/null 2>&1')
    # Simulate existing preferences before any emulator starts. Migration must
    # replace old unshared commands while retaining unrelated appearance.
    sandbox.exec("printf '[Configuration]\\nRunCustomCommand=FALSE\\nFontName=Monospace 17\\nColorBackground=#123456\\n' > ~/.config/xfce4/terminal/terminalrc; printf 'TerminalEmulator=nonexistent-old-choice\\n' > ~/.config/xfce4/helpers.rc")
    sandbox.exec('deskyard-terminal-configure')
    profile = sandbox.exec('cat ~/.config/xfce4/terminal/terminalrc').stdout
    assert 'FontName=Monospace 17' in profile and 'ColorBackground=#123456' in profile
    sandbox.exec('mkdir -p /workspace/terminal-project')
    assert names() == set()
    launch(f'{terminal} {cwd_flag}=/workspace/terminal-project')
    assert wait_until(lambda: len(names()) == 1, 15), names()
    first = next(iter(names()))
    assert sandbox.exec(f"tmux -S {socket} display-message -p -t ={first}: '#{{pane_current_path}}'").stdout.strip() == '/workspace/terminal-project'
    window = sandbox.exec(f'xdotool search --onlyvisible --class {terminal} | tail -1').stdout.strip()
    assert wait_until(lambda: first in sandbox.exec(f'xdotool getwindowname {window}').stdout, 10), sandbox.exec(f'xdotool getwindowname {window}').stdout
    sandbox.exec(f'xdotool windowactivate --sync {window} key ctrl+shift+t')
    assert wait_until(lambda: len(names()) == 2, 15), names()
    launch('x-terminal-emulator')
    assert wait_until(lambda: len(names()) == 3, 15), names()
    # XFCE intentionally lacks the gio CLI; use its actual application menu.
    launch('xfce4-appfinder --disable-server')
    finder = wait_until(lambda: sandbox.exec('xdotool search --onlyvisible --class xfce4-appfinder', check=False).stdout.strip(), 10)
    assert finder
    sandbox.exec(f'xdotool windowactivate --sync {finder.splitlines()[-1]} key ctrl+a')
    sandbox.exec("xdotool type --clearmodifiers 'Xfce Terminal'; xdotool key Return")
    assert wait_until(lambda: len(names()) == 4, 15), names()
    sandbox.exec('xdotool key ctrl+alt+t')
    assert wait_until(lambda: len(names()) == 5, 15), names()
    before_here = names()
    # Exact installed Thunar Open Terminal Here action, with %f expanded.
    launch('exo-open --working-directory /workspace/terminal-project --launch TerminalEmulator')
    assert wait_until(lambda: len(names()) == 6, 15), names()
    here = next(iter(names() - before_here))
    assert sandbox.exec(f"tmux -S {socket} display-message -p -t ={here}: '#{{pane_current_path}}'").stdout.strip() == '/workspace/terminal-project'

    before = names()
    arg = '-x'
    launch(f"{terminal} {arg} sh -c " + shlex.quote('printf explicit-command > /workspace/explicit-terminal-proof'))
    assert wait_until(lambda: sandbox.exec('cat /workspace/explicit-terminal-proof', check=False).stdout == 'explicit-command', 15)
    assert names() == before
    sandbox.exec(f'tmux -S {socket} kill-server')


def test_native_chooser_and_named_title(sandbox):
    """Native chooser hides lease internals and switches only its initiating client."""
    sandbox.exec("python3 - <<'PY'\n" + r'''
import os, pty, select, subprocess, time
terminal='xfce4-terminal'
socket='/run/workspace/deskyard-terminal/tmux.sock'
def tmux(*args):
    return subprocess.check_output(['tmux','-S',socket,*args],text=True).strip()
def wait(f):
    end=time.monotonic()+10
    while time.monotonic()<end:
        try:
            result=f()
            if result:return result
        except subprocess.CalledProcessError:pass
        time.sleep(.04)
    raise AssertionError('native chooser state timed out')
def clients():
    return dict(line.split(' ',1) for line in tmux('list-clients','-F','#{client_pid} #{client_session}').splitlines())
def attach(*args):
    pid,fd=pty.fork()
    if pid==0:
        os.environ['TERM']='xterm-256color'
        os.execvp('deskyard',['deskyard',*args])
    os.set_inheritable(fd,False)
    return pid,fd
def read(fd):
    data=b''
    while select.select([fd],[],[],.2)[0]:data+=os.read(fd,65536)
    return data
attachments=[]
lease=None
try:
    subprocess.Popen([terminal],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
    wait(lambda:len(clients())==1)
    native=next(iter(clients()))
    original=clients()[native]
    window=wait(lambda:subprocess.check_output(['xdotool','search','--onlyvisible','--class',terminal],text=True).splitlines())[-1]
    attachments.append(attach('terminal-helper',original))
    wait(lambda:len(clients())==2)
    other=next(pid for pid in clients() if pid!=native)
    # The same lease path used by a real browser creates build and a private group.
    lease_id='b'*32
    lease=subprocess.Popen(['deskyard','terminal-lease','build',lease_id],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    assert lease.stdout.readline()==b'prepared\n'
    attachments.append(attach('terminal-attach',lease_id))
    assert lease.stdout.readline()==b'ready\n'
    remote=next(pid for pid in clients() if pid not in [native,other])
    group='_deskyard_'+lease_id
    assert clients()[remote]==group
    read(attachments[1][1])
    os.write(attachments[1][1],b'\x02s')
    guarded=read(attachments[1][1])
    assert b'Choose in browser Sessions' in guarded, ('leased client was offered native chooser',guarded)
    assert clients()[remote]==group
    assert tmux('display-message','-p','-t','=build:','#{pane_in_mode}')=='0'
    read(attachments[0][1])
    subprocess.run(['xdotool','windowactivate','--sync',window,'key','--clearmodifiers','ctrl+b','s'],check=True)
    wait(lambda:tmux('display-message','-p','-t','='+original+':','#{pane_in_mode}')=='1')
    chooser=read(attachments[0][1])
    assert b'build' in chooser and original.encode() in chooser,chooser
    assert b'_deskyard_' not in chooser, ('private group leaked in chooser',chooser)
    # Expand build's windows and panes: owned row formatting must remain useful
    # without reintroducing session-group metadata or private preview labels.
    subprocess.run(['xdotool','key','Home','Right','Right'],check=True)
    expanded=read(attachments[0][1])
    assert b'_deskyard_' not in expanded,('private group leaked after expand',expanded)
    subprocess.run(['xdotool','key','Home','Return'],check=True)
    wait(lambda:clients().get(native)=='build')
    assert clients()[other]==original,clients()
    assert clients()[remote]==group,clients()
    assert wait(lambda:'build' in subprocess.check_output(['xdotool','getwindowname',window],text=True))
    # A chooser switch does not end the old shell or its jobs.
    assert {l.split('\t')[0] for l in subprocess.check_output(['deskyard','terminal-list'],text=True).splitlines()}=={original,'build'}
    lease.stdin.close()
    lease.wait(timeout=10)
    wait(lambda:remote not in clients())
    assert clients()[native]=='build' and clients()[other]==original, clients()
    assert {l.split('\t')[0] for l in subprocess.check_output(['deskyard','terminal-list'],text=True).splitlines()}=={original,'build'}
finally:
    if lease and lease.stdin and not lease.stdin.closed:lease.stdin.close()
    subprocess.run(['tmux','-S',socket,'kill-server'],stderr=subprocess.DEVNULL)
    for pid,fd in attachments:
        os.close(fd);os.waitpid(pid,0)
    if lease:lease.wait(timeout=10)
PY''')
