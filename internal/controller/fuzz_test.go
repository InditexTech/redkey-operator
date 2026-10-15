// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

func FuzzMergeMeta(f *testing.F) {
	f.Add("team", "a", "team", "b", ClusterLabel, "c1", true)
	f.Add(ClusterLabel, "evil", "", "", ClusterLabel, "c1", false)
	f.Add("", "", "", "", "", "", false)

	f.Fuzz(func(t *testing.T, sk, sv, ok, ov, bk, bv string, withOverride bool) {
		spec := map[string]string{sk: sv}
		base := map[string]string{bk: bv}
		var override map[string]string
		if withOverride {
			override = map[string]string{ok: ov}
		}

		out := mergeMeta(spec, override, base)

		if got, found := out[bk]; !found || got != bv {
			t.Fatalf("base key %q must always win: got %q, want %q", bk, got, bv)
		}
		if withOverride {
			if ok != bk && out[ok] != ov {
				t.Fatalf("override key %q lost", ok)
			}
			if sk != ok && sk != bk {
				if _, found := out[sk]; found {
					t.Fatalf("spec key %q must be discarded when override is set", sk)
				}
			}
		} else if sk != bk && out[sk] != sv {
			t.Fatalf("spec key %q lost", sk)
		}

		out["\x00mutation"] = "x"
		if len(spec) != 1 || len(base) != 1 || (withOverride && len(override) != 1) {
			t.Fatal("mergeMeta must not alias its inputs")
		}
	})
}

func FuzzNeedsNewConfig(f *testing.F) {
	f.Add("3", int64(3), int64(2), true)
	f.Add("not-a-number", int64(5), int64(4), true)
	f.Add("", int64(1), int64(1), true)
	f.Add("-9223372036854775808", int64(0), int64(0), true)
	f.Add("99999999999999999999", int64(1), int64(0), true)
	f.Add("", int64(2), int64(1), false)

	f.Fuzz(func(t *testing.T, genAnnotation string, generation, observed int64, hasConfig bool) {
		cluster := &redisv1.Redkey{
			ObjectMeta: metav1.ObjectMeta{Generation: generation},
			Status:     redisv1.RedkeyStatus{ObservedGeneration: observed},
		}
		var cfg *redisv1.RedkeyConfig
		if hasConfig {
			cfg = &redisv1.RedkeyConfig{ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{"redkey.inditex.dev/cluster-generation": genAnnotation},
			}}
		}

		got := needsNewConfig(cluster, cfg)

		want := observed < generation
		if hasConfig {
			if parsed, err := strconv.ParseInt(genAnnotation, 10, 64); err == nil && parsed >= generation {
				want = false
			}
		}
		if got != want {
			t.Fatalf("needsNewConfig(gen=%d, observed=%d, annotation=%q) = %v, want %v",
				generation, observed, genAnnotation, got, want)
		}
	})
}
