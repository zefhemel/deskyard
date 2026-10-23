from pathlib import Path
p=Path('/usr/local/bin/workspace-init')
s=p.read_text()
s=s.replace('# --- home folder owned', '''# Throwaway investigation: remap the named account before any user process.
if [ -n "${DESKYARD_PROBE_UID:-}" ]; then
    groupmod -g "$DESKYARD_PROBE_UID" user
    usermod -u "$DESKYARD_PROBE_UID" -g "$DESKYARD_PROBE_UID" user
fi
workspace_uid=$(id -u user)
workspace_gid=$(id -g user)

# --- home folder owned''')
s=s.replace('!= 1000', '!= "$workspace_uid"', 1)
s=s.replace('!= 1000', '!= "$workspace_gid"', 1)
s=s.replace('chown -R 1000:1000 /workspace','chown -R "$workspace_uid:$workspace_gid" /workspace')
s=s.replace('-o 1000 -g 1000', '-o "$workspace_uid" -g "$workspace_gid"')
p.write_text(s)

p=Path('/etc/workspace/desktop-session.sh')
p.write_text(p.read_text().replace('pgrep -u 1000', 'pgrep -u "$(id -u)"'))
