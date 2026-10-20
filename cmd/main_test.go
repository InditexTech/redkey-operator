// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestBuildInfoReportsVersion(t *testing.T) {
	families, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "redkey_operator_build_info" {
			continue
		}
		for _, lp := range mf.GetMetric()[0].GetLabel() {
			if lp.GetName() == "version" {
				if lp.GetValue() != version {
					t.Fatalf("build_info version = %q, want %q", lp.GetValue(), version)
				}
				return
			}
		}
		t.Fatal("redkey_operator_build_info has no version label")
	}
	t.Fatal("redkey_operator_build_info not registered")
}

func TestVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the operator binary")
	}

	tests := []struct {
		name    string
		ldflags string
		want    string
	}{
		{name: "injected via ldflags", ldflags: "-X main.version=1.2.3-test", want: "1.2.3-test"},
		{name: "plain build falls back to dev", want: "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "manager")
			args := []string{"build", "-o", bin}
			if tt.ldflags != "" {
				args = append(args, "-ldflags", tt.ldflags)
			}
			args = append(args, ".")
			if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}

			for _, flagName := range []string{"--version", "-version"} {
				out, err := exec.Command(bin, flagName).CombinedOutput()
				if err != nil {
					t.Fatalf("manager %s: %v\n%s", flagName, err, out)
				}
				if got := strings.TrimSpace(string(out)); got != tt.want {
					t.Fatalf("manager %s = %q, want %q", flagName, got, tt.want)
				}
			}
		})
	}
}
