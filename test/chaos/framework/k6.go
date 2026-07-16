// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	k6StartupTimeout = 2 * time.Minute
	k6StopTimeout    = 30 * time.Second
	k6LogTailLines   = int64(200)
	// k6 runs with a very long duration so it keeps generating load until the
	// test explicitly stops it by deleting the deployment.
	k6RunDuration = "24h"
	// k6ProgressPollInterval is how often WaitForK6Progress samples the k6 iteration count.
	k6ProgressPollInterval = 3 * time.Second
)

// k6IterationRe extracts the completed-iteration count from a k6 progress line, e.g.
// "running (0d00h00m44.0s), 10/10 VUs, 447 complete and 0 interrupted iterations".
var k6IterationRe = regexp.MustCompile(`(\d+) complete and \d+ interrupted iterations`)

// K6LoadSelector returns the label selector for k6 load pods.
func K6LoadSelector() string {
	return "app=k6-load"
}

// StartK6LoadDeployment creates a k6 Deployment that generates continuous load against the Redis
// cluster. The deployment keeps running until explicitly stopped via StopK6Load. Returns the
// deployment name.
func StartK6LoadDeployment(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	vus int,
) (string, error) {
	if vus <= 0 {
		vus = GetK6VUs()
	}

	redisHosts, err := getRedisHosts(ctx, clientset, namespace, clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get redis hosts: %w", err)
	}

	deployName := fmt.Sprintf("k6-load-%s", clusterName)
	labels := map[string]string{
		"app":                        "k6-load",
		"redkey.inditex.dev/cluster": clusterName,
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyAlways,
					Containers: []corev1.Container{
						{
							Name:  "k6",
							Image: GetK6Image(),
							Args: []string{
								"run",
								"/scripts/test-300k.js",
								"--duration", k6RunDuration,
								"--vus", fmt.Sprintf("%d", vus),
							},
							Env: []corev1.EnvVar{
								{Name: "REDIS_HOSTS", Value: redisHosts},
							},
						},
					},
				},
			},
		},
	}

	propagation := metav1.DeletePropagationForeground
	_ = clientset.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	_ = wait.PollUntilContextTimeout(ctx, time.Second, k6StopTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
		return errors.IsNotFound(err), nil
	})

	if _, err := clientset.AppsV1().Deployments(namespace).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("failed to create k6 deployment: %w", err)
	}

	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, k6StartupTimeout, true,
		func(ctx context.Context) (bool, error) {
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: K6LoadSelector(),
			})
			if err != nil {
				return false, nil
			}
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		return deployName, fmt.Errorf("k6 deployment pod did not start: %w", err)
	}

	return deployName, nil
}

// StopK6Load deletes the k6 load deployment and waits for its pods to terminate. It is safe to
// call with an empty name.
func StopK6Load(ctx context.Context, clientset kubernetes.Interface, namespace, deployName string) error {
	if deployName == "" {
		return nil
	}

	propagation := metav1.DeletePropagationForeground
	err := clientset.AppsV1().Deployments(namespace).Delete(ctx, deployName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return wait.PollUntilContextTimeout(ctx, time.Second, k6StopTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})
}

// GetK6Logs returns the logs from a running k6 load pod.
func GetK6Logs(ctx context.Context, clientset kubernetes.Interface, namespace string) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: K6LoadSelector(),
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no k6 pods found")
	}

	pod := pods.Items[0]
	tailLines := k6LogTailLines
	opts := &corev1.PodLogOptions{TailLines: &tailLines}
	req := clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Sprintf("Pod %s running but failed to get logs: %v", pod.Name, err), nil
	}
	defer func() { _ = stream.Close() }()

	var buf strings.Builder
	if _, err := io.Copy(&buf, stream); err != nil {
		return fmt.Sprintf("Pod %s running but failed to read logs: %v", pod.Name, err), nil
	}
	return buf.String(), nil
}

// GetK6CompletedIterations returns the most recent completed-iteration count reported by the k6
// load pod, parsed from its progress output. It returns an error when no progress line is present
// (e.g. the pod has not produced output yet).
func GetK6CompletedIterations(ctx context.Context, clientset kubernetes.Interface, namespace string) (int, error) {
	logs, err := GetK6Logs(ctx, clientset, namespace)
	if err != nil {
		return 0, err
	}
	matches := k6IterationRe.FindAllStringSubmatch(logs, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no k6 progress lines found in logs")
	}
	last := matches[len(matches)-1][1]
	n, err := strconv.Atoi(last)
	if err != nil {
		return 0, fmt.Errorf("failed to parse k6 iteration count %q: %w", last, err)
	}
	return n, nil
}

// WaitForK6Progress asserts the k6 load generator is actively driving traffic by polling its
// completed-iteration count until it exceeds the baseline captured at the start. A load generator
// that stays frozen — e.g. stuck on a stale cluster topology after pod IPs churn — never advances
// and causes this to return an error, failing the chaos spec instead of silently passing.
func WaitForK6Progress(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace string,
	timeout time.Duration,
) error {
	baseline, err := GetK6CompletedIterations(ctx, clientset, namespace)
	if err != nil {
		return fmt.Errorf("failed to read baseline k6 iteration count: %w", err)
	}

	last := baseline
	perr := wait.PollUntilContextTimeout(ctx, k6ProgressPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			current, cerr := GetK6CompletedIterations(ctx, clientset, namespace)
			if cerr != nil {
				return false, nil
			}
			last = current
			return current > baseline, nil
		})
	if perr != nil {
		return fmt.Errorf(
			"k6 load generator made no progress within %s: completed iterations stuck at %d (baseline %d)",
			timeout, last, baseline)
	}
	return nil
}

// getRedisHosts returns the seed endpoints for the k6 cluster client.
//
// The first (primary) seed is the cluster's headless Service FQDN
// (<cluster>.<namespace>.svc.cluster.local:6379). Its A-record always resolves to the set of
// *current, ready* pod IPs, so go-redis can always rediscover the live topology from it — even after
// the StatefulSet is recreated (e.g. PurgeKeysOnRebalance) and pod ordinals appear/disappear during
// scaling. Seeding only with per-pod DNS names is fragile: on scale-down the higher ordinals stop
// resolving, and after a purge-driven recreate the client can stay wedged dialing the pods' old IPs.
//
// The per-pod DNS names are appended as additional fallback seeds for the case where the Service
// momentarily has no ready endpoints but an individual pod is already up. We still require at least
// one redis pod so k6 does not start before the cluster exists.
func getRedisHosts(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName string) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no redis pods found")
	}

	// Primary seed: the headless Service FQDN (always resolves to the live, ready pods).
	hosts := []string{fmt.Sprintf("%s.%s.svc.cluster.local:6379", clusterName, namespace)}

	// Fallback seeds: the per-pod stable DNS names.
	for _, pod := range pods.Items {
		if pod.Name != "" {
			hosts = append(hosts, fmt.Sprintf("%s.%s.%s.svc.cluster.local:6379", pod.Name, clusterName, namespace))
			continue
		}
		if pod.Status.PodIP != "" {
			hosts = append(hosts, fmt.Sprintf("%s:6379", pod.Status.PodIP))
		}
	}
	return strings.Join(hosts, ","), nil
}
