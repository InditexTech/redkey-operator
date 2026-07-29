# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)
# SPDX-License-Identifier: Apache-2.0

#!/usr/bin/env bash
set -euxo pipefail

KIND_VERSION="v0.22.0"
KUBECTL_CHANNEL="stable-1.33"

export GOROOT="${GOROOT:-/usr/local/go}"
export GOPATH="${GOPATH:-/go}"
export PATH="${GOROOT}/bin:${GOPATH}/bin:${PATH}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
	x86_64)
		ARCH="amd64"
		;;
	aarch64|arm64)
		ARCH="arm64"
		;;
	*)
		echo "Unsupported architecture: $ARCH" >&2
		exit 1
		;;
esac

apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gzip make tar
rm -rf /var/lib/apt/lists/*

install_binary() {
	local url="$1"
	local destination="$2"

	curl -fsSL "$url" -o "$destination"
	chmod +x "$destination"
}

install_kind() {
	install_binary "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-${OS}-${ARCH}" /usr/local/bin/kind
}

install_kubebuilder() {
	install_binary "https://go.kubebuilder.io/dl/latest/${OS}/${ARCH}" /usr/local/bin/kubebuilder
}

install_kubectl() {
	local kubectl_version
	kubectl_version="$(curl -fsSL "https://dl.k8s.io/release/${KUBECTL_CHANNEL}.txt")"
	install_binary "https://dl.k8s.io/release/${kubectl_version}/bin/${OS}/${ARCH}/kubectl" /usr/local/bin/kubectl
}

install_helm() {
	local helm_version
	local helm_archive
	local temp_dir

	helm_version="$(curl -fsSL https://api.github.com/repos/helm/helm/releases/latest | grep '"tag_name":' | head -n1 | cut -d '"' -f4)"
	helm_archive="/tmp/helm-${helm_version}-${OS}-${ARCH}.tar.gz"
	temp_dir="$(mktemp -d)"

	curl -fsSL "https://get.helm.sh/helm-${helm_version}-${OS}-${ARCH}.tar.gz" -o "$helm_archive"
	tar -C "$temp_dir" -xzf "$helm_archive"
	install -m 0755 "$temp_dir/${OS}-${ARCH}/helm" /usr/local/bin/helm

	rm -rf "$temp_dir" "$helm_archive"
}

install_kind
install_kubebuilder
install_kubectl
install_helm

if ! docker network inspect kind >/dev/null 2>&1; then
	docker network create -d=bridge --subnet=172.19.0.0/24 kind
fi

kind version
kubebuilder version
helm version --short
docker --version
go version
kubectl version --client=true
