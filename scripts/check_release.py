import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
API = ROOT / 'backend/services/api'
GO = os.environ.get('GO', shutil.which('go') or 'go')
environment = dict(os.environ)
environment['PATH'] = str(Path(GO).resolve().parent) + os.pathsep + environment.get('PATH', '')
if not all(environment.get(name) for name in ('TEST_DATABASE_URL', 'RAG_TEST_DATABASE_URL')):
    raise SystemExit('Set TEST_DATABASE_URL and RAG_TEST_DATABASE_URL to a disposable-test PostgreSQL/pgvector admin connection with CREATEDB.')


def run(args, cwd=ROOT, **kwargs):
    print('+', ' '.join(map(str, args)), flush=True)
    return subprocess.run(args, cwd=cwd, env=environment, check=True, **kwargs)


formatted = subprocess.check_output([str(Path(GO).resolve().parent / 'gofmt'), '-l', '.'], cwd=API, env=environment, text=True)
if formatted.strip():
    raise SystemExit('Run gofmt on: ' + formatted)
run([GO, 'vet', '-tags=integration', './...'], API)
packages = subprocess.check_output([GO, 'list', './...'], cwd=API, env=environment, text=True).splitlines()
packages = ','.join(p for p in packages if not p.endswith('/ragclient/ragv1'))
run([GO, 'test', '-race', '-count=1', '-tags=integration', '-coverpkg=' + packages, '-coverprofile=coverage.out', './...'], API)
with (API / 'coverage.out').open('rb') as source, (API / 'coverage.xml').open('wb') as target:
    run([GO, 'run', 'github.com/boumenot/gocover-cobertura@v1.5.0', '-strict', '-ignore-gen-files', '-ignore-non-code-lines'],
        API, stdin=source, stdout=target)
run([sys.executable, '-m', 'coverage', 'run', '-m', 'unittest', 'discover', '-s', 'rag/tests', '-q'])
run([sys.executable, '-m', 'coverage', 'report'])
run([sys.executable, '-m', 'coverage', 'xml'])
run([sys.executable, '-m', 'rag.evaluation', 'run', '--output', 'docs/coverage/offline-evals.json'])
run(['npm', 'run', 'build'], ROOT / 'frontend')
run(['npm', 'run', 'coverage'], ROOT / 'frontend')
run(['node', 'scripts/coverage-report.mjs'], ROOT / 'frontend')
run([sys.executable, 'scripts/coverage_report.py'])
