#!/usr/bin/env python3
"""Stopped-service backup/restore. Keys stay outside the backup; never overwrite data."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile


def digest(path):
    with path.open('rb') as stream:
        checksum = hashlib.sha256()
        for block in iter(lambda: stream.read(1024*1024), b''):
            checksum.update(block)
        return checksum.hexdigest()


def sync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def backup(database, output):
    database = database.resolve()
    if not database.is_file():
        raise ValueError('Database does not exist')
    output = output.absolute()
    if output.exists():
        raise ValueError('Output must be a new directory')
    # Same OS flock used by Store.LockServer. Held through the entire snapshot.
    with Path(str(database)+'.server.lock').open('a+b') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Server is running; stop it before making an offline backup') from None
        output.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='.fiftyapi-backup-', dir=output.parent) as temp:
            stage = Path(temp)/'snapshot'
            stage.mkdir(mode=0o700)
            manifest = {'format': 1, 'files': {}}
            for suffix in ['', '-wal', '-shm']:
                source = Path(str(database)+suffix)
                if source.exists():
                    target = stage/('fiftyapi.db'+suffix)
                    shutil.copyfile(source, target)
                    target.chmod(0o600)
                    with target.open('rb') as stream:
                        os.fsync(stream.fileno())
                    manifest['files'][target.name] = digest(target)
            (stage/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
            (stage/'manifest.json').chmod(0o600)
            with (stage/'manifest.json').open('rb') as stream:
                os.fsync(stream.fileno())
            sync_directory(stage)
            stage.rename(output)
            sync_directory(output.parent)


def restore(source, output):
    source = source.resolve()
    output = output.absolute()
    if output.exists():
        raise ValueError('Restore output must be a new directory; existing data will not be overwritten')
    manifest = json.loads((source/'manifest.json').read_text())
    files = manifest.get('files', {})
    # Restore only the supported database snapshot files.
    base = 'fiftyapi.db'
    allowed = {base, base+'-wal', base+'-shm'}
    if manifest.get('format') != 1 or base not in files or not set(files).issubset(allowed):
        raise ValueError('Invalid backup manifest')
    for name, checksum in files.items():
        path = source/name
        if path.is_symlink() or not path.is_file() or digest(path) != checksum:
            raise ValueError('Backup checksum mismatch; restore stopped')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.fiftyapi-restore-', dir=output.parent) as temp:
        stage = Path(temp)/'data'
        stage.mkdir(mode=0o700)
        for name in files:
            target = stage/name
            shutil.copyfile(source/name, target)
            target.chmod(0o600)
            # Validate the copied bytes too, so source replacement cannot pass unchecked.
            if digest(target) != files[name]:
                raise ValueError('Backup changed during restore')
            with target.open('rb') as stream:
                os.fsync(stream.fileno())
        sync_directory(stage)
        stage.rename(output)
        sync_directory(output.parent)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    create = commands.add_parser('create')
    create.add_argument('--db', type=Path, required=True)
    create.add_argument('--output', type=Path, required=True)
    recover = commands.add_parser('restore')
    recover.add_argument('--from', dest='source', type=Path, required=True)
    recover.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == 'create':
            backup(args.db, args.output)
        else:
            restore(args.source, args.output)
    except (OSError, ValueError) as error:
        parser.exit(1, f'{error}\n')
    print('Offline operation completed. Keep the original master key separately.')


if __name__ == '__main__':
    main()
