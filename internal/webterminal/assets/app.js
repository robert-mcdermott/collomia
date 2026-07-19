import { Terminal } from './vendor/xterm.mjs';
import { FitAddon } from './vendor/addon-fit.mjs';

const fragment = new URLSearchParams(window.location.hash.slice(1));
const token = fragment.get('token');
window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`);

const status = document.querySelector('#status');
const terminalElement = document.querySelector('#terminal');
const terminal = new Terminal({
  allowProposedApi: false,
  convertEol: false,
  cursorBlink: true,
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
  fontSize: 14,
  scrollback: 10000,
  theme: {
    background: '#090b10',
    foreground: '#d8deed',
    cursor: '#f6c177',
    selectionBackground: '#334160'
  }
});
const fitAddon = new FitAddon();
terminal.loadAddon(fitAddon);
terminal.open(terminalElement);

let socket;
let authenticated = false;
let resizeFrame = 0;

function showStatus(message, isError = false) {
  status.textContent = message;
  status.classList.toggle('error', isError);
  status.classList.add('visible');
}

function hideStatus() {
  status.classList.remove('visible', 'error');
}

function sendResize() {
  if (!authenticated || socket.readyState !== WebSocket.OPEN) return;
  fitAddon.fit();
  socket.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }));
}

function scheduleResize() {
  window.cancelAnimationFrame(resizeFrame);
  resizeFrame = window.requestAnimationFrame(sendResize);
}

if (!token) {
  showStatus('Missing access token. Start a new session with collo --web.', true);
  terminal.writeln('\r\n\x1b[31mAuthentication token missing.\x1b[0m');
} else {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  socket = new WebSocket(`${protocol}//${window.location.host}/ws`);
  socket.binaryType = 'arraybuffer';

  socket.addEventListener('open', () => {
    socket.send(JSON.stringify({ type: 'auth', token }));
    authenticated = true;
    hideStatus();
    sendResize();
    terminal.focus();
  });

  socket.addEventListener('message', (event) => {
    if (event.data instanceof ArrayBuffer) {
      terminal.write(new Uint8Array(event.data));
      return;
    }
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      showStatus('Received an invalid server message.', true);
      return;
    }
    if (message.type === 'exit') {
      authenticated = false;
      terminal.writeln(`\r\n\x1b[90m[Collomia exited with status ${message.code}]\x1b[0m`);
      showStatus(`Session ended (status ${message.code}).`);
    } else if (message.type === 'error') {
      terminal.writeln(`\r\n\x1b[31m${message.message}\x1b[0m`);
      showStatus(message.message, true);
    }
  });

  socket.addEventListener('close', (event) => {
    authenticated = false;
    if (event.code !== 1000) {
      showStatus(event.reason || 'Terminal connection closed.', true);
    }
  });

  socket.addEventListener('error', () => {
    showStatus('Could not connect to the local Collomia terminal.', true);
  });

  terminal.onData((data) => {
    if (!authenticated || socket.readyState !== WebSocket.OPEN) return;
    socket.send(new TextEncoder().encode(data));
  });

  const observer = new ResizeObserver(scheduleResize);
  observer.observe(terminalElement);
  window.addEventListener('resize', scheduleResize);
  window.addEventListener('beforeunload', () => socket.close(1000, 'browser closed'));
}
