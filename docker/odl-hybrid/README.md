# ODL hybrid runtime dependencies

The Docker image uses the hash-checked `requirements.txt` exported from `uv.lock`.
The lock covers Python 3.12 on Linux amd64 and arm64; it does not install packages
into the developer's environment or download model weights.

PyTorch and torchvision are resolved only from the official CPU wheel index.
This retains Docling's layout, table and optional OCR processing without bundling
NVIDIA's proprietary CUDA libraries. GPU deployment needs a separate dependency
and license review.

After reviewing an update, regenerate both files from the repository root:

```sh
uv lock --project docker/odl-hybrid
uv export --project docker/odl-hybrid --frozen --no-dev --no-emit-project \
  --emit-index-url --format requirements-txt \
  --output-file docker/odl-hybrid/requirements.txt
```

Audit the exported versions before rebuilding the image. Installing with
`pip --require-hashes` ensures Docker uses those reviewed artifacts.
