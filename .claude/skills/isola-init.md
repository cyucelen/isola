# isola-init

Set up isola in a project for multi-branch development with automatic port allocation.

## When to Use

- User wants to set up isola in their project
- User asks about configuring `.isola.toml`
- User wants to run multiple branches simultaneously

## Instructions

1. **Check if isola is installed:**
   ```bash
   isola version
   ```
   If not installed, suggest: `brew install cyucelen/tap/isola` (macOS) or download from GitHub releases.

2. **Create `.isola.toml` in the project root:**

   Basic structure:
   ```toml
   [services.SERVICE_NAME]
   command = "COMMAND_TO_START"
   port_env = "PORT"  # environment variable for port
   port_range = [START, END]  # optional, default 3100-3199
   ```

3. **Common configurations:**

   **Node.js/React/Vite:**
   ```toml
   [services.frontend]
   command = "npm run dev"
   port_env = "PORT"
   port_range = [3100, 3199]
   ```

   **Go:**
   ```toml
   [services.api]
   command = "go run ."
   port_env = "PORT"
   port_range = [8100, 8199]
   ```

   **Python/Django:**
   ```toml
   [services.web]
   command = "python manage.py runserver 0.0.0.0:$PORT"
   port_env = "PORT"
   ```

   **Multiple services:**
   ```toml
   [services.frontend]
   command = "npm run dev"
   port_env = "PORT"
   port_range = [3100, 3199]

   [services.backend]
   command = "go run ./cmd/server"
   port_env = "PORT"
   port_range = [8100, 8199]
   ```

4. **Environment variables provided by isola:**
   - `PORT` - Assigned port for this service
   - `ISOLA_BRANCH` - Current branch name
   - `ISOLA_BRANCH_SLUG` - URL-safe branch name (e.g., `feature-auth`)
   - `ISOLA_SERVICE` - Service name
   - `ISOLA_{SERVICE}_PORT` - Port of another service (for cross-service communication)
   - `ISOLA_{SERVICE}_URL` - Proxy URL of another service

5. **Verify setup:**
   ```bash
   isola doctor
   ```

## Example Interaction

User: "I want to set up isola for my Next.js + Express project"

Response: Create `.isola.toml`:
```toml
[services.frontend]
command = "npm run dev"
port_env = "PORT"
port_range = [3100, 3199]

[services.api]
command = "npm run start:api"
port_env = "PORT"
port_range = [8100, 8199]
env = { API_URL = "http://$ISOLA_BRANCH_SLUG.localhost:8000" }
```
