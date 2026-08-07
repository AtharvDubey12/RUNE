#!/bin/bash

# RUNE Code Execution Engine - Infrastructure Setup Script
# Run with: sudo ./setup.sh (or run as root)
# Note: only run this script on a fresh machine with an active internet connection.

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root (use sudo ./setup.sh)"
  exit
fi


# ==========================================
# Helper Functions
# ==========================================

get_ip() {
    hostname -I | awk '{print $1}'
}

install_go() {
    echo "[*] Installing Go..."
    apt-get update
    apt-get install -y golang-go
    
    if [ -f "go.mod" ]; then
        echo "[*] Downloading Go dependencies..."
        go get github.com/joho/godotenv
        go mod tidy
    else
        echo "[!] go.mod not found. Please run this script from the root of the RUNE project."
    fi
}

install_compilers() {
    echo "[*] Installing Compilers and Interpreters (C++, Java, Python, Node.js)..."
    apt-get update
    apt-get install -y build-essential default-jdk python3 nodejs
}

install_isolate() {
    echo "[*] Installing Isolate..."
    apt-get update
    apt-get install -y git libcap-dev pkg-config make libseccomp-dev libsystemd-dev
    #apt-get install -y git libcap-dev pkg-config make

    if [ ! -d "/usr/local/bin/isolate" ] && [ ! -f "/usr/local/bin/isolate" ]; then
        ORIG_DIR=$(pwd)
        cd /tmp
        rm -rf isolate
        git clone https://github.com/ioi/isolate.git
        cd isolate
        make isolate
        make install
        echo "[*] Isolate installed successfully."
        cd "$ORIG_DIR"
    else
        echo "[*] Isolate is already installed."
    fi
}

generate_env() {
    local ROLE=$1
    echo ""
    echo "--- Environment Configuration (.env) ---"
    
    # Initialize/Clear .env
    echo "# RUNE Configuration - Role: $ROLE" > .env

    # 1. Server Port (API & Monolith)
    if [[ "$ROLE" == "api" || "$ROLE" == "monolith" ]]; then
        read -p "Enter Server PORT [Default: 3000]: " PORT_VAL
        PORT_VAL=${PORT_VAL:-3000}
        echo "PORT=$PORT_VAL" >> .env
    fi

    # 2. Database Connection (All Go layers)
    if [[ "$ROLE" == "api" || "$ROLE" == "core" || "$ROLE" == "monolith" ]]; then
        read -p "Enter PostgreSQL connection string [Default: postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable]: " PG_DSN
        echo "POSTGRES_DSN=${PG_DSN:-postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable}" >> .env
    fi

    # 3. Redis Connection (Cluster layers only)
    if [[ "$ROLE" == "api" || "$ROLE" == "core" ]]; then
        read -p "Enter Redis full connection string [Default: redis://localhost:6379/0]: " REDIS_ADDR_VAL
        echo "REDIS_ADDR=${REDIS_ADDR_VAL:-redis://localhost:6379/0}" >> .env
    fi

    # 4. Hardware Tuning (Core & Monolith execution engines)
    if [[ "$ROLE" == "core" || "$ROLE" == "monolith" ]]; then
        VCPU_COUNT=$(nproc)
        DEFAULT_POLLER=$((VCPU_COUNT * 3))

        echo ""
        echo "[Hardware Tuning] Detected $VCPU_COUNT physical/logical CPU cores."
        
        read -p "Enter LOCAL_QUEUE_CAPACITY (Max jobs node can hold) [Default: 1000]: " LQC_VAL
        echo "LOCAL_QUEUE_CAPACITY=${LQC_VAL:-1000}" >> .env

        read -p "Enter BOX_COUNT (Concurrent sandboxes - map to vCPUs) [Default: $VCPU_COUNT]: " BOX_COUNT_VAL
        echo "BOX_COUNT=${BOX_COUNT_VAL:-$VCPU_COUNT}" >> .env

        if [[ "$ROLE" == "core" ]]; then
            read -p "Enter POLLER_CAPACITY (Max concurrent Redis pulls - 2x/3x of boxes) [Default: $DEFAULT_POLLER]: " POL_VAL
            echo "POLLER_CAPACITY=${POL_VAL:-$DEFAULT_POLLER}" >> .env
        fi
    fi

    if [ ! -z "$SUDO_USER" ]; then
        chown $SUDO_USER:$SUDO_USER .env
    fi

    echo "[*] Configuration successfully saved to .env file!"
    echo ""
}

# ==========================================
# Role Implementations
# ==========================================

setup_db() {
    echo "=== Setting up DB Layer for RUNE (PostgreSQL) ==="
    apt-get update
    apt-get install -y postgresql postgresql-contrib

    echo ""
    read -p "Enter a database username [Default: postgres]: " DB_USER
    DB_USER=${DB_USER:-postgres}
    
    read -p "Enter a secure database password [Default: RUNEpost]: " DB_PASS
    DB_PASS=${DB_PASS:-RUNEpost}
    
    read -p "Enter database name [Default: runedb]: " DB_NAME
    DB_NAME=${DB_NAME:-runedb}

    echo "[*] Configuring PostgreSQL network binding..."
    # Allow external connections
    sed -i "s/#listen_addresses = 'localhost'/listen_addresses = '*'/" /etc/postgresql/*/main/postgresql.conf
    echo "host    all             all             0.0.0.0/0               md5" >> /etc/postgresql/*/main/pg_hba.conf
    
    systemctl restart postgresql

    echo "[*] Creating User, Database, and Tables..."
    
    # Create user and database
    sudo -u postgres psql -c "ALTER USER $DB_USER WITH PASSWORD '$DB_PASS';"
    sudo -u postgres psql -c "CREATE USER $DB_USER WITH PASSWORD '$DB_PASS';" 2>/dev/null || sudo -u postgres psql -c "ALTER USER $DB_USER WITH PASSWORD '$DB_PASS';"
    
    # Inject Schema
    sudo -u postgres psql -d $DB_NAME -c "
    CREATE TABLE IF NOT EXISTS public.submissions (
        token character varying(36) NOT NULL,
        source_code text NOT NULL,
        language_id integer NOT NULL,
        stdin text,
        expected_output text,
        cpu_time_limit double precision DEFAULT 2.0,
        memory_limit double precision DEFAULT 262144.0,
        base64_encoded boolean DEFAULT true,
        status_id integer NOT NULL DEFAULT 1,
        status_description character varying(50) NOT NULL DEFAULT 'In Queue',
        stdout text,
        stderr text,
        compile_output text,
        time double precision DEFAULT 0,
        memory double precision DEFAULT 0,
        created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
        updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
        callback_url text,
        worker_id text,
        CONSTRAINT submissions_pkey PRIMARY KEY (token)
    );
    CREATE INDEX IF NOT EXISTS idx_submissions_status ON public.submissions USING btree (status_id);
    "

    HOST_IP=$(get_ip)
    echo "==========================================================="
    echo "Database Setup Complete"
    echo "PostgreSQL is running and actively listening on port 5432."
    echo ""
    echo "Save this POSTGRES_DSN connection string for your API/Core nodes:"
    echo "postgres://$DB_USER:$DB_PASS@$HOST_IP:5432/$DB_NAME?sslmode=disable"
    echo "==========================================================="
}

setup_redis() {
    echo "=== Setting up REDIS Layer ==="
    apt-get update
    apt-get install -y redis-server
    
    echo ""
    read -p "Enter a secure password for Redis (leave blank for none): " REDIS_PASS
    
    sed -i 's/^bind 127.0.0.1 -::1/bind 0.0.0.0/' /etc/redis/redis.conf
    sed -i 's/^protected-mode yes/protected-mode no/' /etc/redis/redis.conf
    
    if [ ! -z "$REDIS_PASS" ]; then
        if grep -q "^# requirepass" /etc/redis/redis.conf; then
            sed -i "s/^# requirepass foobared/requirepass $REDIS_PASS/" /etc/redis/redis.conf
        else
            echo "requirepass $REDIS_PASS" >> /etc/redis/redis.conf
        fi
        AUTH_STRING=":$REDIS_PASS@"
    else
        AUTH_STRING=""
    fi

    systemctl restart redis-server
    
    HOST_IP=$(get_ip)
    
    echo "==========================================================="
    echo "Redis Setup Complete"
    echo "Redis is globally accessible on port 6379."
    echo ""
    echo "Save this REDIS_ADDR connection string for your API/Core nodes:"
    echo "redis://${AUTH_STRING}${HOST_IP}:6379/0"
    echo "==========================================================="
}

setup_nginx() {
    echo "=== Setting up NGINX Layer ==="
    apt-get update
    apt-get install -y nginx

    echo ""
    read -p "How many API nodes do you want to load balance? (e.g., 2): " NUM_APIS
    
    if ! [[ "$NUM_APIS" =~ ^[0-9]+$ ]] || [ "$NUM_APIS" -lt 1 ]; then
        echo "Invalid input. Defaulting to 1 API node."
        NUM_APIS=1
    fi

    UPSTREAM_SERVERS=""
    for (( i=1; i<=$NUM_APIS; i++ ))
    do
        read -p "Enter the IP and Port of API Node $i (e.g., 10.0.0.$i:3000): " API_NODE
        UPSTREAM_SERVERS="${UPSTREAM_SERVERS}    server ${API_NODE};\n"
    done

    CONFIG_FILE="/etc/nginx/sites-available/rune_cluster"
    
    # Write the NGINX configuration
    cat <<EOF > $CONFIG_FILE
upstream rune_api_cluster {
$(echo -e "$UPSTREAM_SERVERS")
}

server {
    listen 80;
    server_name localhost;

    location / {
        proxy_pass http://rune_api_cluster;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }
}
EOF

    # Enable config and restart NGINX
    ln -sf $CONFIG_FILE /etc/nginx/sites-enabled/
    rm -f /etc/nginx/sites-enabled/default
    systemctl restart nginx

    echo ""
    echo "==========================================================="
    echo "NGINX Setup Complete"
    echo "NGINX is currently running and actively load-balancing (via Round-Robin)."
    echo ""
    echo "--- NGINX Service Commands ---"
    echo "To stop NGINX:    sudo systemctl stop nginx"
    echo "To start NGINX:   sudo systemctl start nginx"
    echo "To restart NGINX: sudo systemctl restart nginx"
    echo "To check status:  sudo systemctl status nginx"
    echo ""
    HOST_IP=$(get_ip)
    echo "Use this URL to send submissions:  http://$HOST_IP/"
    echo "==========================================================="
}

setup_api() {
    echo "=== Setting up RUNE API Layer ==="
    install_go
    generate_env "api"
    
    API_PORT=$(grep "^PORT=" .env | cut -d '=' -f2)
    HOST_IP=$(get_ip)
    
    echo "==========================================================="
    echo "RUNE API Node Setup Complete"
    echo "To run the RUNE Cluster API server, execute:"
    echo "go run cmd/api/main.go"
    echo ""
    echo "Provide this string to your NGINX Load Balancer configuration:"
    echo "$HOST_IP:$API_PORT"
    echo "==========================================================="
}

setup_core() {
    echo "=== Setting up RUNE CORE Layer ==="
    install_go
    install_compilers
    install_isolate
    generate_env "core"
    echo "==========================================================="
    echo "RUNE Core Setup Complete"
    echo "To run the RUNE Worker Node, execute:"
    echo "go run cmd/core/main.go"
    echo "==========================================================="
}

setup_monolith() {
    echo "=== Setting up RUNE Monolith ==="
    install_go
    install_compilers
    install_isolate
    
    # local DB for the monolith, so we prompt if they want to install it locally
    echo "Do you want to install and configure a local PostgreSQL database now [required for async]? (y/n)"
    read -p "Choice: " INSTALL_LOCAL_DB
    if [[ "$INSTALL_LOCAL_DB" == "y" ]]; then
        setup_db
    fi
    
    generate_env "monolith"
    
    echo "==========================================================="
    echo "Monolith Setup Complete"
    echo "To run RUNE as a standalone system, execute:"
    echo "go run cmd/RUNE/main.go"
    echo "==========================================================="
}

# ==========================================
# Main Interactive Menu
# ==========================================

echo "==============================================="
echo "   RUNE Platform Infrastructure Setup Script   "
echo "==============================================="
echo "Choose a role for this machine: "
echo "1) RUNE Cluster: DB Layer (PostgreSQL)"
echo "2) RUNE Cluster: REDIS Layer (Queue)"
echo "3) RUNE Cluster: NGINX Layer (Load Balancer)"
echo "4) RUNE Cluster: API Layer (Web Server)"
echo "5) RUNE Cluster: RUNE CORE Layer (Execution)"
echo "6) RUNE Monolith Core: Single Machine Setup"
echo "==============================================="
read -p "Select an option [1-6]: " OPTION

case $OPTION in
    1) setup_db ;;
    2) setup_redis ;;
    3) setup_nginx ;;
    4) setup_api ;;
    5) setup_core ;;
    6) setup_monolith ;;
    *) echo "Invalid option selected. Exiting." ;;
esac