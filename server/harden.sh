#!/usr/bin/env bash
# Optional lock-down for a Boost Link server. Run as root after install.sh:
#   bash harden.sh
# It turns on the firewall with only the Boost Link ports and SSH open, and
# installs security updates automatically. If root already logs in with an
# SSH key, it also turns off password logins (bots try passwords all day).
# Safe to run again.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "Run as root (sudo bash harden.sh)"; exit 1; }
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq ufw unattended-upgrades fail2ban >/dev/null

# Security updates install themselves every day.
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
CONF

# Firewall: SSH plus the Boost Link ports, nothing else.
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow 22/tcp >/dev/null
ufw allow 28090/tcp >/dev/null
ufw allow 28090/udp >/dev/null
ufw allow 3478:3479/udp >/dev/null
ufw allow 3480/udp >/dev/null
ufw allow 49160:49999/udp >/dev/null
ufw --force enable >/dev/null
ufw status verbose

# fail2ban blocks addresses that keep guessing SSH passwords (its default jail).
systemctl enable --now fail2ban >/dev/null 2>&1 || true

# Password logins off, but only when a key is already set up, so you can't lock yourself out.
if [ -s /root/.ssh/authorized_keys ]; then
  cat > /etc/ssh/sshd_config.d/10-boost-link.conf <<'CONF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
CONF
  sshd -t && (systemctl reload ssh 2>/dev/null || systemctl reload sshd)
  echo "SSH: key logins only."
else
  echo "SSH: no key in /root/.ssh/authorized_keys, so password logins stay on."
  echo "     Add a key from your PC (see README, step 4b), then run this again."
fi
echo "Done."
