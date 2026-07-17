#!/usr/bin/env python3
"""Patch s3-tests cleanup for servers without versioning / object lock.

ceph/s3-tests setup/teardown empties buckets via list_object_versions and
delete_objects(..., BypassGovernanceRetention=True). That breaks on fbs-core:

- ListObjectVersions → NotImplemented
- leftover multipart uploads → DeleteBucket fails (FK / not empty)
- retention wait loops slow the suite to a crawl when cleanup fails

This rewrites list_versions / nuke_bucket / nuke_prefixed_buckets in-place.
Idempotent (marker-guarded).
"""
from __future__ import annotations

import pathlib
import sys

MARKER = "FBS_NUKE_FALLBACK_NO_VERSIONING"

NEW_BLOCK = f'''
# generator function that returns object listings in batches, where each
# batch is a list of dicts compatible with delete_objects()
# {MARKER}
def list_versions(client, bucket, batch_size):
    kwargs = {{'Bucket': bucket, 'MaxKeys': batch_size}}
    truncated = True
    use_versions = True
    while truncated:
        if use_versions:
            try:
                listing = client.list_object_versions(**kwargs)
            except ClientError as e:
                code = e.response.get('Error', {{}}).get('Code', '')
                if code not in ('NotImplemented', '501', 'MethodNotAllowed'):
                    raise
                use_versions = False
                kwargs = {{'Bucket': bucket, 'MaxKeys': batch_size}}
                continue
            kwargs['KeyMarker'] = listing.get('NextKeyMarker')
            kwargs['VersionIdMarker'] = listing.get('NextVersionIdMarker')
            truncated = listing['IsTruncated']
            objs = listing.get('Versions', []) + listing.get('DeleteMarkers', [])
            if len(objs):
                yield [{{'Key': o['Key'], 'VersionId': o['VersionId']}} for o in objs]
            continue

        listing = client.list_objects_v2(**kwargs)
        truncated = listing.get('IsTruncated', False)
        if truncated:
            kwargs['ContinuationToken'] = listing['NextContinuationToken']
        contents = listing.get('Contents', [])
        if contents:
            yield [{{'Key': o['Key']}} for o in contents]


def nuke_bucket(client, bucket):
    """Best-effort empty + delete for non-versioning servers."""
    batch_size = 128
    for objects in list_versions(client, bucket, batch_size):
        keys = []
        for o in objects:
            entry = {{'Key': o['Key']}}
            if 'VersionId' in o and o.get('VersionId'):
                entry['VersionId'] = o['VersionId']
            keys.append(entry)
        try:
            client.delete_objects(
                Bucket=bucket,
                Delete={{'Objects': keys, 'Quiet': True}},
            )
        except ClientError:
            for obj in objects:
                kwargs = {{'Bucket': bucket, 'Key': obj['Key']}}
                if 'VersionId' in obj and obj.get('VersionId'):
                    kwargs['VersionId'] = obj['VersionId']
                try:
                    client.delete_object(**kwargs)
                except ClientError:
                    pass

    try:
        # Abort any lingering multipart uploads before deleting the bucket.
        try:
            uploads = client.list_multipart_uploads(Bucket=bucket).get('Uploads', [])
            for u in uploads:
                try:
                    client.abort_multipart_upload(
                        Bucket=bucket,
                        Key=u['Key'],
                        UploadId=u['UploadId'],
                    )
                except ClientError:
                    pass
        except ClientError:
            pass

        client.delete_bucket(Bucket=bucket)
    except ClientError as e:
        # Leftover multiparts or other state: log and continue.
        # Tests use unique prefixes; a stuck bucket must not halt the suite.
        print(f"  warning: could not delete bucket {{bucket}}: {{e}}")


def nuke_prefixed_buckets(prefix, client=None):
    if client is None:
        client = get_client()

    buckets = get_buckets_list(client, prefix)
    for bucket_name in buckets:
        try:
            nuke_bucket(client, bucket_name)
        except Exception:
            pass

    print('Done with cleanup of buckets in tests.')
'''


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} path/to/s3tests/functional/__init__.py", file=sys.stderr)
        return 2
    path = pathlib.Path(sys.argv[1])
    text = path.read_text(encoding="utf-8")
    if MARKER in text:
        print(f"already patched: {path}")
        return 0

    start = text.find("# generator function that returns object listings in batches")
    if start < 0:
        print("could not find list_versions block start", file=sys.stderr)
        return 1
    end = text.find("\ndef configured_storage_classes(", start)
    if end < 0:
        print("could not find configured_storage_classes after nuke helpers", file=sys.stderr)
        return 1

    path.write_text(text[:start] + NEW_BLOCK.lstrip("\n") + text[end:], encoding="utf-8")
    print(f"patched: {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
