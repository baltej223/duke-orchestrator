# Duke Orchestrator
Its a declarative orchestration system for duke nodes running on the same machine.
- You provide a control file in which you declare the desired state, and the system continuously reconciles the actual state to match it.

> Note: Example of control file (a specific yaml file) can be found in the root of repo as [init.yaml](https://github.com/baltej223/duke-orchestrator/blob/main/init.yaml)

# Quick Start
Make sure you have go already installed.
```sh
git clone https://github.com/baltej223/duke-orchestrator.git duke-orchestrator
cd duke-orchestrator
```
- **Then copy the compiled binary to this folder (duke-orchestrator) by the name of main**.

Then compile the orchestrator.
```sh
go build duke_orch.go
```

```sh
./duke_orch -control-file init.yaml 
# ./duke_orch -cf init.yaml
# or
./duke_orch -cf <your_file>.yaml
```
# Explanation of control file
> [init.yaml](https://github.com/baltej223/duke-orchestrator/blob/main/init.yaml)

```yaml
version: 0.1.0
duke_executable: ./main
total_nodes: 7
logging_file: cluster.log

seed_node:
  id: a
  address: localhost:8000
  api_at: localhost:9000

non_seed_nodes:
  nodes:
    - id: b
      address: localhost:8001
      api_at: localhost:9001

    - id: c
      address: localhost:8002
      api_at: localhost:9002

    - id: d
      address: localhost:8003
      api_at: localhost:9003

    - id: e
      address: localhost:8004
      api_at: localhost:9004

    - id: f
      address: localhost:8005
      api_at: localhost:9005

    - id: g
      address: localhost:8006
      api_at: localhost:9006
```
- version: The version of the duke_orchestrator
- duke_executable: The path of the duke compiled executable. Check [https://github.com/baltej223/dukedb](https://github.com/baltej223/dukedb) for compiling from source code.
- total_nodes: Total number of nodes.
- logging_file: The file to log the duke nodes' logs.
- ## seed_node:
```yaml
seed_node:
  id: a
  address: localhost:8000
  api_at: localhost:9000
```
- Define one seed node, since the first node that runs should be the seed node, to which all the other nodes will join to form the cluster.
- id: Should be different for all the nodes.
- address: The address at which the node will receive information from other nodes.
- api_at: The address at which the client will send GET/PUT requests.

- ## non_seed_nodes:
```yaml
    - id: b
      address: localhost:8001
      api_at: localhost:9001
```
- id: Should be different for all the nodes.
- address: The address at which the node will receive information from other nodes.
- api_at: The address at which the client will send GET/PUT requests.

> [!Note]
> In the first 20-30 seconds the cluster will be forming and the view of cluster for each node might be different, after that, it will converge to the true view of the cluster.

## Contributing
Its a rather new project, any types of contribution, bug reports, features, and changes are welcomed.
