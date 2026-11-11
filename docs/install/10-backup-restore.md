# 10. Backup and restore

Two scripts in `scripts/` save and bring back everything the stack cannot download again:

| Volume | What it holds |
|---|---|
| `gateway-data` | profiles, the gateway's cached Lampa build |
| `prowlarr-config` | search sources and **any account details you entered** |
| `lampac-database` | watch history, resume positions and bookmarks of every profile |
| `torrserver-config` | TorrServer's settings and account |

Plus your `.env` (the household token and the stack's keys). Search results, TorrServer's cache, metrics and
logs are not backed up; the monitoring stack (step 9) rebuilds its dashboards from the repository.

**The archive holds secrets.** Keep it encrypted (`gpg -c <archive>`) and away from shared folders. `backups/` is
git-ignored.

Time: under a minute for the backup, a few minutes for a restore.

## Back up

On the server, in the repository folder:

```bash
sh scripts/backup.sh
```

The stack's containers stop for a few seconds and start again. The archive lands in `backups/` as
`media-stack-backup-<date>-<time>.tar`.

With Portainer the stack's variables live in Portainer, not in a file: copy them into a file first and point the
script at it, for example `ENV_FILE=~/media-stack.env sh scripts/backup.sh`. Without it the archive has the
volumes only.

To back up every night, add a cron job on the server, for example `crontab -e`:

```
30 3 * * * cd /path/to/media-stack && sh scripts/backup.sh >> backups/backup.log 2>&1
```

## Restore

On the same server, or on a new one after [step 3](03-docker.md) and `git clone`:

```bash
sh scripts/restore.sh backups/media-stack-backup-<date>-<time>.tar
```

The script checks the whole archive before it changes anything, then replaces the content of the four volumes
(creating them if needed) and restarts what was running. A `.env` already in the folder is left alone; the
archived one is written next to it as `.env.restored`. Then start the stack as in [step 6](06-first-run.md)
(`docker compose up -d`, or deploy the stack in Portainer with the restored variables).

Check: the profiles appear on "Who's watching?", a card you watched shows its progress, and Prowlarr lists your
indexers.

Restore drill on the reference setup (2026-11-11): back up, delete the volumes, restore, start: 40 seconds, every
volume's content identical. CI repeats the drill on every change.
