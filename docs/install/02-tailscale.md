# 2. Tailscale (optional, for remote access)

Skip this step if you only watch at home. TVs never use Tailscale; they connect over the LAN.

Remote access goes through Tailscale on the **server's host**, not through the stack. Traffic between
your devices in a tailnet is encrypted by Tailscale, so the stack's plain HTTP ports are used as they are.

1. Install Tailscale on the server and sign it in to your tailnet (Tailscale's own install guide for your OS).
   If the server already runs Tailscale for something else, use that.
2. In the Tailscale admin console, note the server's machine name. With MagicDNS on (the default for new
   tailnets) your devices reach it by that name, for example `pi`.
3. Install Tailscale on the phone or laptop you use away from home and sign in to the same tailnet. On a phone,
   turn on **VPN On Demand** (iPhone) or **Always-on VPN** (Android), so it is connected whenever you open Lampa.
4. Use the server's tailnet name in the household address: `http://<machine-name>:<GATEWAY_PORT>/h/<token>/`.
   It works at home too, as long as Tailscale is on.

A LAN address (`http://192.168.x.x:…`) works away from home only if the server's Tailscale advertises your LAN as
a subnet route (`--advertise-routes=192.168.x.0/24`, approved in the admin console). An exit node does not help.

Nothing is exposed to the public internet: Tailscale Funnel stays off.
