# Boost Link server: going live in about 15 minutes

The server only does matchmaking (plus a relay for players whose routers can't connect
directly). Fights still go player to player, so a $5/month server is plenty.
No game files go on the server.

## 1. Rent a server (5 minutes)

Any provider with a $5-6 Linux VPS works: Vultr, DigitalOcean, Linode/Akamai, Hetzner.

- **Image:** Ubuntu 24.04 LTS.
- **Size:** the cheapest (1 CPU, 1 GB RAM is more than enough).
- **Location:** the middle of your players. Mostly US East: New York / New Jersey / Virginia.
  All of North America: Chicago or Dallas.
- **Login:** choose a root password (or an SSH key if you have one).

Write down the server's **IP address**.

## 2. Copy this folder to the server (2 minutes)

Windows has the tools built in. Open **PowerShell** in the folder that contains
`boost-link-server` and run (replace the IP):

```
scp -r boost-link-server root@203.0.113.50:
```

Type `yes` the first time, then the root password.

## 3. Install (1 minute)

```
ssh root@203.0.113.50
bash boost-link-server/install.sh
```

It installs to `/opt/boost-link`, starts automatically on boot, restarts itself if it ever
crashes, and prints the IP players use and the status page address.

## 4. Open the ports in the provider's firewall

Most providers have no firewall by default. If yours has one ("Firewall", "Security group"),
allow inbound:

| Port | Protocol | For |
|---|---|---|
| 28090 | TCP | matchmaking + status page |
| 3480 | UDP | relay |
| 49160-49200 | UDP | relay traffic |
| 22 | TCP | SSH (so you can log in) |

Check it works: open `http://<server IP>:28090` in a browser. That's the Boost Link home page
(live queue count, how to join, Discord button); `/status` has the detailed queue and connection results.

## 5. Point everyone at it

Everyone, you included, opens the launcher and picks **Join**, with the server's IP as the
host IP. Nobody needs ZeroTier, Radmin, or port forwarding any more. Nobody needs to be "host".

## Settings

Edit `/opt/boost-link/server.json` (`nano /opt/boost-link/server.json`), then
`systemctl restart boost-link`.

- `discordInvite`: your Discord invite link (https://discord.gg/...), shown as a "Join the Discord" button on the home page.
- `siteTagline`: optional subtitle for the home page.
- `discordWebhook`: paste a Discord webhook link to post when someone starts searching.
- `publicStatusUrl`: e.g. `http://<server IP>:28090/status`, added to the Discord post.
- `rttLimitMs`: raise the 300 ms ping limit (e.g. 450) to let far-apart players match.
- `roomSize`: 2 now. 4 for 2v2 once we've tested it.

## Handy commands

| What | Command |
|---|---|
| Live log | `journalctl -u boost-link -f` |
| Restart | `systemctl restart boost-link` |
| Stop | `systemctl stop boost-link` |
| Update | copy a new `boost-link-server` folder over (step 2), then `bash boost-link-server/install.sh` (keeps your settings) |

## Notes

- The status page and Discord posts never show IPs or serials.
- The relay password is handed to players' launchers automatically. It's random each restart.
- If the server's public IP isn't found at start (log says "relay disabled"), set
  `"relayIP": "<server IP>"` under `localTurn` in server.json and restart.

## Using a domain name

Add an A record (for example `play`) pointing at the server IP. On Cloudflare, set it to
**DNS only (grey cloud)**: the orange-cloud proxy only passes web traffic and breaks matchmaking.
Tip: forward the home page to port 80 with `iptables -t nat -A PREROUTING -p tcp --dport 80 -j REDIRECT --to-port 28090` if you want `http://play.yourdomain.com` without `:28090` (not saved across reboots unless you install iptables-persistent).
