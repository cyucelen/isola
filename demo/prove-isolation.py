#!/usr/bin/env python3
"""Prove worktree database isolation using a disposable Postgres fixture."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.request
import uuid


SERVER = '''import http.server, json, os, subprocess
from urllib.parse import urlparse

database = urlparse(os.environ["DATABASE_URL"]).path.lstrip("/")

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        rows = subprocess.check_output([
            "docker", "exec", os.environ["DEMO_PG_CONTAINER"],
            "psql", "-U", "isola", "-d", database, "-At", "-v", "ON_ERROR_STOP=1",
            "-c", "SELECT count(*) FROM todos"
        ], text=True).strip()
        body = json.dumps({"worktree": os.environ["ISOLA_BRANCH"],
                           "database": database, "todos": int(rows)}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

http.server.HTTPServer(("127.0.0.1", int(os.environ["PORT"])), Handler).serve_forever()
'''


def run(*args, cwd=None, timeout=120):
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--isola", default="isola", help="Path to the isola binary")
    args = parser.parse_args()
    binary = shutil.which(args.isola)
    require(binary, "Install isola or pass --isola /path/to/isola")
    binary = str(Path(binary).resolve())
    for dependency in ("git", "docker", "python3"):
        require(shutil.which(dependency), f"Missing prerequisite: {dependency}")
    run("docker", "info", "--format", "{{.ServerVersion}}")
    container = "isola-proof-" + uuid.uuid4().hex[:12]
    with tempfile.TemporaryDirectory(prefix="isola-proof-") as temporary:
        root = Path(temporary)
        repo = root / "project"
        feature = root / "feature"
        repo.mkdir()
        try:
            print("Starting disposable Postgres (Docker is only the demo fixture)...", flush=True)
            run("docker", "run", "-d", "--rm", "--name", container,
                "-e", "POSTGRES_USER=isola", "-e", "POSTGRES_PASSWORD=demo-only",
                "-p", "127.0.0.1::5432", "postgres:16", timeout=300)
            for _ in range(60):
                ready = subprocess.run(["docker", "exec", container, "pg_isready", "-U", "isola"],
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
                if ready.returncode == 0:
                    break
                time.sleep(1)
            else:
                raise RuntimeError("Demo Postgres did not become ready")
            port = run("docker", "port", container, "5432/tcp").rsplit(":", 1)[1]

            def sql(database, query):
                return run("docker", "exec", container, "psql", "-U", "isola",
                           "-d", database, "-At", "-v", "ON_ERROR_STOP=1", "-c", query)

            sql("isola", "CREATE DATABASE proof_template")
            sql("proof_template", "CREATE TABLE todos (title text); INSERT INTO todos VALUES ('ship'), ('test'), ('review')")
            run("git", "-c", "core.hooksPath=/dev/null", "init", "-b", "main", str(repo))
            run("git", "config", "core.hooksPath", "/dev/null", cwd=repo)
            (repo / "server.py").write_text(SERVER)
            (repo / ".gitignore").write_text(".isola/\n")
            (repo / ".isola.toml").write_text('''project = "isola-proof"
[proxy]
enabled = false
[services.api]
command = "python3 server.py"
port_range = { min = 18000, max = 18999 }
[services.api.env]
DATABASE_URL = "${accessories.database.url}"
DEMO_PG_CONTAINER = "''' + container + '''"
[accessories.database]
kind = "postgres"
server_url = "postgres://isola:demo-only@127.0.0.1:''' + port + '''/isola?sslmode=disable"
clone_from = "proof_template"
name = "proof_${ISOLA_BRANCH_SLUG}"
''')
            run("git", "add", ".", cwd=repo)
            run("git", "-c", "user.name=Isola Demo", "-c", "user.email=demo@example.invalid",
                "-c", "commit.gpgsign=false", "commit", "-m", "Disposable isolation demo", cwd=repo)
            run("git", "worktree", "add", "-b", "feature", str(feature), cwd=repo)
            for worktree in (repo, feature):
                print(f"\n{worktree.name}> isola up", flush=True)
                print(run(binary, "up", cwd=worktree), flush=True)
            services = json.loads(run(binary, "ls", "--json", cwd=repo))
            ports = {entry["worktree"]: entry["port"] for entry in services}
            require(ports["main"] != ports["feature"], "Worktrees shared a service port")
            print("\nService ports: " + json.dumps(ports), flush=True)
            http = urllib.request.build_opener(urllib.request.ProxyHandler({}))

            def read(branch):
                for attempt in range(30):
                    try:
                        with http.open(f"http://127.0.0.1:{ports[branch]}", timeout=5) as response:
                            return json.load(response)
                    except OSError:
                        if attempt == 29:
                            raise
                        time.sleep(0.2)

            before = {branch: read(branch) for branch in ("main", "feature")}
            require(all(value["todos"] == 3 for value in before.values()), "Template was not copied")
            require(before["main"]["database"] != before["feature"]["database"], "Worktrees shared a database")
            print("\nBefore: " + json.dumps(before), flush=True)
            print("\nDelete every todo in feature's database.", flush=True)
            sql("proof_feature", "DELETE FROM todos")
            after = {branch: read(branch) for branch in ("main", "feature")}
            print("After:  " + json.dumps(after), flush=True)
            require(after["main"]["todos"] == 3 and after["feature"]["todos"] == 0,
                    "Database isolation proof failed")
            require(sql("proof_template", "SELECT count(*) FROM todos") == "3", "Template changed")
            print("\nPASS: separate service ports; feature has 0 todos; main and template still have 3.", flush=True)
            for worktree in (feature, repo):
                run(binary, "destroy", cwd=worktree)
            require(sql("isola", "SELECT count(*) FROM pg_database WHERE datname IN ('proof_main', 'proof_feature')") == "0",
                    "isola destroy did not drop both worktree databases")
            require(sql("proof_template", "SELECT count(*) FROM todos") == "3", "Teardown changed the template")
            print("PASS: isola destroy dropped both worktree databases and preserved the template.", flush=True)
        finally:
            try:
                for worktree in (feature, repo):
                    if (worktree / ".isola.toml").exists():
                        try:
                            subprocess.run([binary, "down"], cwd=worktree, timeout=60,
                                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                        except subprocess.TimeoutExpired:
                            print(f"Cleanup timed out for {worktree}; inspect isola processes.", flush=True)
            finally:
                subprocess.run(["docker", "rm", "-f", container], timeout=30,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
