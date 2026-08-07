# RUNE - Runtime for Untrusted Native Execution

The end-to-end, high-performance code execution engine built for **Algorhythm**.

RUNE is designed as a modern, fast, and lightweight drop-in replacement for Judge0, specifically optimized for algorithmic competitive programming platforms and sandbox evaluation.

---

## Key Features

* **Supported Languages:** C++ (GCC/G++), Java (OpenJDK 21), JavaScript (Node.js), and Python 3.
* **Dual Execution Pipeline:** Synchronous execution (`wait=true`) for instant feedback and asynchronous queue/worker with webhook notifications (`wait=false`) for horizontal scaling.
* **Solo Batch Submissions:** Compile source code once and execute it across multiple `stdin` test cases concurrently.
* **Isolate Sandboxing:** Hardware and Linux kernel-level cgroup isolation for safe untrusted code execution.

---

## System Requirements

* **Operating System:** Linux with modern kernel support (works on latest Ubuntu LTS 26.04).
* **Permissions:** **`sudo` / Root privileges are strictly required** for Isolate to construct cgroups, configure UID/GID mappings, and bind-mount filesystem directories.
* **Core Runtimes:** `gcc`, `g++`, `openjdk-21-jdk`, `python3`, `nodejs`.

---

## Installation & Setup

### 1. Clone the Repository
```bash
git clone https://github.com/AtharvDubey12/RUNE.git
cd RUNE
```

### 2. Run the Initialization Script
Execute the main setup script on a fresh Linux machine. This script installs required compilers (explicitly binding `openjdk-21-jdk`), builds `isolate`, and interactively generates your `.env` configuration.

```bash
chmod +x setup.sh
sudo ./setup.sh
```

### 3. Sandbox Configuration & Fixes (`fix-isolate.sh`)
If running on WSL2 or encountering sandbox mount/cgroup initialization issues, execute the isolate fix script to configure subuids/subgids and cgroup root paths:

```bash
chmod +x fix-isolate.sh
sudo ./fix-isolate.sh
```

### 4. Launch RUNE

> **Crucial:** RUNE **must** be executed with `sudo`. Running without `sudo` will prevent Isolate from managing cgroups, resulting in `Sandbox initialization failed` or permission errors.

* **Standalone Core (Single Node Monolith):**
  ```bash
  sudo go run cmd/RUNE/main.go
  ```

* **API Layer (Cluster Setup):**
  ```bash
  sudo go run cmd/api/main.go
  ```

* **Core Worker Layer (Cluster Setup):**
  ```bash
  sudo go run cmd/core/main.go
  ```

*(To run in the background, append `&`, e.g., `sudo go run cmd/RUNE/main.go &`)*

---

## Customizations & Configuration (`.env`)

1. **`BOX_COUNT` / Container Limits:**  
   Defines the maximum concurrent Isolate sandboxes (concurrent code executions) active at any single moment. Map this according to your available vCPUs/CPU cores.
1. **`POLLER_CAPACITY` / Max Submissions in a Core at any instance of time:**  
   Defines the maximum number of incomplete jobs (submission) a Core can contain within its system at any time.
3. **Database, Redis and PORT:**  
   Default credentials in `.env` (redis connection string, db connection string, port number) should be updated prior to production deployment.

---

## API Reference

### Optional Query Flags

| Flag | Type | Description |
| :--- | :--- | :--- |
| `wait` | `boolean` | `true`: Synchronous mode (holds connection until execution finishes).<br>`false` (default): Asynchronous mode (returns queue tokens immediately). |
| `base64_encoded` | `boolean` | `true`: Payload body fields (`source_code`, `stdin`, `expected_output`) are base64-encoded.<br>`false` (default): Payload fields are parsed as raw strings. |

---

## API Endpoints

### 1. Single Submission `POST /submissions`

Submits a single source code execution request.

* **Request Payload Example (`wait=true`, raw string):**
```bash
curl -X POST "http://localhost:3000/submissions?wait=true" \
  -H "Content-Type: application/json" \
  -d '{
    "source_code": "#include <iostream>\nusing namespace std;\nint main(){cout<<\"hello\";return 0;}",
    "language_id": 54
  }'
```

* **Response Example (`200 OK`):**
```json
{
  "stdout": "hello",
  "stderr": null,
  "compile_output": null,
  "time": 0.003,
  "memory": 3880,
  "status": {
    "id": 3,
    "description": "Accepted"
  }
}
```

### 2. Solo Batch Submission `POST /submissions/solobatch`

Evaluates a single source code submission against multiple `stdin` test cases in parallel without redundant recompilations.

* **Request Payload Example (`wait=false`):**
```json
{
  "source_code": "int a = int(input())\nprint(a+1)",
  "language_id": 71,
  "stdin": ["5", "4", "3"],
  "base64_encoded": false,
  "callback_url": "[https://mycoolserver.com/cb?id=18](https://mycoolserver.com/cb?id=18)"
}
```

* **Response Example:**
```json
{
  "tokens": ["un725hdb2", "h1us81hd91", "idh826f29hw7"]
}
```

---

## Language ID Reference

| Language | `language_id` |
| :--- | :--- |
| **C++ (GCC)** | `54` |
| **Java (OpenJDK 21)** | `62` |
| **JavaScript (Node.js)** | `63` |
| **Python 3** | `71` |
