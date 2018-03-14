# golb
Educational project.
HTTP load balancer based on standart golang componenets and hashicorp implementation of [raft](https://github.com/hashicorp/raft) protocol

## features:
* pass requests from slave nodes to master
* if master leaves cluster -- new one choosen based on raft leadership

## build

```
git clone https://github.com/pupska/golb.git
cd golb && go build
```

## usage

####init server with first node
```
./golb -datafolder ./n0 -bootstrap -hport 8000 -rport 7000
```

####join server
```
./golb -datafolder ./n1 -hport 8001 -rport 7001 -join <web_addr_of_any_node_in_cluster>
```

####example
######setup cluster
```
# init cluster:
./golb -datafolder ./n0 -addr 127.0.0.1 -bootstrap -hport 8000 -rport 7000

# join next nodes:
./golb -datafolder ./n1 -addr 127.0.0.1 -hport 8001 -rport 7001 -join 127.0.0.1:8000
./golb -datafolder ./n2 -addr 127.0.0.1 -hport 8002 -rport 7002 -join 127.0.0.1:8000

# join to cluster via non-master node.
./golb -datafolder ./n3 -addr 127.0.0.1 -hport 8003 -rport 7003 -join 127.0.0.1:8002

```

###### try to access web service on different nodes

```
# request to "master node"
$ curl 127.0.0.1:8000
responce from master node at 127.0.0.1:8000

# request to "slave node"
$ curl 127.0.0.1:8001
responce from master node at 127.0.0.1:8000

#repeat request to some node in cluster after leading master stopped
curl 127.0.0.1:8001
responce from master node at 127.0.0.1:8002
```