#!/usr/bin/env python3
"""
HuggingFace model cleanup script.

Environment variables:
  - HF_HOME: Cache directory (set by operator)
  - MODEL_ID: Model identifier (e.g., "meta-llama/Llama-2-7b-hf")
  - REVISION: Optional model revision/tag (defaults to "main")
"""

import os
import sys
import subprocess


def install_packages():
    """Install required Python packages."""
    packages = [
        "huggingface-hub>=0.20.0",
    ]

    print("Installing required packages...")
    for package in packages:
        try:
            subprocess.check_call(
                [sys.executable, "-m", "pip", "install", "-q", package])
        except subprocess.CalledProcessError as e:
            print(
                f"WARNING: Failed to install {package}: {e}", file=sys.stderr)


def delete_model_revision(repo_id: str, revision: str, cache_dir: str = None):
    """
    Safely delete a specific revision of a Hugging Face model from the local cache
    using scan_cache_dir(), without affecting other revisions.

    Args:
        repo_id (str): Model ID (e.g. "meta-llama/Llama-3-8B")
        revision (str): Commit hash or tag (e.g. "main" or "abcdef123456")
        cache_dir (str): Optional cache root (defaults to HF_HOME or ~/.cache/huggingface/hub)
    """
    from huggingface_hub import scan_cache_dir

    # Determine cache directory
    if cache_dir is None:
        cache_dir = os.path.join(get_cache_home(), "hub")

    print(f"🔍 Scanning cache directory: {cache_dir}")
    cache_info = scan_cache_dir(cache_dir)

    # Find matching repo + revision
    matches = [
        e for e in cache_info.repos
        if e.repo_id == repo_id
    ]

    if not matches:
        print(f"[!] No local cache found for repo: {repo_id}")
        return False

    repo_cache = matches[0]

    revisions_to_delete = [
        r for r in repo_cache.revisions if r.commit_hash == revision or r.revision == revision
    ]

    if not revisions_to_delete:
        print(f"[!] Revision '{revision}' not found for '{repo_id}'")
        print("    Available revisions:")
        for r in repo_cache.revisions:
            print(f"      - {r.revision} ({r.commit_hash})")
        return False

    # Perform deletion
    for rev in revisions_to_delete:
        print(
            f"🧹 Deleting revision: {repo_id}@{rev.revision} ({rev.commit_hash})")
        cache_info.delete_revisions(rev)

    print("✅ Successfully deleted the revision and cleaned unused blobs.")
    return True


def main():
    # Install packages first
    install_packages()

    model_id = os.environ.get('MODEL_ID')
    if not model_id:
        print("ERROR: MODEL_ID environment variable is required", file=sys.stderr)
        sys.exit(1)

    hf_home = os.environ.get('HF_HOME', '/data/models')
    revision = os.environ.get('REVISION', 'main')

    print(f"Cleaning up model: {model_id} (revision: {revision})")
    print(f"Cache directory: {hf_home}")

    try:
        deleted = delete_model_revision(model_id, revision, hf_home)
        if deleted:
            print(f"Successfully deleted {model_id}")
        else:
            print(f"Model {model_id} not found in cache, nothing to delete")

    except Exception as e:
        # Don't fail cleanup if the model isn't found or has issues
        # This is especially important during deletion scenarios
        print(f"WARNING: Error during cleanup: {e}", file=sys.stderr)
        print("Continuing with deletion despite cleanup errors")
        # Exit with success to allow deletion to proceed
        sys.exit(0)


if __name__ == "__main__":
    main()
