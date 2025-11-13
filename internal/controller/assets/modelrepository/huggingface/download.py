#!/usr/bin/env python3
"""
HuggingFace model download script.

Environment variables:
  - HF_HOME: Cache directory (set by operator)
  - HF_HUB_CACHE: Hub cache directory (set by operator)
  - HF_TOKEN: Optional authentication token
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
    token = os.environ.get('HF_TOKEN')
    revision = os.environ.get('REVISION', 'main')

    print(f"Downloading model: {model_id} (revision: {revision})")
    print(f"Target directory: {hf_home}")

    if token:
        print("Using authentication token from HF_TOKEN")
    else:
        print("No authentication token provided (public models only)")

    try:
        snapshot_download(
            repo_id=model_id,
            revision=revision,
            cache_dir=hf_home,
            token=token,
            resume_download=True,
        )
        print(f"Successfully downloaded {model_id}")
    except Exception as e:
        print(f"ERROR: Failed to download model: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
