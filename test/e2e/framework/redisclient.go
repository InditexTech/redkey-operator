// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
)

// Redis CLI commands used in E2E tests.
const (
	RedisCliDBSize           = "redis-cli DBSIZE"
	RedisCliFlushAll         = "redis-cli FLUSHALL"
	RedisCliClusterInfo      = "redis-cli cluster info"
	RedisCliClusterNodes     = "redis-cli cluster nodes"
	RedisCliClusterCheck     = "redis-cli --cluster check localhost:6379"
	RedisCliClusterSlots     = "redis-cli cluster slots"
	RedisCliClusterMyID      = "redis-cli cluster myid"
	RedisCliClusterForget    = "redis-cli cluster forget %s"
	RedisCliClusterMeet      = "redis-cli cluster meet %s %d"
	RedisCliClusterDelSlots  = "redis-cli cluster delslots %s"
	RedisCliClusterRebalance = "redis-cli --cluster rebalance --cluster-use-empty-masters localhost:6379"
	RedisCliPing             = "redis-cli ping"
)

// Auth-aware commands (use %s for password placeholder).
const (
	RedisCliDBSizeAuth       = "redis-cli -a %s DBSIZE"
	RedisCliClusterInfoAuth  = "redis-cli -a %s cluster info"
	RedisCliClusterNodesAuth = "redis-cli -a %s cluster nodes"
	RedisCliClusterCheckAuth = "redis-cli -a %s --cluster check localhost:6379"
	RedisCliPingAuth         = "redis-cli -a %s ping"
)

// ClusterInfo holds parsed redis CLUSTER INFO fields.
type ClusterInfo struct {
	State            string
	SlotsAssigned    int
	SlotsOK          int
	SlotsPFail       int
	SlotsFail        int
	KnownNodes       int
	ClusterSize      int
	MessagesSent     int
	MessagesReceived int
}

// ParseClusterInfo parses the output of CLUSTER INFO into a struct.
func ParseClusterInfo(output string) ClusterInfo {
	info := ClusterInfo{}
	lines := strings.SplitSeq(output, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "cluster_state":
			info.State = value
		case "cluster_slots_assigned":
			info.SlotsAssigned, _ = strconv.Atoi(value)
		case "cluster_slots_ok":
			info.SlotsOK, _ = strconv.Atoi(value)
		case "cluster_slots_pfail":
			info.SlotsPFail, _ = strconv.Atoi(value)
		case "cluster_slots_fail":
			info.SlotsFail, _ = strconv.Atoi(value)
		case "cluster_known_nodes":
			info.KnownNodes, _ = strconv.Atoi(value)
		case "cluster_size":
			info.ClusterSize, _ = strconv.Atoi(value)
		case "cluster_stats_messages_sent":
			info.MessagesSent, _ = strconv.Atoi(value)
		case "cluster_stats_messages_received":
			info.MessagesReceived, _ = strconv.Atoi(value)
		}
	}
	return info
}

// ClusterNode holds parsed redis CLUSTER NODES fields.
type ClusterNode struct {
	ID       string
	IP       string
	Port     int
	Flags    string
	MasterID string
	Slots    []string
}

// IsReplica returns true if the node has the "slave" flag.
func (n ClusterNode) IsReplica() bool {
	return strings.Contains(n.Flags, "slave")
}

// IsPrimary returns true if the node has the "master" flag.
func (n ClusterNode) IsPrimary() bool {
	return strings.Contains(n.Flags, "master")
}

// ParseClusterNodes parses the output of CLUSTER NODES.
func ParseClusterNodes(output string) []ClusterNode {
	lines := strings.Split(output, "\n")
	nodes := make([]ClusterNode, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		node := ClusterNode{
			ID:       parts[0],
			Flags:    parts[2],
			MasterID: parts[3],
		}

		// Parse IP:port@cport
		addrParts := strings.Split(parts[1], "@")
		if len(addrParts) >= 1 {
			ipPort := strings.Split(addrParts[0], ":")
			if len(ipPort) >= 2 {
				node.IP = ipPort[0]
				node.Port, _ = strconv.Atoi(ipPort[1])
			}
		}

		// Slots are from index 8 onward
		if len(parts) > 8 {
			node.Slots = parts[8:]
		}

		nodes = append(nodes, node)
	}
	return nodes
}

// GetClusterInfo executes CLUSTER INFO on a pod and parses the result.
func GetClusterInfo(namespace, podName string, password ...string) (ClusterInfo, error) {
	cmd := RedisCliClusterInfo
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf(RedisCliClusterInfoAuth, password[0])
	}
	stdout, _, err := ExecInPod(namespace, podName, cmd)
	if err != nil {
		return ClusterInfo{}, fmt.Errorf("cluster info from %s/%s: %w", namespace, podName, err)
	}
	return ParseClusterInfo(stdout), nil
}

// GetClusterNodes executes CLUSTER NODES on a pod and parses the result.
func GetClusterNodes(namespace, podName string, password ...string) ([]ClusterNode, error) {
	cmd := RedisCliClusterNodes
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf(RedisCliClusterNodesAuth, password[0])
	}
	stdout, _, err := ExecInPod(namespace, podName, cmd)
	if err != nil {
		return nil, fmt.Errorf("cluster nodes from %s/%s: %w", namespace, podName, err)
	}
	return ParseClusterNodes(stdout), nil
}

// GetNodeID returns the CLUSTER MYID of the redis instance in the given pod.
func GetNodeID(namespace, podName string, password ...string) (string, error) {
	cmd := RedisCliClusterMyID
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf("redis-cli -a %s cluster myid", password[0])
	}
	stdout, _, err := ExecInPod(namespace, podName, cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// ForgetNode executes CLUSTER FORGET on the given pod to forget a node ID.
func ForgetNode(namespace, podName, nodeID string, password ...string) error {
	cmd := fmt.Sprintf(RedisCliClusterForget, nodeID)
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf("redis-cli -a %s cluster forget %s", password[0], nodeID)
	}
	_, _, err := ExecInPod(namespace, podName, cmd)
	return err
}

// MeetNode executes CLUSTER MEET on the given pod.
func MeetNode(namespace, podName, targetIP string, targetPort int, password ...string) error {
	cmd := fmt.Sprintf(RedisCliClusterMeet, targetIP, targetPort)
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf("redis-cli -a %s cluster meet %s %d", password[0], targetIP, targetPort)
	}
	_, _, err := ExecInPod(namespace, podName, cmd)
	return err
}

// DelSlots executes CLUSTER DELSLOTS on the given pod.
func DelSlots(namespace, podName string, slots []int, password ...string) error {
	slotStrs := make([]string, len(slots))
	for i, s := range slots {
		slotStrs[i] = strconv.Itoa(s)
	}
	slotsArg := strings.Join(slotStrs, " ")
	cmd := fmt.Sprintf(RedisCliClusterDelSlots, slotsArg)
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf("redis-cli -a %s cluster delslots %s", password[0], slotsArg)
	}
	_, _, err := ExecInPod(namespace, podName, cmd)
	return err
}

// SetSlotMigratingBetween puts a single slot owned by srcPod into an open (migrating/importing) state
// between srcPod (migrating) and dstPod (importing), simulating a resharding interrupted mid-flight
// between two specific nodes. Because it targets named pods, the open slot can be placed on non-seed
// nodes — the exact condition that used to stall a scale-up rebalance: importing/migrating markers are
// local to the node that holds them and are not gossiped, so the rebalance seed's CLUSTER NODES view
// cannot see an open slot left on another node. It returns the affected slot.
func SetSlotMigratingBetween(namespace, srcPod, dstPod string) (int, error) {
	srcID, err := GetNodeID(namespace, srcPod)
	if err != nil {
		return 0, fmt.Errorf("getting node ID from %s: %w", srcPod, err)
	}
	dstID, err := GetNodeID(namespace, dstPod)
	if err != nil {
		return 0, fmt.Errorf("getting node ID from %s: %w", dstPod, err)
	}
	// Pick the first slot srcPod owns so CLUSTER SETSLOT MIGRATING (which requires ownership) succeeds.
	slotOut, _, err := ExecInPod(namespace, srcPod,
		"redis-cli cluster nodes | grep myself | awk '{ print $9 }' | cut -d- -f1")
	if err != nil {
		return 0, fmt.Errorf("reading an owned slot from %s: %w", srcPod, err)
	}
	slot, err := strconv.Atoi(strings.TrimSpace(slotOut))
	if err != nil {
		return 0, fmt.Errorf("parsing owned slot %q from %s: %w", strings.TrimSpace(slotOut), srcPod, err)
	}

	if _, _, err := ExecInPod(namespace, srcPod,
		fmt.Sprintf("redis-cli cluster setslot %d migrating %s", slot, dstID)); err != nil {
		return 0, fmt.Errorf("setting slot %d migrating on %s: %w", slot, srcPod, err)
	}
	if _, _, err := ExecInPod(namespace, dstPod,
		fmt.Sprintf("redis-cli cluster setslot %d importing %s", slot, srcID)); err != nil {
		return 0, fmt.Errorf("setting slot %d importing on %s: %w", slot, dstPod, err)
	}
	return slot, nil
}

// InsertKeys inserts `count` random key-value pairs into the cluster via the given pod.
func InsertKeys(namespace, podName string, count int, password ...string) error {
	for i := range count {
		key := fmt.Sprintf("e2e-key-%d-%d", i, time.Now().UnixNano())
		value := fmt.Sprintf("value-%d", i)
		cmd := fmt.Sprintf("redis-cli -c set %s %s", key, value)
		if len(password) > 0 && password[0] != "" {
			cmd = fmt.Sprintf("redis-cli -c -a %s set %s %s", password[0], key, value)
		}
		if _, _, err := ExecInPod(namespace, podName, cmd); err != nil {
			return fmt.Errorf("inserting key %s: %w", key, err)
		}
	}
	return nil
}

// GetDBSize returns the total number of unique keys across all primary pods by running DBSIZE.
// Replica pods are skipped to avoid double-counting keys that exist on both primary and replica.
func GetDBSize(namespace string, podNames []string, password ...string) (int, error) {
	total := 0
	re := regexp.MustCompile(`\d+`)
	for _, pod := range podNames {
		// Check the node's role; only count primaries to avoid double-counting.
		roleCmd := "redis-cli role"
		if len(password) > 0 && password[0] != "" {
			roleCmd = fmt.Sprintf("redis-cli -a %s role", password[0])
		}
		roleOut, _, err := ExecInPod(namespace, pod, roleCmd)
		if err != nil {
			return 0, fmt.Errorf("role check on %s: %w", pod, err)
		}
		firstLine := strings.TrimSpace(strings.SplitN(roleOut, "\n", 2)[0])
		if firstLine != "master" {
			continue
		}

		cmd := RedisCliDBSize
		if len(password) > 0 && password[0] != "" {
			cmd = fmt.Sprintf(RedisCliDBSizeAuth, password[0])
		}
		stdout, _, err := ExecInPod(namespace, pod, cmd)
		if err != nil {
			return 0, fmt.Errorf("dbsize from %s: %w", pod, err)
		}
		match := re.FindString(strings.TrimSpace(stdout))
		if match != "" {
			count, _ := strconv.Atoi(match)
			total += count
		}
	}
	return total, nil
}

// FlushAll runs FLUSHALL on all given pods.
func FlushAll(namespace string, podNames []string, password ...string) error {
	for _, pod := range podNames {
		cmd := RedisCliFlushAll
		if len(password) > 0 && password[0] != "" {
			cmd = fmt.Sprintf("redis-cli -a %s FLUSHALL", password[0])
		}
		if _, _, err := ExecInPod(namespace, pod, cmd); err != nil {
			return fmt.Errorf("flushing %s: %w", pod, err)
		}
	}
	return nil
}

// PingRedis runs redis-cli ping on a pod and returns true if PONG received.
func PingRedis(namespace, podName string, password ...string) bool {
	cmd := RedisCliPing
	if len(password) > 0 && password[0] != "" {
		cmd = fmt.Sprintf(RedisCliPingAuth, password[0])
	}
	stdout, _, err := ExecInPod(namespace, podName, cmd)
	if err != nil {
		return false
	}
	return strings.TrimSpace(stdout) == "PONG"
}

// CheckAuthRequired returns true if the Redis server requires authentication,
// i.e. PING without a password fails. This is the inverse of CheckAuthDisabled.
// Unlike PingRedis with a password (which can produce false positives when the
// -a flag is wrong but redis-cli still sends PING and gets PONG), this check
// does not pass any password, so AUTH is never attempted by redis-cli.
func CheckAuthRequired(namespace, podName string) bool {
	return !PingRedis(namespace, podName)
}

// CheckAuthDisabled returns true if the Redis server does NOT require
// authentication, i.e. PING without a password succeeds.
func CheckAuthDisabled(namespace, podName string) bool {
	return PingRedis(namespace, podName)
}

// VerifyClusterHealthy checks that the cluster state is "ok" and all 16384 slots are assigned.
func VerifyClusterHealthy(namespace, podName string, expectedNodes int, password ...string) error {
	info, err := GetClusterInfo(namespace, podName, password...)
	if err != nil {
		return err
	}
	if info.State != "ok" {
		return fmt.Errorf("cluster state is %q, expected ok", info.State)
	}
	if info.SlotsAssigned != 16384 {
		return fmt.Errorf("slots assigned = %d, expected 16384", info.SlotsAssigned)
	}
	if expectedNodes > 0 && info.KnownNodes != expectedNodes {
		return fmt.Errorf("known nodes = %d, expected %d", info.KnownNodes, expectedNodes)
	}
	// Also verify via CLUSTER NODES that all nodes have resolved roles (not in
	// handshake state). CLUSTER INFO counts transient handshake nodes in
	// known_nodes, which can cause a race with the CLUSTER FORGET blacklist.
	if expectedNodes > 0 {
		nodes, err := GetClusterNodes(namespace, podName, password...)
		if err != nil {
			return fmt.Errorf("getting cluster nodes: %w", err)
		}
		resolved := 0
		for _, n := range nodes {
			if n.IsPrimary() || n.IsReplica() {
				resolved++
			}
		}
		if resolved != expectedNodes {
			return fmt.Errorf("resolved nodes (master/slave) = %d, expected %d", resolved, expectedNodes)
		}
	}
	return nil
}

// LogClusterInfo prints the cluster info to GinkgoWriter for debugging.
func LogClusterInfo(namespace, podName string, password ...string) {
	info, err := GetClusterInfo(namespace, podName, password...)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get cluster info from %s/%s: %v\n", namespace, podName, err)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Cluster Info from %s/%s: state=%s, slots=%d, known_nodes=%d, cluster_size=%d\n",
		namespace, podName, info.State, info.SlotsAssigned, info.KnownNodes, info.ClusterSize)
}
