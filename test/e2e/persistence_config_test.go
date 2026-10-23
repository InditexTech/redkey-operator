// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Persistence directives (appendonly/save) are owned by exactly one layer, so the generated
// redis.conf never repeats them:
//   - ephemeral clusters: Robin writes `appendonly no` + `save ""`;
//   - persistent clusters: Robin writes none, they come from spec.config (e.g. the Helm chart),
//     and when spec.config sets none the Redis defaults apply.
var _ = Describe("Persistence configuration", Ordered, Label("persistence"), func() {
	const (
		baseConfig = `maxmemory 90mb
maxmemory-policy allkeys-lru
protected-mode no`
		// Redis defaults when redis.conf sets no persistence directive.
		redisDefaultSave = "3600 1 300 100 60 10000"
		testKeys         = 50
	)

	// Persistence blocks rendered by the Helm chart for persistent Redkeys.
	var (
		chartAOFOnly    = "save \"\"\nappendonly yes\nappendfsync everysec\n" + baseConfig
		chartAllDisable = "save \"\"\nappendonly no\n" + baseConfig
	)

	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 60*time.Minute)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-persistence")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	AfterAll(func() {
		By("cleaning up the test namespace")
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
	})

	// expectRedisConfDirectives checks how many appendonly/save lines the generated ConfigMap has.
	expectRedisConfDirectives := func(clusterName string, wantAppendonly, wantSave int) {
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cm)).To(Succeed())
		conf := cm.Data["redis.conf"]
		Expect(countRedisConfDirective(conf, "appendonly")).To(Equal(wantAppendonly),
			"unexpected number of 'appendonly' lines in redis.conf:\n%s", conf)
		Expect(countRedisConfDirective(conf, "save")).To(Equal(wantSave),
			"unexpected number of 'save' lines in redis.conf:\n%s", conf)
	}

	// expectRuntimePersistence checks the effective appendonly/save values on every Redis pod.
	expectRuntimePersistence := func(clusterName string, nodes int, wantAppendonly, wantSave string) {
		Eventually(func(g Gomega) {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, nodes)
			g.Expect(err).NotTo(HaveOccurred())
			for _, pod := range podNames {
				g.Expect(redisConfigGet(clusterNs, pod, "appendonly")).To(Equal(wantAppendonly),
					"pod %s: unexpected appendonly", pod)
				g.Expect(redisConfigGet(clusterNs, pod, "save")).To(Equal(wantSave),
					"pod %s: unexpected save", pod)
			}
		}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
	}

	// applyRedisConfig updates spec.config and waits until the change is rolled out and healthy.
	applyRedisConfig := func(clusterName string, nodes int, config string) {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		updateClusterTopology(ctx, key, func(c *redkeyv1beta1.Redkey) {
			c.Spec.Config = config
		})
		Expect(waitForActiveRedisConfigApplied(ctx, clusterName, clusterNs, config,
			framework.UpgradeTimeout)).To(Succeed())
		_, err := framework.WaitForClusterReady(ctx, k8sClient, key, framework.HealthTimeout)
		Expect(err).NotTo(HaveOccurred())
		Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
			framework.RedisPodLabels(clusterName), nodes, framework.HealthTimeout)).To(Succeed())
	}

	expectDBSize := func(clusterName string, nodes, want int) {
		Eventually(func(g Gomega) {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, nodes)
			g.Expect(err).NotTo(HaveOccurred())
			size, err := framework.GetDBSize(clusterNs, podNames)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(size).To(Equal(want))
		}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
	}

	Context("Ephemeral cluster", func() {
		const clusterName = "persistence-ephemeral"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("gets AOF and RDB disabled by Robin, written once", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Config = baseConfig
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs}, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying redis.conf has a single appendonly and save line")
			expectRedisConfDirectives(clusterName, 1, 1)

			By("verifying AOF and RDB are disabled on every node")
			expectRuntimePersistence(clusterName, 3, "no", "")
		})
	})

	Context("Persistent cluster", func() {
		const (
			clusterName = "persistence-storage"
			nodes       = 3
		)

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("gets no persistence directive from Robin and runs with the Redis defaults", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Config = baseConfig
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs}, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying redis.conf has no appendonly/save line")
			expectRedisConfDirectives(clusterName, 0, 0)

			By("verifying the Redis defaults apply (AOF off, default RDB save points)")
			expectRuntimePersistence(clusterName, nodes, "no", redisDefaultSave)
		})

		It("enables AOF from spec.config without duplicating directives", func() {
			applyRedisConfig(clusterName, nodes, chartAOFOnly)

			expectRedisConfDirectives(clusterName, 1, 1)
			expectRuntimePersistence(clusterName, nodes, "yes", "")

			By(fmt.Sprintf("inserting %d keys", testKeys))
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, nodes)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.InsertKeys(clusterNs, podNames[0], testKeys)).To(Succeed())
			expectDBSize(clusterName, nodes, testKeys)
		})

		It("disables AOF and RDB explicitly, keeping the data", func() {
			applyRedisConfig(clusterName, nodes, chartAllDisable)

			expectRedisConfDirectives(clusterName, 1, 1)
			expectRuntimePersistence(clusterName, nodes, "no", "")
			expectDBSize(clusterName, nodes, testKeys)
		})

		It("re-enables AOF, keeping the data", func() {
			applyRedisConfig(clusterName, nodes, chartAOFOnly)

			expectRedisConfDirectives(clusterName, 1, 1)
			expectRuntimePersistence(clusterName, nodes, "yes", "")
			expectDBSize(clusterName, nodes, testKeys)
		})
	})
})

// waitForActiveRedisConfigApplied waits until the highest-sequence RedkeyConfig carries the given
// Redis config and is Applied. Matching the config avoids returning on the previous, already
// Applied config before the operator creates the new one.
func waitForActiveRedisConfigApplied(
	ctx context.Context, clusterName, namespace, redisConfig string, timeout time.Duration,
) error {
	var lastPhase string
	err := wait.PollUntilContextTimeout(ctx, framework.DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, namespace)
			if err != nil || len(configs) == 0 {
				return false, nil
			}
			active := configs[len(configs)-1]
			if active.Spec.RedisConfig != redisConfig {
				return false, nil
			}
			lastPhase = active.Status.ConfigPhase
			return lastPhase == redkeyv1beta1.ConfigPhaseApplied, nil
		})
	if err != nil {
		return fmt.Errorf("waiting for the new Redis config to be Applied (last phase %q): %w", lastPhase, err)
	}
	return nil
}

// redisConfigGet returns the value of a Redis configuration parameter (CONFIG GET).
func redisConfigGet(namespace, pod, param string) (string, error) {
	stdout, _, err := framework.ExecInPod(namespace, pod, "redis-cli CONFIG GET "+param)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		return "", nil
	}
	return strings.TrimSpace(lines[1]), nil
}

// countRedisConfDirective returns how many redis.conf lines set the given directive.
func countRedisConfDirective(conf, directive string) int {
	n := 0
	for _, line := range strings.Split(conf, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == directive {
			n++
		}
	}
	return n
}
