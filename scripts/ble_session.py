"""Serialize local goPine BLE clients for one adapter and peripheral.

BlueZ shares a device connection between clients, so one client's disconnect
can interrupt another client. Hold TargetLock through asynchronous cleanup,
including asyncio.run() shutdown. This is an advisory lock for goPine tools;
it does not reserve BlueZ against unrelated applications or other computers.
"""
import errno
import fcntl
import hashlib
import os
from pathlib import Path
import re
import stat
import tempfile


class SessionLockError(RuntimeError):
    """The target lock could not be created safely."""


class SessionBusy(SessionLockError):
    """Another local goPine client owns this adapter and peripheral."""


class TargetLock:
    def __init__(self, adapter, address):
        if not re.fullmatch(r'hci[0-9]+', adapter, re.IGNORECASE):
            raise ValueError('adapter must be an hci index')
        if not re.fullmatch(r'(?:[0-9a-f]{2}:){5}[0-9a-f]{2}', address, re.IGNORECASE):
            raise ValueError('address must be a Bluetooth address')
        self.adapter = 'hci' + str(int(adapter[3:]))
        self.address = address.upper()
        self._fd = None

    def __enter__(self):
        if self._fd is not None:
            raise SessionLockError('TargetLock is already held by this instance')
        uid = os.geteuid()
        directory = Path(tempfile.gettempdir()) / f'gopine-ble-{uid}'
        key = hashlib.sha256((self.adapter + '\0' + self.address).encode('ascii')).hexdigest()
        directory_fd = None
        lock_fd = None
        try:
            try:
                directory.mkdir(mode=0o700)
            except FileExistsError:
                pass
            directory_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
            info = os.fstat(directory_fd)
            if info.st_uid != uid or stat.S_IMODE(info.st_mode) & 0o077:
                raise SessionLockError('BLE lock directory must be private and owned by the current user')
            lock_fd = os.open(key + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK,
                              0o600, dir_fd=directory_fd)
            info = os.fstat(lock_fd)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != uid or
                    stat.S_IMODE(info.st_mode) & 0o077 or info.st_nlink != 1):
                raise SessionLockError('BLE lock file must be private, regular and owned by the current user')
            try:
                fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError as error:
                if error.errno in (errno.EACCES, errno.EAGAIN):
                    raise SessionBusy(f'Another goPine Bluetooth session is using {self.adapter} {self.address}; '
                                      'wait for it to finish before retrying') from None
                raise
            self._fd, lock_fd = lock_fd, None
            return self
        except OSError as error:
            raise SessionLockError(f'Cannot acquire BLE target lock: {error.strerror}') from error
        finally:
            if lock_fd is not None:
                os.close(lock_fd)
            if directory_fd is not None:
                os.close(directory_fd)

    def __exit__(self, exc_type, exc_value, traceback):
        if self._fd is not None:
            fd, self._fd = self._fd, None
            try:
                fcntl.flock(fd, fcntl.LOCK_UN)
            finally:
                os.close(fd)
        # Keep the file: unlinking would let a new client lock a different inode
        # while an existing client still owns the old one. Process exit also
        # releases flock, so a stale file never means a stale session.
