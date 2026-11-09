"""Saves H.264 from ws://<host>:8080/video. Usage: ws_video_test.py <file> [seconds] [host]"""
import base64, os, socket, struct, sys, time

OUT = sys.argv[1]
SECS = int(sys.argv[2]) if len(sys.argv) > 2 else 10
HOST = sys.argv[3] if len(sys.argv) > 3 else "doorbell.home.arpa"
s = socket.create_connection((HOST, 8080), timeout=10)
key = base64.b64encode(os.urandom(16)).decode()
s.sendall(f"GET /video HTTP/1.1\r\nHost: {HOST}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n".encode())
buf = b""
while b"\r\n\r\n" not in buf:
    buf += s.recv(4096)
head, buf = buf.split(b"\r\n\r\n", 1)
print(head.split(b"\r\n")[0].decode())


def need(n):
    global buf
    while len(buf) < n:
        d = s.recv(65536)
        if not d:
            raise EOFError
        buf += d
    r, buf = buf[:n], buf[n:]
    return r


frames = bytes_ = 0
first = None
t0 = time.time()
with open(OUT, "wb") as f:
    while time.time() - t0 < SECS:
        b0, b1 = need(2)
        n = b1 & 0x7F
        if n == 126:
            n = struct.unpack(">H", need(2))[0]
        elif n == 127:
            n = struct.unpack(">Q", need(8))[0]
        data = need(n)
        if b0 & 0x0F != 2:
            continue
        if first is None:
            first = data[:6].hex()
            print(f"first frame after {time.time() - t0:.2f} s, starts with {first}")
        f.write(data)
        frames += 1
        bytes_ += n
dt = time.time() - t0
print(f"{frames} frames in {dt:.1f} s = {frames / dt:.1f} fps, {bytes_ * 8 / dt / 1000:.0f} kbit/s")
