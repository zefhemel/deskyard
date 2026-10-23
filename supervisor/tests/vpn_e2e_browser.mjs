// Exercise only the fixture desktop's shared browser through the real CLI CDP
// tunnel. No host browser is automated and no account information is entered.
const ws = new WebSocket(process.env.CDP);
const pending = new Map();
let nextID = 0;
ws.onmessage = event => {
  const response = JSON.parse(event.data);
  const request = pending.get(response.id);
  if (!request) return;
  pending.delete(response.id);
  clearTimeout(request.timer);
  if (response.error) request.reject(new Error(response.error.message));
  else request.resolve(response.result);
};
function send(method, params = {}, sessionId) {
  return new Promise((resolve, reject) => {
    const id = ++nextID;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`CDP deadline: ${method}`));
    }, 15000);
    pending.set(id, {resolve, reject, timer});
    ws.send(JSON.stringify({id, method, params, sessionId}));
  });
}
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('CDP connection deadline')), 15000);
    ws.onopen = () => { clearTimeout(timer); resolve(); };
    ws.onerror = () => { clearTimeout(timer); reject(new Error('CDP connection failed')); };
  });
  const target = await send('Target.createTarget', {url: 'https://example.com'});
  const session = await send('Target.attachToTarget', {targetId: target.targetId, flatten: true});
  let title;
  for (let i = 0; i < 30; i++) {
    const result = await send('Runtime.evaluate', {
      expression: 'document.title', returnByValue: true,
    }, session.sessionId);
    title = result.result.value;
    if (title === 'Example Domain') break;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
  if (title !== 'Example Domain') throw new Error('Shared browser navigation failed');
  console.log('PASS browser navigation via supervisor CDP');
} finally {
  ws.close();
  for (const request of pending.values()) clearTimeout(request.timer);
}
