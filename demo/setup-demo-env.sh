#!/bin/bash
# Setup script for isola demo environment
# This creates a temporary project with the necessary structure for recording demos

set -e

DEMO_DIR="${1:-/tmp/isola-demo}"
ISOLA_BIN="${2:-$(pwd)/isola}"

echo "Setting up demo environment in: $DEMO_DIR"
echo "Using isola binary: $ISOLA_BIN"

# Clean up existing demo directory
rm -rf "$DEMO_DIR"
mkdir -p "$DEMO_DIR"
cd "$DEMO_DIR"

# Initialize git repository
git init
git config user.email "demo@example.com"
git config user.name "Demo User"

# Create a simple project structure
mkdir -p frontend backend

# Create a simple frontend (using Python http.server as mock)
cat > frontend/server.py << 'EOF'
import http.server
import socketserver
import os

PORT = int(os.environ.get('PORT', 3100))
BRANCH = os.environ.get('ISOLA_BRANCH', 'unknown')

class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-type', 'text/html')
        self.end_headers()
        response = f"""
        <html>
        <head><title>Frontend - {BRANCH}</title></head>
        <body style="font-family: system-ui; padding: 40px; background: #1a1a2e; color: #eee;">
            <h1>🌳 isola Demo Frontend</h1>
            <p>Branch: <strong>{BRANCH}</strong></p>
            <p>Port: <strong>{PORT}</strong></p>
        </body>
        </html>
        """
        self.wfile.write(response.encode())

with socketserver.TCPServer(("", PORT), Handler) as httpd:
    print(f"Frontend serving on port {PORT} (branch: {BRANCH})")
    httpd.serve_forever()
EOF

# Create a simple backend (using Python http.server as mock)
cat > backend/server.py << 'EOF'
import http.server
import socketserver
import os
import json

PORT = int(os.environ.get('PORT', 8100))
BRANCH = os.environ.get('ISOLA_BRANCH', 'unknown')

class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        response = json.dumps({
            "service": "backend",
            "branch": BRANCH,
            "port": PORT,
            "status": "ok"
        }, indent=2)
        self.wfile.write(response.encode())

with socketserver.TCPServer(("", PORT), Handler) as httpd:
    print(f"Backend API serving on port {PORT} (branch: {BRANCH})")
    httpd.serve_forever()
EOF

# Start a throwaway Postgres so the demo can show per-worktree database
# isolation. It is seeded with a template database (myapp_dev) that isola
# clones for each worktree.
echo "Starting demo Postgres (Docker)..."
docker rm -f isola-demo-pg >/dev/null 2>&1 || true
docker run -d --name isola-demo-pg \
	-e POSTGRES_USER=isola -e POSTGRES_PASSWORD=isola -e POSTGRES_DB=isola \
	-p 55432:5432 postgres:16 >/dev/null
for i in $(seq 1 30); do
	docker exec isola-demo-pg pg_isready -U isola >/dev/null 2>&1 && break
	sleep 1
done
docker exec -e PGPASSWORD=isola isola-demo-pg psql -U isola -d isola -v ON_ERROR_STOP=1 \
	-c "CREATE DATABASE myapp_dev" >/dev/null
docker exec -e PGPASSWORD=isola isola-demo-pg psql -U isola -d myapp_dev -v ON_ERROR_STOP=1 \
	-c "CREATE TABLE todos (id serial primary key, title text); INSERT INTO todos (title) VALUES ('write docs'), ('ship isola'), ('demo accessories')" >/dev/null
echo "Seeded template database: myapp_dev"

# Create .isola.toml configuration
cat > .isola.toml << 'EOF'
# isola configuration for demo project

[services.frontend]
command = "python3 server.py"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.backend]
command = "python3 server.py"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000

[env]
DEMO_MODE = "true"

# Per-worktree database: each worktree gets its own copy of the myapp_dev
# template, injected into services as DATABASE_URL.
[accessories.database]
kind       = "postgres"
server_url = "postgres://isola:isola@localhost:55432/isola"
clone_from = "myapp_dev"
name       = "myapp_${ISOLA_BRANCH_SLUG}"
inject     = "DATABASE_URL"
EOF

# Initial commit
git add .
git commit -m "Initial commit: demo project setup"

# Create a feature branch (for worktree demo)
git branch feature/new-api

# Add isola to PATH for this session
export PATH="$(dirname "$ISOLA_BIN"):$PATH"

echo ""
echo "✅ Demo environment ready!"
echo ""
echo "To record demos:"
echo "  cd $DEMO_DIR"
echo "  export PATH=\"$(dirname "$ISOLA_BIN"):\$PATH\""
echo ""
echo "Then run VHS:"
echo "  vhs /path/to/demo-workflow.tape"
echo "  vhs /path/to/demo-tui.tape"
echo ""
