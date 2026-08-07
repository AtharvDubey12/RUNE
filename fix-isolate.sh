#!/usr/bin/env bash
# ==============================================================================
# RUNE Isolate Sandbox Fixer for Debian / WSL2
# Fixes: "User isolate not found in /etc/subuid" and cgroup initialization issues.
# ==============================================================================
# This is an optional script, only run this if RUNE Core responds with {"error":"Sandbox initialization failed"}
# chmod +x fix-isolate.sh 
# sudo ./fix-isolate.sh
set -e

if [ "$EUID" -ne 0 ]; then
    echo "[-] Error: Please run this script with sudo:"
    echo "    sudo ./fix-isolate.sh"
    exit 1
fi

echo "[*] Checking and fixing Isolate sandbox configurations..."

# 1. Create the isolate system user if it doesn't already exist
if ! id -u isolate >/dev/null 2>&1; then
    useradd -r isolate
    echo "[+] Created 'isolate' system user."
else
    echo "[*] 'isolate' system user already exists."
fi

# 2. Ensure subuid and subgid configuration files exist
touch /etc/subuid /etc/subgid

# 3. Inject subordinate UID and GID ranges for the isolate user if missing
if ! grep -q "^isolate:" /etc/subuid; then
    echo "isolate:100000:65536" >> /etc/subuid
    echo "[+] Added UID mapping range for isolate in /etc/subuid"
fi

if ! grep -q "^isolate:" /etc/subgid; then
    echo "isolate:100000:65536" >> /etc/subgid
    echo "[+] Added GID mapping range for isolate in /etc/subgid"
fi

# 4. Inject namespace mapping for root if missing
if ! grep -q "^root:" /etc/subuid; then
    echo "root:100000:65536" >> /etc/subuid
    echo "[+] Added UID mapping range for root in /etc/subuid"
fi

if ! grep -q "^root:" /etc/subgid; then
    echo "root:100000:65536" >> /etc/subgid
    echo "[+] Added GID mapping range for root in /etc/subgid"
fi

# 5. Update Isolate configuration for raw cgroup path if needed
if [ -f "/usr/local/etc/isolate" ]; then
    if grep -q "cg_root = auto:" /usr/local/etc/isolate; then
        sed -i 's|cg_root = auto:.*|cg_root = /sys/fs/cgroup|g' /usr/local/etc/isolate
        echo "[+] Updated isolate cg_root configuration for WSL2 compatibility."
    fi
fi

echo "[*] Isolate configuration complete!"
echo "[*] Verifying sandbox initialization..."
sudo isolate --cg --init --box-id=0
sudo isolate --cg --cleanup --box-id=0
echo "[+] Sandbox check passed successfully. RUNE is ready to run!"