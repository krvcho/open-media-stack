# 1. Prerequisites

## Server

* A Raspberry Pi 5 or an amd64 Linux host. See [requirements](../requirements.md) for the tiers (numbers are
  pending measurement).
* Linux with Docker support (Raspberry Pi OS 64-bit, Debian, Ubuntu, …). arm64 or amd64.
* A wired LAN connection is recommended for streaming to TVs.
* If the server already runs other Docker stacks: this stack uses its own Compose project, networks and
  volumes, and configurable host ports. It does not touch other stacks.
* **Raspberry Pi OS: enable the memory cgroup.** The Pi boots with `cgroup_disable=memory`, and Docker then
  ignores every container's memory limit (`docker info` warns `No memory limit support`). Append
  `cgroup_enable=memory` to the single line in `/boot/firmware/cmdline.txt` and reboot:

  ```bash
  sudo cp /boot/firmware/cmdline.txt /boot/firmware/cmdline.txt.bak
  ```

  ```bash
  sudo sed -i '1 s/$/ cgroup_enable=memory/' /boot/firmware/cmdline.txt
  ```

  ```bash
  sudo reboot
  ```

  After the reboot, `cat /sys/fs/cgroup/cgroup.controllers` lists `memory`, and the `docker info` warning is
  gone.

## Accounts and keys

* A TMDB account for the API key ([step 4](04-tmdb-key.md)). **Required.**
* Accounts for any private or semi-private indexers you want to use. The project ships no indexers.
* Optional: a Tailscale account for remote access ([step 2](02-tailscale.md)).

## Clients

* The devices you want to watch on: Android TV, Samsung (Tizen), LG (webOS), a desktop browser, an iPhone.
* For an iPhone: an external player app that supports URL schemes (for example VLC, Infuse, nPlayer); see
  [step 8](08-connect-clients.md).
* TVs must be on the same LAN as the server.

## Time

About an hour for the first install, most of it waiting for image downloads.
