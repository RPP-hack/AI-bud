"""Run with the Python server/dev extras installed and Go >= 1.27 on PATH."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile

root = Path(__file__).resolve().parents[1]
module = root / "backend/services/api"
go = os.environ.get("GO", "go")
with tempfile.TemporaryDirectory(prefix="rag-protoc-") as tool_dir:
    env = dict(os.environ, GOBIN=tool_dir)
    for package in ("google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10",
                    "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1"):
        subprocess.run([go, "install", package], cwd=module, env=env, check=True)
    env["PATH"] = tool_dir + os.pathsep + env.get("PATH", "")
    subprocess.run([
        sys.executable, "-m", "grpc_tools.protoc", "-I" + str(root / "contracts/proto"),
        "--python_out=" + str(root / "rag/src"), "--pyi_out=" + str(root / "rag/src"),
        "--grpc_python_out=" + str(root / "rag/src"),
        "--go_out=" + str(module), "--go_opt=module=ai-bud/services/api",
        "--go-grpc_out=" + str(module), "--go-grpc_opt=module=ai-bud/services/api",
        str(root / "contracts/proto/rag/v1/rag.proto"),
    ], env=env, check=True)
