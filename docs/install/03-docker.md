# 3. Docker

If the server already runs Docker (for example with Portainer), only check the versions:

```bash
docker version
```

```bash
docker compose version
```

`docker compose` (Compose v2, with a space) must be available; the stack uses Compose profiles.

If Docker is not installed, follow Docker's official instructions for your distribution, for example
[Install Docker Engine on Debian](https://docs.docker.com/engine/install/debian/) (Raspberry Pi OS 64-bit is
Debian-based). The Compose v2 plugin is part of that installation.

## Portainer (optional)

The stack can be deployed as a Portainer CE Git stack instead of from the command line. Confirmed on the
reference Raspberry Pi with Portainer CE 2.45.3 (it embeds Docker Compose 2.40.3): it builds the images
(`build:`) on the server before starting the stack, renders `configs` with inline `content`, substitutes
`${VAR}` from the stack's variables, and reads `COMPOSE_PROFILES` from them.

1. For a private repository, create a GitHub fine-grained token: **Settings → Developer settings → Personal
   access tokens → Fine-grained tokens**, repository access *only this repository*, permission **Contents:
   Read-only**. Keep it for step 3.
2. Generate the stack's secrets on your computer, for example `sh scripts/gen-env.sh secrets/server.env`, and
   adjust ports, `PROWLARR_BIND` and `TZ` in it.
3. In Portainer: **Stacks → Add stack**. Name `media-stack`. Build method **Repository**:
   * Repository URL: your repository's HTTPS URL; reference `refs/heads/main`; compose path `compose.yaml`.
   * Authentication: on; your GitHub username and the token from step 1.
   * Environment variables: **Load variables from .env file**, and choose the file from step 2.
   * Optional features: add a variable `COMPOSE_PROFILES` with a comma-separated list, for example
     `flaresolverr` (see [step 5](05-clone-and-configure.md)). It can also be a line in the file from step 2.
4. **Deploy the stack**. The first deployment builds the gateway image on the server; this takes a few minutes
   on a Raspberry Pi. On its first start the gateway downloads the pinned Lampa build and shows as
   *unhealthy* until it has; if that takes long, see
   [troubleshooting](../troubleshooting.md#the-gateway-stays-unhealthy-on-first-start-its-log-ends-with-downloading-lampa-build).

Updates: after new commits, use the stack's **Pull and redeploy**, or enable Portainer's automatic updates for
the stack.
