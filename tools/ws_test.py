"""Test client for doorbelld. Usage: ws_test.py [host] [--listen SECONDS]"""
import base64, json, os, socket, struct, sys, time

args = [a for a in sys.argv[1:] if not a.startswith("--") and not a.isdigit()]
HOST = args[0] if args else "doorbell.home.arpa"
LISTEN = int(sys.argv[sys.argv.index("--listen") + 1]) if "--listen" in sys.argv else 0
s = socket.create_connection((HOST, 8080), timeout=10)
key = base64.b64encode(os.urandom(16)).decode()
s.sendall(f"GET / HTTP/1.1\r\nHost: {HOST}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n".encode())
buf = b""
while b"\r\n\r\n" not in buf:
    buf += s.recv(1024)
head, buf = buf.split(b"\r\n\r\n", 1)
print(head.split(b"\r\n")[0].decode())


def need(n):
    global buf
    while len(buf) < n:
        d = s.recv(4096)
        if not d:
            raise EOFError
        buf += d
    r, buf = buf[:n], buf[n:]
    return r


def recv():
    b0, b1 = need(2)
    n = b1 & 0x7F
    if n == 126:
        n = struct.unpack(">H", need(2))[0]
    elif n == 127:
        n = struct.unpack(">Q", need(8))[0]
    return b0 & 0x0F, need(n)


def send(text):
    p = text.encode()
    m = os.urandom(4)
    assert len(p) < 126
    s.sendall(bytes([0x81, 0x80 | len(p)]) + m + bytes(c ^ m[i % 4] for i, c in enumerate(p)))


for msg in ['{"jsonrpc":"2.0","id":1,"method":"system.ping"}',
            '{"jsonrpc":"2.0","id":2,"method":"system.info"}',
            '{"jsonrpc":"2.0","id":3,"method":"gibtsnicht"}']:
    send(msg)
    t0 = time.time()
    op, p = recv()
    print(f"{(time.time() - t0) * 1000:.0f} ms", p.decode())

if LISTEN:
    s.settimeout(LISTEN)
    print(f"waiting {LISTEN} s for events", flush=True)
    end = time.time() + LISTEN
    try:
        while time.time() < end:
            print(recv()[1].decode(), flush=True)
    except socket.timeout:
        print("no further events")
