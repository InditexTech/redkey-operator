# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
#
# SPDX-License-Identifier: Apache-2.0

# Define the desired Golang version
FROM golang:1.26.8-trixie@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS builder

# install git and basic build tools so xk6 can fetch & build extensions
RUN apt update && apt upgrade -y && apt install -y curl procps build-essential ca-certificates

RUN go install go.k6.io/xk6/cmd/xk6@v1.3.7
RUN xk6 build \
    --with github.com/grafana/xk6-redis \
    --output /k6

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS final
COPY --from=builder /k6 /usr/bin/k6
COPY k6scripts/ /scripts/
ENTRYPOINT ["/usr/bin/k6"]
