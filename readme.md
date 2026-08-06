# RUNE - Runtime for Untrusted Native Execution

The end-to-end code execution engine built for **Algorhythm**.

It is in very early stages of development. The final aim:

* A modern and fast drop-in replacement for judge0 specifically for Algorhythm, with support for: C++, Java, JavaScript and Python3 only. 
* Have synchronous submission pipeline
* Have asynchronous queue and worker + webhook based pipeline
* Support Batch submissions for both sync and async pipelines


## Installation

1. Clone the **RUNE** repository on a clean machine
```bash
git clone https://github.com/AtharvDubey12/RUNE.git
```
2. Open main directory
```bash
cd RUNE
```
3. Run the initialization script
```bash
./init.sh
```
4. Launch **RUNE**
```bash
go run cmd/RUNE/main.go
```
Your **RUNE** instance is now running on socket address: http://localhost:3000/

>Note that you only need to run `init.sh` before running RUNE for the very first time.
## Customizations

1. RUNE Containers limit

By default, RUNE containers are capped at a maximum of **50** in ```config.json```. This means that at any instance of time, there could be at max **50** independent code executions running on a machine. If the requests are more than the available containers, then it waits until a container is free. You will have to decide this amount according to the system resources of your machine on which **RUNE** is running.

2. Redis Password

By default, Redis password is set as **RUNEredis** in ```config.json```. It is recommended that you change this to something else before deploying somewhere.

3. PostgreSQL Password

By default, PostgreSQL password is set as **RUNEpost** in ```config.json```. It is recommended that you change this to something else before deploying somewhere.

## API Flags

Below are two *optional* flags that can be appended to all APIs.

1. ```wait``` flag

```wait=true``` means a synchronous submission, ```wait=false``` or absense of flag indicates asynchronous submission.
> Note that for low traffic demos use ```wait=true``` as sync pipeline is faster than async counterpart. On the otherhand, this approach doesn't scale well when your platform has a large number of concurrent users as you can only have a limited number of RUNE Containers running at an instance time and increasing their amount too much would overwhelm the system resources.

2. ```base64_encoded``` flag

```base64_encoded=true``` means HTTP ```POST``` payload has every body field encoded in ```base64```. Similarly, ```base64_encoded=false``` or absense of flag indicates payload fields are to be taken as is.
>Note that is recommended to use ```base64_encoded=true``` to ensure special characters in code doesn't cause any issues.

## API Endpoints

1. Single Submission ```POST``` ```/submissions```

Hit this route to make a single submission. 

An Example payload without ```stdin``` and ```base64``` encoding:
```json
{
   "source_code" : "print('Hello, World!')",
   "language_id" : 71, // Python's language ID is 71
    "base64_encoded": false
}
```

2. Solo Batch Submission `POST` `/submissions/solobatch`

This functionality isn't available in `Judge0` where you could send one code and multiple testcases. Traditionally each code is compiled and tested against its corresponding testcase. This route however solves this issue where there is one code that is to be judged across multiple tests.

An example payload to

```json
{
    "source_code": "int a = int(input())\nprint(a+1)",
    "language_id": 71,
    "stdin": ["5", "4", "3"],
    "base64_encoded": false,
    "callback_url": "https://mycoolserver.com/cb?id=18"
}
```
the output when `wait=false`
```json
{
    "tokens" : [un725hdb2, h1us81hd91, idh826f29hw7]
}
```

> Note that the route would work fine with `wait=true` but the `HTTP` request run the risk of being timed out before all tests could finish. Thus, it is recommended to use it only with `wait=false` (asynchronously). 

