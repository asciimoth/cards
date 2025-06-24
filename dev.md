# Dev env
Dev environment defined in `flake.nix`
Use `nix develop` or `direnv allow` to enter dev env.

# Setup
- Enter dev env
- Run `go get` to fetch all dependencies

# Running service
```sh
docker compose up --build
```

# Env vars
All service configuration are implemented with env vars.
There should be `.env` file in repo root.
Check `.env.example` for template.
