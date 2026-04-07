#!/usr/bin/env python3
"""
HuggingFace model download script.

Environment variables:
  - HF_HOME: Target directory for downloaded models (set by operator)
  - HF_TOKEN: Optional authentication token
  - MODEL_ID: Model identifier (e.g., "meta-llama/Llama-2-7b-hf")
  - REVISION_ID: Optional model revision/tag (defaults to "main")
                  Can be a branch name, tag, or commit hash.
                  When spec.revision is configured in ModelRepository,
                  this maps to spec.revision.revisions[].ref
"""

import os
import sys
import subprocess

def install_packages():
    """Install required Python packages."""
    packages = [
        "huggingface-hub>=0.20.0",
        "requests>=2.31.0"
    ]

    print("Installing required packages...")
    for package in packages:
        try:
            subprocess.check_call([sys.executable, "-m", "pip", "install", "-q", package])
        except subprocess.CalledProcessError as e:
            print(f"WARNING: Failed to install {package}: {e}", file=sys.stderr)
            # Continue anyway, package might already be installed

def main():
    # Install packages first
    install_packages()

    # Import after installation
    from huggingface_hub import snapshot_download

    model_id = os.environ.get('MODEL_ID')
    if not model_id:
        print("ERROR: MODEL_ID environment variable is required", file=sys.stderr)
        sys.exit(1)

    hf_home = os.environ.get('HF_HOME', '/data/models')
    token = os.environ.get('HF_TOKEN', False)
    revision = os.environ.get('REVISION_ID', 'main')

    print(f"Downloading model: {model_id} (revision: {revision})")
    print(f"HF_HOME: {hf_home}")
    print(f"Cache dir: {hf_home}/hub")

    if token:
        print("Using authentication token from HF_TOKEN")
    else:
        print("No authentication token provided (public models only)")

    try:
        # Download into HF cache format ($HF_HOME/hub/models--org--name/snapshots/<hash>/)
        # This enables: revision deduplication, scan_cache_dir cleanup, and vLLM's
        # get_model_path() to locate models by repo ID with local_files_only=True
        path = snapshot_download(
            repo_id=model_id,
            revision=revision,
            token=token,
            resume_download=True,
        )
        print(f"Successfully downloaded {model_id} to {path}")
    except Exception as e:
        print(f"ERROR: Failed to download model: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
