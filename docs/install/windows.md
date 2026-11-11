# Windows 11 with Docker Desktop

The stack runs on a Windows 11 PC through Docker Desktop's WSL 2 backend, the same images as on Linux. Use it
to try the project, or as the server for a household whose PC stays on. Follow the main guide; this page lists
only what differs on Windows.

Measured on 2026-11-11 (Windows 11 Pro, Docker Desktop with WSL 2): **5 minutes from `git clone` to a film
playing in the browser**, with every image downloaded fresh (about 3.5 GB) and the gateway built. Add about 5
minutes for a TMDB key (step 4) and the time to read the steps.

## What you need

* Windows 11 with virtualisation on (Task Manager → Performance → CPU shows "Virtualization: Enabled").
* [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) with the **WSL 2** backend
  (the installer's default). Give it at least 4 GB of memory, 8 GB for two streams at once (Docker Desktop →
  Settings → Resources, or WSL's own limit in `%UserProfile%\.wslconfig`).
* [Git for Windows](https://git-scm.com/download/win). Its **Git Bash** runs the project's shell scripts
  (`gen-env.sh`, `backup.sh`, `restore.sh`) and has `openssl`.

## Steps

Run the commands in **Git Bash**.

1. Steps 1–4 of the main guide: skip the Linux and Docker Engine parts; get the TMDB key.
2. [Step 5](05-clone-and-configure.md): `git clone`, then `sh scripts/gen-env.sh` and add `TMDB_API_KEY` to
   `.env`. The repository's `.gitattributes` keeps Unix line endings on Windows, which the scripts and configs
   need; do not convert them.
3. [Step 6](06-first-run.md): `docker compose up -d --build`. Open `http://localhost:8080/h/<token>/`.
4. [Step 7](07-prowlarr-indexers.md): Prowlarr is at `http://127.0.0.1:9696/` on this PC.
5. [Step 8](08-connect-clients.md): other devices use the PC's LAN address. The first time a port is opened,
   Windows Firewall may ask to allow Docker Desktop: allow it on **private** networks only. LAN access from a TV
   to a Windows host is unverified.

Backup and restore ([step 10](10-backup-restore.md)) work the same in Git Bash; checked on this setup.

## Windows specifics

* **Sleep stops the server.** Set Windows to never sleep while plugged in (Settings → System → Power) if other
  devices use it.
* **Docker Desktop starts with your Windows session**, not before it: after a reboot, sign in (or enable
  "Start Docker Desktop when you sign in") before the stack is reachable. The containers restart by themselves
  (`restart: unless-stopped`).
* **A published port can vanish after Docker Desktop restarts** (seen with Lampac's 9118 and Prowlarr's 9696):
  `docker compose restart prowlarr lampac` brings them back.
* **Monitoring (step 9):** Alloy runs, but its host metrics describe Docker Desktop's Linux VM, not Windows. For
  the PC itself use windows_exporter as in step 9's "Add a Windows PC".
* **The VPN profile** (gluetun) works on Docker Desktop (tested).
