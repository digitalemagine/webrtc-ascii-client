"""Run a program on a pseudo-terminal and record what it draws.

Usage: ptyrun.py OUT IDENT ROWS COLS CMD...

notcurses (ncplayer) asks the terminal what it supports at startup and only
draws sextants for terminals it recognizes; without a terminal it falls back to
half blocks, and inside tmux 3.8 it waits for an answer that never comes. This
answers its queries as IDENT (an XTVERSION reply such as "foot(1.18.0)"), with
10x20-pixel cells, and stops the program once it has been quiet for a second.
"""

import fcntl
import os
import pty
import re
import select
import struct
import sys
import termios
import time

out, ident, rows, cols = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
cmd = sys.argv[5:]
size = struct.pack("HHHH", rows, cols, cols * 10, rows * 20)

pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, size)
    os.execvp(cmd[0], cmd)
fcntl.ioctl(fd, termios.TIOCSWINSZ, size)

# Replies in this order: notcurses sends DA1 last and reads up to its reply.
answers = [
    (rb"\x1b\[>0?q", b"\x1bP>|" + ident.encode() + b"\x1b\\"),  # XTVERSION
    (rb"\x1b\[16t", b"\x1b[6;20;10t"),  # cell size in pixels
    (rb"\x1b\[14t", b"\x1b[4;%d;%dt" % (rows * 20, cols * 10)),  # text area in pixels
    (rb"\x1b\[18t", b"\x1b[8;%d;%dt" % (rows, cols)),  # text area in cells
    (rb"\x1b\[6n", b"\x1b[1;1R"),  # cursor position
    (rb"\x1b\[c", b"\x1b[?62;22c"),  # DA1
]
answered = {}
buf = b""
start = 0  # where the output after the startup queries begins
deadline = time.time() + 30
last = time.time()
while time.time() < deadline and time.time() - last < 1.0:
    ready, _, _ = select.select([fd], [], [], 0.1)
    if not ready:
        continue
    try:
        data = os.read(fd, 65536)
    except OSError:  # the program exited
        break
    if not data:
        break
    buf += data
    last = time.time()
    reply = b""
    for pattern, answer in answers:
        n = len(re.findall(pattern, buf))
        if n > answered.get(pattern, 0):
            answered[pattern] = n
            reply += answer
    if reply:
        os.write(fd, reply)
        if answered.get(rb"\x1b\[c") == 1 and not start:
            start = len(buf)
try:
    os.kill(pid, 9)
except ProcessLookupError:
    pass
# Only what the program draws: its startup queries aren't part of the picture.
with open(out, "wb") as f:
    f.write(buf[start:])
