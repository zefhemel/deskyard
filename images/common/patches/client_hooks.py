"""Add the Deskyard v1 boundary to the exact Selkies 2.1.0 wheel client.

The wheel ships bundled JavaScript twice. Hash both before editing either;
upstream upgrades require reviewing these call sites, never fuzzy matching.
"""
import hashlib
from pathlib import Path
import sys

CORE_SHA256 = '866a4ddf63c8f65e136b244ef9b8d33242c8d0cf6a5f728267707642f30330af'
CORE_FILES = ('src/selkies-core.js', 'assets/selkies-core-BGbb3Bx7.js')
MARKER = '<meta name="deskyard-client-contract" content="1">'
H = 'window.__deskyardHooks'


def patch(client: Path):
    sources = []
    for name in CORE_FILES:
        data = (client / name).read_bytes()
        if hashlib.sha256(data).hexdigest() != CORE_SHA256:
            raise ValueError(f'{name}: unexpected Selkies client SHA-256; review the v1 hook call sites')
        sources.append(data.decode())
    index = client / 'index.html'
    page = index.read_text()
    if page.count('<head>') != 1 or 'deskyard-client-contract' in page:
        raise ValueError('unexpected client page; review the v1 contract marker insertion')
    source = sources[0]

    def replace(old, new, count=1):
        nonlocal source
        actual = source.count(old)
        if actual != count:
            raise ValueError(f'v1 hook expected {count} occurrences, found {actual}: {old}')
        source = source.replace(old, new)

    replace('window.selkiesTransport=u', f'window.selkiesTransport=u,{H}.transport(u)')
    replace('window.selkiesTransport=null', f'window.selkiesTransport=null,{H}.transport(null)')
    replace('ke.href', f'{H}.url(ke.href)', 2)
    # These are the WebSocket-mode transport send call sites. The adapter's
    # explicit sound controls call transport.send directly to bypass filtering.
    start = source.index('function xi(')
    head, websocket = source[:start], source[start:]
    websocket = websocket.replace('u.send(', f'{H}.send(u,')
    source = head + websocket
    replace('navigator.mediaDevices.getUserMedia(', f'{H}.capture(', 4)
    replace('window.isSecureContext?(window.VideoDecoder', f'(window.isSecureContext||{H}.allowInsecure)?(window.VideoDecoder')
    replace('`onpointerrawupdate`in window', f'!{H}.pointerMoveOnly&&`onpointerrawupdate`in window')
    replace('window.webrtcInput=e,e.setDisplayLayouts', f'window.webrtcInput=e,{H}.input(e),e.setDisplayLayouts')
    replace('window.webrtcInput=null', f'window.webrtcInput=null,{H}.input(null)')
    replace('setTrackpadMode(e){let t=!!e;', f'setTrackpadMode(e){{e={H}.touchMode(e);let t=!!e;')
    for method in ('_twoFingerStart', '_twoFingerMove'):
        replace(f'{method}(e,t,n,r){{', f'{method}(e,t,n,r){{[e,t,n,r]={H}.points(e,t,n,r);')
    replace('_sendZoomNotches(e){if(e===0)return;', f'_sendZoomNotches(e){{if(this._twoFinger?.kind===`pinch`&&{H}.zoom(e,this._twoFinger.x0,this._twoFinger.y0))return;if(e===0)return;')
    replace('_drainScrollNotches(e,t){', f'_drainScrollNotches(e,t){{if(this._twoFinger?.kind===`scroll`&&{H}.pan(e,t))return 0;')
    replace('_trackpadMove(e,t,n,r){', f'_trackpadMove(e,t,n,r){{let [dx,dy]={H}.trackpadOrigin(e);e.lastX+=dx;e.lastY+=dy;')
    # Report prediction even when Selkies composites the cursor into video;
    # its DOM-cursor rendering returns early in that mode.
    replace('_placeEchoCursor(){let e=this._echo,t=',
            f'_placeEchoCursor(){{let e=this._echo;if(e&&this._trackpadMode&&e.display===this.displayId&&this._trackpadTouches.size===1&&(this._trackpadGestureMode===`moving`||this._trackpadGestureMode===`dragging`)){{let box=this._streamBox();if(box?.scaleX>0&&box?.scaleY>0){{let x=e.x,y=e.y;for(let[,dx,dy]of this._echoDeltas)[x,y]=this._echoStep(x,y,dx,dy);{H}.cursor(box.left+x/box.scaleX,box.top+y/box.scaleY)}}}}let t=')
    replace('e.focus(', f'{H}.focus(e,', 3)
    replace('n.focus(),n.select()', f'{H}.focus(n),n.select()')
    replace('h=new(window.AudioContext||window.webkitAudioContext)({sampleRate:48e3}),', f'h=new(window.AudioContext||window.webkitAudioContext)({{sampleRate:48e3}}),{H}.playback(h),')
    replace('await h.audioWorklet.addModule(', f'await {H}.addWorklet(h,')
    replace('new AudioWorkletNode(h,', f'{H}.workletNode(h,')
    replace('_.connect(h.destination),console.log(`Playback AudioWorkletProcessor', f'_.connect(h.destination),{H}.playbackOutput(_),console.log(`Playback AudioWorkletProcessor')
    replace('type===`audioTick`){', f'type===`audioTick`){{{H}.audioTick();')
    replace('alert(`Microphone error:', f'{H}.alert(`Microphone error:')
    replace('alert(`Webcam error:', f'{H}.alert(`Webcam error:')
    for name in CORE_FILES:
        (client / name).write_text(source)
    index.write_text(page.replace('<head>', '<head>' + MARKER, 1))


if __name__ == '__main__':
    patch(Path(sys.argv[1]))
