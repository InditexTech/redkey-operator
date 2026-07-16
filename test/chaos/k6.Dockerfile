# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
#
# SPDX-License-Identifier: Apache-2.0

# Define the desired Golang version
ARG GOLANG_VERSION=1.26.4


FROM golang:${GOLANG_VERSION}-trixie AS builder

# install git and basic build tools so xk6 can fetch & build extensions
RUN apt update && apt upgrade -y && apt install -y curl procps build-essential ca-certificates

RUN go install go.k6.io/xk6/cmd/xk6@latest
RUN xk6 build \
    --with github.com/grafana/xk6-redis \
    --output /k6

FROM debian:trixie-slim AS final
COPY --from=builder /k6 /usr/bin/k6
COPY k6scripts/ /scripts/
ENTRYPOINT ["/usr/bin/k6"]
