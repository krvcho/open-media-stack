# Set it up with Claude Code

If you have [Claude Code](https://docs.claude.com/en/docs/claude-code/overview), it can do the setup with you: it
checks your machine, runs the commands, explains each step and asks you whenever a choice is yours. You still
make every decision and type every password yourself.

1. Install Claude Code on the machine that will be the server (or one that can reach it over SSH).
2. Open a terminal in an empty folder and start `claude`.
3. Paste the prompt below. Answer its questions; it works through [Getting started](getting-started.md).

Tell it if you prefer another language; it will switch.

## The prompt

```text
Set up Open Media Stack on this machine for me, from https://github.com/krvcho/open-media-stack.
Follow docs/getting-started.md from that repository step by step, using docs/install/ for details.

How to work with me:
- Ask me one question at a time, in plain words, and say why you need the answer. Talk to me in the
  language I write in.
- Before each step, tell me what you are about to do and why; after it, run the step's "Check" and show
  me the result. Do not move on while a check fails: find the cause first (docs/troubleshooting.md).
- Ask me before anything that changes this machine outside the project folder: installing software,
  editing system files, firewall or router settings, rebooting.

What to find out from me:
1. Which machine this is and its operating system; whether Docker and Git are installed (check them
   yourself first). The server's LAN address, and whether my router reserves it.
2. My time zone. Whether I have a TMDB API key (optional; I paste it into .env myself or give it to you
   to put there).
3. Whether the streaming engine's peer-to-peer traffic should go through a VPN. Explain it as a privacy
   choice: the engine never uploads, but other peers see the IP address it connects from. If yes, I give you the path to my provider's WireGuard configuration file; you copy its
   fields into .env.
4. Whether I want online sources (ONLINE_SOURCES=1) and the optional monitoring stack.
5. Which devices I will watch on (browser, Android TV, Samsung, LG, iPhone), to tailor step 8.

Rules:
- Never print secrets in the chat: the household token, keys, passwords, the WireGuard private key.
  When I need the household address, write it to a file in the project folder and tell me the file.
- Never commit or push anything, and never change files in the repository other than .env.
- When I reach the indexer step, explain how to choose (Prowlarr's Language and Privacy filters, Test, the rules of each
  site, the law where I live) and let me pick and add them. I type every
  account password there; you never ask for it. Afterwards you may check through Prowlarr's API, with the
  key in .env, that at least one indexer exists and tests fine, and run a test search through the gateway.
- Never turn on uploading (keep TORRSERVER_UPLOAD_KB=0) and never expose any port to the internet. For
  remote access, suggest Tailscale as in docs/install/02-tailscale.md.

Finish with the "You are done when" checklist of docs/getting-started.md, item by item, and offer to make a
first backup with scripts/backup.sh.
```

## What it can and cannot do

* It can check and install prerequisites (with your permission), create `.env`, start the stack, read the logs,
  test search and streaming from the server, and fix what the troubleshooting page covers.
* It cannot touch your TV or phone: it tells you what to type there. It cannot choose sources for you, and it does
  not see your passwords.
* It works on the copy of the project on your machine; nothing is sent back to this repository.
