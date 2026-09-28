// Event bus client for gameday pages.
//
// Usage:
//   const bus = createEventBus({
//     url: '/ui/events',
//     types: ['game.match.state'],
//     replay: 100,
//     onEvent: (env) => { ... },
//     onStatus: (status) => { ... },
//   });
//   ...
//   bus.close();
//
// Envelopes delivered to onEvent have the shape:
//   {seq, ts, type, data, replay?}
// The first envelope is the "subscribed" system message. When the bus
// shuts down, a "close" message is delivered before the socket
// closes.
//
// The client pings the socket every 15 seconds, detects sequence
// gaps, and reconnects with exponential backoff plus jitter.

function createEventBus(opts) {
  const url = opts.url || '/ui/events';
  const types = opts.types || [];
  const replay = opts.replay || 0;
  const onEvent = opts.onEvent || (() => {});
  const onStatus = opts.onStatus || (() => {});

  const base = url.startsWith('ws')
    ? url
    : (location.protocol === 'https:' ? 'wss://' : 'ws://') +
        location.host + url;

  let closed = false;
  let socket = null;
  let lastSeq = 0;
  let attempt = 0;
  let pingTimer = null;

  function connect() {
    if (closed) {
      return;
    }
    const params = new URLSearchParams();
    if (types.length > 0) {
      params.set('types', types.join(','));
    }
    if (replay > 0) {
      params.set('replay', String(replay));
    }
    const qs = params.toString();
    const target = base + (qs ? '?' + qs : '');

    onStatus('connecting');
    socket = new WebSocket(target);

    socket.onopen = () => {
      attempt = 0;
      onStatus('open');
      pingTimer = setInterval(() => {
        if (socket && socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'ping' }));
        }
      }, 15000);
    };

    socket.onmessage = (msg) => {
      let env;
      try {
        env = JSON.parse(msg.data);
      } catch (e) {
        return;
      }
      if (typeof env.seq === 'number') {
        if (lastSeq !== 0 && env.seq !== lastSeq + 1 && !env.replay) {
          // A gap: events were dropped for this subscriber.
          onStatus('gap');
        }
        if (env.replay || env.seq > lastSeq) {
          lastSeq = env.seq;
        }
      }
      onEvent(env);
    };

    socket.onclose = () => {
      clearInterval(pingTimer);
      pingTimer = null;
      if (closed) {
        return;
      }
      onStatus('closed');
      const delay = Math.min(15000, Math.pow(2, attempt) * 500) +
        Math.floor(Math.random() * 250);
      attempt += 1;
      setTimeout(connect, delay);
    };

    socket.onerror = () => {
      // onclose always follows onerror; nothing to do here.
    };
  }

  connect();

  return {
    close() {
      closed = true;
      clearInterval(pingTimer);
      if (socket) {
        socket.onclose = null;
        socket.close(1000, 'client closing');
        socket = null;
      }
    },
  };
}
