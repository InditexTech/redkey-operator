<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Troubleshooting

The Redkey Operator is a complex piece of software. In some cases it can self-recover from service losses but in other cases a cluster administrator has to intervene. This document helps administrators how to solve failing clusters.

The first section describes [cluster failure scenarios](#cluster-failure-scenarios) and show to solve them.

The second section lists [commands](#cluster-manager-commands) used to solve the failures described in the first section.

## Cluster failure scenarios

### Openshift 3 does not support OLM and runs on an older kube-api version

Redkey Operator is deployed in a production intranet cluster, which runs on Openshift 3. Openshift 3 uses an old `kube-api` version which comes with `CRD apiextension v1beta1`. The solution is to [generate Openshift 3 CRDs](#generate-openshift-3-crds).

### Downgrade a failing operator

See [this thread](https://github.com/operator-framework/operator-lifecycle-manager/issues/1177) in the OLM repository.

To summarize, downgrading is not supported as of October 2020. OLM is designed to upgrade to the next version and downgrading is a form of upgrading. One issue that can come up is that CRs may have to migrated first before you can downgrade a CDR. If there are breaking changes downgrading may not be possible. The solution again is to upgrade to a newer version.

### A failing primary node did not rejoin the cluster

Run the [forget command](#forget-a-node) so all nodes forget about this failing primary. Now run the [check command](#check-the-cluster) to see if the new primary has rejoined correctly and then run the [fix command](#fixing-the-cluster) to fix the slot allocation.

### The cluster has slave node

If a slave node is present in a primaries-only cluster, ix this by running the [reset command](#reset-a-node) on the slave to reset its configuration, so it will become a primary. Now run the [check command](#check-the-cluster) to see if the new primary has rejoined correctly and then run the [fix command](#fixing-the-cluster) to fix the slot allocation.

### Nodes IPs are inconsistent

You can find the node IP information by running the [nodes command](#gathering-nodes-information). If the IPs are inconsistent run the [cluster meet](#cluster-meet) from a correctly configured node to the incorrect node.

### An upgrade is stuck on a partition

During a [Rolling N+1 upgrade](upgrade.md) the cluster status stays `Upgrading` and the
`substatus.upgradingPartition` does not advance over several reconcile intervals. Robin
deliberately refuses to advance to the next partition while the topology is inconsistent,
so this usually means a previous reshard left the cluster in a bad state. Common causes:

- **Open slots** left in a `MIGRATING`/`IMPORTING` state by an interrupted reshard. Run
  the [check command](#check-the-cluster) — if it reports open slots, run the
  [fix command](#fixing-the-cluster). Robin also runs `redis-cli --cluster fix`
  automatically before each reshard, so confirm the source node can be reached.
- **Dead/`FAIL` nodes** from earlier recycles that were not cleaned up, causing reshard
  timeouts. Run the [check command](#check-the-cluster); if you see nodes in `fail` state,
  [forget them](#forget-a-node) from every surviving member.
- **Gossip not converged** — a recycled pod has not yet been recognised by all peers.
  Verify the IPs with the [nodes command](#gathering-nodes-information) and, if needed,
  issue a [cluster meet](#cluster-meet) from a healthy node.
- **A replica that never synced** — Robin waits for `master_link_status:up` before
  treating a replica as HA. Check `redis-cli -h <pod> INFO replication`; a replica stuck
  at `master_link_status:down` points to a connectivity or auth problem with its primary.

Once the underlying inconsistency is fixed, Robin resumes the upgrade from the last
recorded substatus on the next reconcile — no manual status edit is required.

## Cluster manager commands

Below are a list of commands and code snippets for inspecting and repairing the cluster. Most commands use kubectl combined with the Redkey cluster manager tool. This tool is invoked with

```shell
redis-cli --cluster ...
```

For more information see the [Redis cluster tutorial](https://redis.io/topics/cluster-tutorial).

### Check the cluster

Check whether the cluster’s nodes are available and the slots are allocated properly.

```shell
kubectl exec -it [redis-pod-name] -- redis-cli --cluster check localhost 6379
```

#### Good Status - All 16384 slots covered

```shell
$ kubectl exec -it [redis-pod-name] -- redis-cli --cluster check localhost 6379
localhost:6379 (89617d3e...) -> 0 keys | 5461 slots | 0 slaves.
10.244.1.123:6379 (c0aa78d2...) -> 0 keys | 5462 slots | 0 slaves.
10.244.0.57:6379 (11d23d3c...) -> 0 keys | 5461 slots | 0 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.
>>> Performing Cluster Check (using node localhost:6379)
M: 89617d3e8895cc17d1062601d7ee892bc7bc45aa localhost:6379
   slots:[0-5460] (5461 slots) master
M: c0aa78d2c2e5aa21bb968886f6036e19065a5dd6 10.244.1.123:6379
   slots:[10922-16383] (5462 slots) master
M: 11d23d3c2cbac49201825415b38015c862313ec9 10.244.0.57:6379
   slots:[5461-10921] (5461 slots) master
[OK] All nodes agree about slots configuration.
>>> Check for open slots...
>>> Check slots coverage...
[OK] All 16384 slots covered.
```

#### Bad Status - Slots in incoherent state

```shell
$ kubectl exec -it [redis-pod-name] -- redis-cli --cluster check localhost 6379
localhost:6379 (89617d3e...) -> 0 keys | 5461 slots | 0 slaves.
10.244.1.123:6379 (c0aa78d2...) -> 0 keys | 5462 slots | 0 slaves.
10.244.0.57:6379 (11d23d3c...) -> 0 keys | 5461 slots | 0 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.
>>> Performing Cluster Check (using node localhost:6379)
M: 89617d3e8895cc17d1062601d7ee892bc7bc45aa localhost:6379
   slots:[0-5460] (5461 slots) master
M: c0aa78d2c2e5aa21bb968886f6036e19065a5dd6 10.244.1.123:6379
   slots:[10922-16383] (5462 slots) master
M: 11d23d3c2cbac49201825415b38015c862313ec9 10.244.0.57:6379
   slots:[5461-10921] (5461 slots) master
[OK] All nodes agree about slots configuration.
>>> Check for open slots...
[WARNING] Node localhost:6379 has slots in migrating state 1,2,3,4.
[WARNING] Node 10.244.0.57:6379 has slots in importing state 1,2.
[WARNING] The following slots are open: 4,2,1,3.
>>> Check slots coverage...
[OK] All 16384 slots covered.
command terminated with exit code 1
```

#### Bad Status - Missing slots

```shell
$ kubectl exec -it [redis-pod-name] -- redis-cli --cluster check localhost 6379
localhost:6379 (89617d3e...) -> 0 keys | 5460 slots | 0 slaves.
10.244.1.123:6379 (c0aa78d2...) -> 0 keys | 5462 slots | 0 slaves.
10.244.0.57:6379 (11d23d3c...) -> 0 keys | 5461 slots | 0 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.
>>> Performing Cluster Check (using node localhost:6379)
M: 89617d3e8895cc17d1062601d7ee892bc7bc45aa localhost:6379
   slots:[0],[2-5460] (5460 slots) master
M: c0aa78d2c2e5aa21bb968886f6036e19065a5dd6 10.244.1.123:6379
   slots:[10922-16383] (5462 slots) master
M: 11d23d3c2cbac49201825415b38015c862313ec9 10.244.0.57:6379
   slots:[5461-10921] (5461 slots) master
[ERR] Nodes don't agree about configuration!
>>> Check for open slots...
>>> Check slots coverage...
[ERR] Not all 16384 slots are covered by nodes.
```

### Gathering nodes information

For each pod list the status of each node: its role, primary or slave, whether it is failing, and which slots are allocated to it.

```shell
for pod in $(kubectl get pods -l redkey-cluster-name=[redkey-cluster-name] -o json | jq -r '.items[] | .metadata.name'); do echo "POD ${pod} NODES"; kubectl exec -it ${pod} -- redis-cli CLUSTER NODES  | sort; done
```

### Cluster meet

The cluster meet command makes a node join the cluster. This command should be run from a correctly configured pod to have it meet the node joining the cluster.

```shell
kubectl exec -it [redis-pod-name] -- redis-cli cluster meet [ip-new-node] 6379
```

To do cluster meet among all cluster nodes:

```shell
# File: ./redis-cli-meet-all-nodes.sh
# Example: ./redis-cli-meet-all-nodes.sh redis-cluster-mecprada-2
if [ -z "$1" ]; then
    echo "Usage $0 <redis-cluster>"
    exit 1
fi
cluster_redis=$1
echo "Meeting all with all nodes: " $cluster_redis

for pod in $(oc get pod -l redkey-cluster-name=$cluster_redis,redis.redkey.operator/component=redis -o json | jq -r '.items[].metadata.name'); do
    for ip in $(oc get pod -l redkey-cluster-name=$cluster_redis,redis.redkey.operator/component=redis -o json | jq -r '.items[].status.podIP'); do
        oc exec $pod -- redis-cli cluster meet $ip 3689
    done
done
```

### Fixing the cluster

Make sure all nodes agree about slots configuration.

```shell
$ kubectl exec -it [redis-pod-name] -- redis-cli --cluster fix localhost 6379
localhost:6379 (89617d3e...) -> 0 keys | 5460 slots | 0 slaves.
10.244.1.123:6379 (c0aa78d2...) -> 0 keys | 5462 slots | 0 slaves.
10.244.0.57:6379 (11d23d3c...) -> 0 keys | 5461 slots | 0 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.
>>> Performing Cluster Check (using node localhost:6379)
M: 89617d3e8895cc17d1062601d7ee892bc7bc45aa localhost:6379
slots:[0],[2-5460] (5460 slots) master
M: c0aa78d2c2e5aa21bb968886f6036e19065a5dd6 10.244.1.123:6379
slots:[10922-16383] (5462 slots) master
M: 11d23d3c2cbac49201825415b38015c862313ec9 10.244.0.57:6379
slots:[5461-10921] (5461 slots) master
[ERR] Nodes don't agree about configuration!
>>> Check for open slots...
>>> Check slots coverage...
[ERR] Not all 16384 slots are covered by nodes.

>>> Fixing slots coverage...
The following uncovered slots have no keys across the cluster:
[1]
Fix these slots by covering with a random node? (type 'yes' to accept): yes
>>> Covering slot 1 with 10.244.1.123:6379
```

Now run the [cluster check command](#check-the-cluster) again to see if the cluster is fixed.

### Reset a node

Perform a soft reset which resets the node config but keeps the node id.

```shell
kubectl exec -it [redis-pod-name] -- redis-cli cluster reset
```

### Forget a node

Have all nodes in the cluster forget a failing node. First get the node id via the [cluster nodes command](#gathering-nodes-information).

```shell
for pod in $(kubectl get pods -l redkey-cluster-name=[redkey-cluster-name] -o json | jq -r '.items[] | .metadata.name'); do echo "POD ${pod} NODES"; kubectl exec -it ${pod} -- redis-cli CLUSTER FORGET [node-id] | sort; done
```

If there is a set of disconnected or failing nodes they can forget all at once with the following script:

```shell
# File: ./redis-cli-forget-all-disconnected.sh
# Example: ./redis-cli-forget-all-disconnected.sh redis-cluster-mecprada-2
if [ -z "$1" ]; then
    echo "Usage $0 <redis-cluster>"
    exit 1
fi
cluster_redis=$1
echo "Forgetting disconnect in: " $cluster_redis
for pod in $(oc get pod -l redkey-cluster-name=$cluster_redis,redis.redkey.operator/component=redis -o json | jq -r '.items[].metadata.name'); do
    for node in $(oc exec $pod -- cat /tmp/nodes.conf | egrep '(fail|disconnected)' | cut -f1 -d" "); do
        oc exec  $pod -- redis-cli cluster forget $node
    done
done
```

### Generate Openshift 3 CRDs

Generate a CRD for the older kube-api version using operator-sdk command.

```shell
operator-sdk generate crds --crd-version=v1
```
