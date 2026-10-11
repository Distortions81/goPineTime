#!/usr/bin/env python3
"""Build/validate first, then upload immediately when goPine is ready (Linux)."""
import argparse
import datetime
import fcntl
import hashlib
import http.server
import json
import os
import pathlib
import re
import secrets
import subprocess
import sys
import threading

import ota_package

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUTPUT = ROOT / 'build/ota'
CONFIG = OUTPUT / 'device.json'


def target_config(address, adapter, saved):
    address = address or saved.get('address', '')
    adapter = adapter or saved.get('adapter', '')
    if not re.fullmatch(r'(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}', address):
        raise ValueError('Specify --address WATCH_GOPINE_MAC on the first run')
    if not re.fullmatch(r'hci[0-9]+', adapter):
        raise ValueError('Specify --adapter hci1 (or your chosen USB adapter) on the first run')
    return {'address': address.upper(), 'adapter': adapter}


def prepare(args):
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:\+\d+)?', args.version):
        raise ValueError('Version must be major.minor.revision[+build]')
    if sys.platform != 'linux':
        raise ValueError('Linux with BlueZ is required')
    import bleak  # Fail before the watch is put into update mode.
    saved = json.loads(CONFIG.read_text()) if CONFIG.exists() else {}
    if saved.get('mode') != 'gopine':
        saved = {}  # Old saved targets are recovery identities, never guess.
    target = target_config(args.address, args.adapter, saved)
    if not pathlib.Path('/sys/class/bluetooth', target['adapter']).exists():
        raise ValueError(f"Bluetooth adapter {target['adapter']} is missing")
    OUTPUT.mkdir(parents=True, exist_ok=True)
    package = args.package
    if package is None:
        tinygo = os.environ.get('TINYGO', 'tinygo')
        version = subprocess.check_output([tinygo, 'version'], text=True)
        if not version.startswith('tinygo version 0.42.0 '):
            raise ValueError('Build needs TinyGo 0.42.0; set TINYGO to its executable')
        print('Building 0x8020 application and checking memory budgets. Leave the watch alone for now.', flush=True)
        # Seeds TinyGo libc headers needed by build-ble.py on a fresh installation.
        subprocess.run([tinygo, 'build', '-target=./targets/pinetime-gopine.json',
                        '-o', str(OUTPUT/'toolchain-check.elf'), '.'], cwd=ROOT, check=True)
        subprocess.run(['bash', 'scripts/build-ota.sh', '--ble', args.version], cwd=ROOT, check=True)
        package = OUTPUT / f'gopine-update-{args.version}.zip'
    package = package.resolve()
    digest, version, image, _ = ota_package.read_package(package, version=args.version)
    # Freeze the prepared bytes: another build must not change the queued image.
    prepared = OUTPUT / 'prepared' / f'{digest}.zip'
    prepared.parent.mkdir(exist_ok=True)
    data = package.read_bytes()
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError('Package changed during preparation')
    prepared.write_bytes(data)
    CONFIG.write_text(json.dumps(dict(target, mode="gopine"), indent=2)+'\n')
    print(f"Ready: {version}, {len(image):,} bytes, SHA-256 {digest}", flush=True)
    print(f"Target: {target['address']} via {target['adapter']}", flush=True)
    return UploadJob(prepared, digest, version, target)


class UploadJob:
    def __init__(self, package, digest, version, target):
        self.package, self.digest, self.version, self.target = package, digest, version, target
        self.lock = threading.Lock()
        self.state, self.progress, self.logs = 'ready', 0, []
        self.worker = None
        self.session = secrets.randbelow(0xffffffff) + 1

    def snapshot(self):
        with self.lock:
            return dict(state=self.state, progress=self.progress, logs=list(self.logs),
                        version=self.version, sha256=self.digest, **self.target)

    def append(self, line):
        print(line, flush=True)
        with self.lock:
            self.logs.append(line)
            self.logs = self.logs[-300:]
            match = re.search(r'Upload (\d+)%', line)
            if match:
                self.progress = int(match[1])

    def start(self):
        with self.lock:
            if self.state not in ('ready', 'connection_failed'):
                return False
            self.state, self.progress = 'uploading', 0
            self.worker = threading.Thread(target=self.run, daemon=True)
            self.worker.start()
            return True

    def confirm(self):
        with self.lock:
            if self.state != 'awaiting_install':
                return False
            self.state = 'confirmed'
            return True

    def run(self):
        result = 2
        try:
            with (OUTPUT/'upload.lock').open('a') as lockfile:
                # Prevent two local pages/terminals from writing at once.
                fcntl.flock(lockfile, fcntl.LOCK_EX | fcntl.LOCK_NB)
                stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
                logpath = OUTPUT / f'upload-{self.version}-{stamp}.log'
                with logpath.open('w') as log:
                    log.write(f'{self.version} {self.digest} {self.target}\n')
                    command = [sys.executable, '-u', str(ROOT/'scripts/direct_upload.py'),
                               '--package', str(self.package), '--sha256', self.digest,
                               '--adapter', self.target['adapter'], '--address', self.target['address']]
                    command += ['--session', str(self.session)]
                    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                          stdin=subprocess.DEVNULL, text=True) as process:
                        for line in process.stdout:
                            log.write(line); log.flush()
                            self.append(line.rstrip())
                        result = process.wait()
        except Exception as error:
            self.append(f'Updater stopped: {error}')
        with self.lock:
            self.state = {0: 'awaiting_install', 1: 'connection_failed'}.get(result, 'failed')


def make_server(job, port):
    token = secrets.token_urlsafe(32)
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def valid_host(self):
            return self.headers.get('Host') == f'127.0.0.1:{self.server.server_port}'

        def reply(self, status, body, kind='application/json'):
            data = body if isinstance(body, bytes) else json.dumps(body).encode()
            self.send_response(status)
            self.send_header('Content-Type', kind)
            self.send_header('Content-Length', str(len(data)))
            self.send_header('Cache-Control', 'no-store')
            self.send_header('X-Content-Type-Options', 'nosniff')
            self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; frame-ancestors 'none'")
            self.end_headers(); self.wfile.write(data)

        def authorized(self):
            origin = self.headers.get('Origin')
            expected = f'http://127.0.0.1:{self.server.server_port}'
            return self.valid_host() and origin in (None, expected) and secrets.compare_digest(self.headers.get('X-OTA-Token', ''), token)

        def do_GET(self):
            if not self.valid_host():
                return self.reply(403, {'error': 'Loopback host required'})
            if self.path == '/':
                page = (ROOT/'scripts/ota.html').read_text().replace('__TOKEN__', token)
                return self.reply(200, page.encode(), 'text/html; charset=utf-8')
            if self.path == '/status' and self.authorized():
                return self.reply(200, job.snapshot())
            self.reply(404, {'error': 'Not found'})

        def do_POST(self):
            if not self.authorized():
                return self.reply(403, {'error': 'Local session required'})
            if self.path == '/upload':
                ok = job.start()
            elif self.path == '/confirm':
                ok = job.confirm()
            else:
                return self.reply(404, {'error': 'Not found'})
            self.reply(200 if ok else 409, job.snapshot())

    return http.server.ThreadingHTTPServer(('127.0.0.1', port), Handler)


def serve(job, port):
    server = make_server(job, port)
    print(f'Open http://127.0.0.1:{server.server_port} — build is ready; follow the page to connect your watch.', flush=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('--address', help='goPine MAC, saved locally after first use')
    parser.add_argument('--adapter', help='Bluetooth adapter, saved locally after first use')
    parser.add_argument('--package', type=pathlib.Path, help='Use an existing ZIP instead of building')
    parser.add_argument('--web', action='store_true', help='Serve local browser controls after preparing')
    parser.add_argument('--port', type=int, default=8765)
    parser.add_argument('--ready', action='store_true', help='Watch already ready: upload immediately after preparation')
    parser.add_argument('--prepare-only', action='store_true', help='Build and validate, then exit without connecting')
    args = parser.parse_args()
    job = prepare(args)
    if args.prepare_only:
        return 0
    if args.web:
        serve(job, args.port)
        return 0
    if not args.ready:
        input('Open Firmware Update on goPine and hold to connect, then press Enter. ')
    job.start()
    job.worker.join()
    state = job.snapshot()['state']
    if state == 'awaiting_install':
        print('Tap INSTALL on the watch, then KEEP after reboot.')
        return 0
    print('Read the watch message and use Retry on its update screen.')
    return 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (Exception, KeyboardInterrupt) as error:
        print(f'Updater stopped: {error}', file=sys.stderr)
        sys.exit(1)
